package app

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/shrug-labs/aipack/internal/domain"
	clinepaths "github.com/shrug-labs/aipack/internal/harness/cline"
	codexpaths "github.com/shrug-labs/aipack/internal/harness/codex"
	opencodepaths "github.com/shrug-labs/aipack/internal/harness/opencode"
	"github.com/shrug-labs/aipack/internal/util"
)

// CleanRequest describes a clean run.
type CleanRequest struct {
	TargetSpec
	WipeLedger bool
	Yes        bool
	DryRun     bool

	Stdin           io.Reader
	Stderr          io.Writer
	StdinIsTerminal func() bool
}

// RunClean resets harness capability vectors without bricking the harness.
func RunClean(req CleanRequest) error {
	home := req.Home
	if req.Scope == domain.ScopeGlobal && strings.TrimSpace(home) == "" {
		return fmt.Errorf("HOME is not set (required for global scope)")
	}

	stdin := req.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stderr := req.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	isTerminal := req.StdinIsTerminal
	if isTerminal == nil {
		isTerminal = func() bool {
			f, ok := stdin.(*os.File)
			if !ok {
				return false
			}
			st, err := f.Stat()
			if err != nil {
				return false
			}
			return (st.Mode() & os.ModeCharDevice) != 0
		}
	}

	hs := req.Harnesses
	if len(hs) == 0 {
		hs = domain.AllHarnesses()
	}
	for _, h := range hs {
		if _, ok := domain.ParseHarness(string(h)); !ok {
			return fmt.Errorf("unknown harness: %s", h)
		}
	}

	if req.DryRun {
		ops := buildCleanOps(req.Scope, home, req.ProjectDir, hs, req.WipeLedger)
		for _, op := range ops {
			fmt.Fprintf(stderr, "  would remove: %s\n", op.path())
		}
		return nil
	}

	if !req.Yes && !isTerminal() {
		return fmt.Errorf("refusing to clean without --yes (non-interactive)")
	}

	ops := buildCleanOps(req.Scope, home, req.ProjectDir, hs, req.WipeLedger)

	ctx := cleanRunContext{Yes: req.Yes, Stdin: stdin, Stderr: stderr}
	for _, op := range ops {
		if err := op.run(ctx); err != nil {
			return err
		}
	}
	return nil
}

type cleanRunContext struct {
	Yes    bool
	Stdin  io.Reader
	Stderr io.Writer
}

type cleanOp interface {
	run(ctx cleanRunContext) error
	path() string
}

type removePathOp struct {
	Path string
}

func (o removePathOp) path() string { return o.Path }

func (o removePathOp) run(ctx cleanRunContext) error {
	if o.Path == "" || filepath.Clean(o.Path) == "." {
		return fmt.Errorf("invalid clean path: %q", o.Path)
	}
	if _, err := os.Stat(o.Path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !ctx.Yes {
		ok, err := cleanPromptYesNo(ctx.Stdin, ctx.Stderr, fmt.Sprintf("Delete path? %s [y/N]: ", o.Path))
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
	return os.RemoveAll(o.Path)
}

type editJSONOp struct {
	FilePath string
	Edit     func(root map[string]any)
}

func (o editJSONOp) path() string { return o.FilePath }

func (o editJSONOp) run(ctx cleanRunContext) error {
	if o.FilePath == "" || filepath.Clean(o.FilePath) == "." {
		return fmt.Errorf("invalid JSON config path: %q", o.FilePath)
	}
	b, err := os.ReadFile(o.FilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	root := map[string]any{}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &root); err != nil {
			return err
		}
	}
	o.Edit(root)
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	if !ctx.Yes {
		ok, err := cleanPromptYesNo(ctx.Stdin, ctx.Stderr, fmt.Sprintf("Update config (surgical reset)? %s [y/N]: ", o.FilePath))
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
	return util.WriteFileAtomic(o.FilePath, out)
}

type editTOMLOp struct {
	FilePath string
	Edit     func(root map[string]any)
}

func (o editTOMLOp) path() string { return o.FilePath }

func (o editTOMLOp) run(ctx cleanRunContext) error {
	if o.FilePath == "" || filepath.Clean(o.FilePath) == "." {
		return fmt.Errorf("invalid TOML config path: %q", o.FilePath)
	}
	b, err := os.ReadFile(o.FilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	root := map[string]any{}
	if len(b) > 0 {
		if err := toml.Unmarshal(b, &root); err != nil {
			return err
		}
	}
	o.Edit(root)
	out, err := toml.Marshal(root)
	if err != nil {
		return err
	}
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	if !ctx.Yes {
		ok, err := cleanPromptYesNo(ctx.Stdin, ctx.Stderr, fmt.Sprintf("Update config (surgical reset)? %s [y/N]: ", o.FilePath))
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
	return util.WriteFileAtomic(o.FilePath, out)
}

func buildCleanOps(scope domain.Scope, home string, projectDir string, hs []domain.Harness, wipeLedger bool) []cleanOp {
	var ops []cleanOp

	for _, h := range hs {
		switch h {
		case domain.HarnessOpenCode:
			ops = append(ops, cleanOpenCodeOps(scope, home, projectDir)...)
		case domain.HarnessCodex:
			ops = append(ops, cleanCodexOps(scope, home, projectDir)...)
		case domain.HarnessCline:
			ops = append(ops, cleanClineOps(scope, home, projectDir)...)
		case domain.HarnessClaudeCode:
			ops = append(ops, cleanClaudeCodeOps(scope, home, projectDir)...)
		}
	}

	if wipeLedger {
		if scope == domain.ScopeProject {
			ops = append(ops, removePathOp{Path: filepath.Join(projectDir, ".aipack")})
		} else if home != "" {
			ops = append(ops, removePathOp{Path: filepath.Join(home, ".config", "aipack", "ledger")})
		}
	}

	sort.SliceStable(ops, func(i, j int) bool {
		return ops[i].path() < ops[j].path()
	})

	return ops
}

func cleanOpenCodeOps(scope domain.Scope, home string, projectDir string) []cleanOp {
	var ops []cleanOp
	var base string
	var configPath string
	if scope == domain.ScopeProject {
		base = filepath.Join(projectDir, ".opencode")
		configPath, _ = opencodepaths.SettingsProjectPaths(projectDir)
	} else {
		base = filepath.Join(home, ".config", "opencode")
		configPath, _ = opencodepaths.SettingsGlobalPaths(home)
	}

	ops = append(ops,
		removePathOp{Path: filepath.Join(base, "agents")},
		removePathOp{Path: filepath.Join(base, "commands")},
		removePathOp{Path: filepath.Join(base, "skills")},
		removePathOp{Path: filepath.Join(base, "rules")},
	)

	ops = append(ops, editJSONOp{FilePath: configPath, Edit: func(root map[string]any) {
		root["mcp"] = map[string]any{}
		root["tools"] = map[string]any{}
		root["instructions"] = []any{}
		root["skills"] = map[string]any{}
	}})

	return ops
}

func cleanCodexOps(scope domain.Scope, home string, projectDir string) []cleanOp {
	var ops []cleanOp
	var configPath string
	if scope == domain.ScopeProject {
		configPath = codexpaths.SettingsProjectPath(projectDir)
		ops = append(ops,
			removePathOp{Path: filepath.Join(projectDir, ".agents", "skills")},
			removePathOp{Path: filepath.Join(projectDir, "AGENTS.override.md")},
		)
	} else {
		configPath = codexpaths.SettingsGlobalPath(home)
		codexHome := filepath.Join(home, ".codex")
		ops = append(ops,
			removePathOp{Path: filepath.Join(home, ".agents", "skills")},
			removePathOp{Path: filepath.Join(codexHome, "rules")},
			removePathOp{Path: filepath.Join(codexHome, "AGENTS.override.md")},
		)
	}

	ops = append(ops, editTOMLOp{FilePath: configPath, Edit: func(root map[string]any) {
		delete(root, "mcp_servers")
		if m, ok := root["mcp"].(map[string]any); ok {
			delete(m, "servers")
			if len(m) == 0 {
				delete(root, "mcp")
			} else {
				root["mcp"] = m
			}
		}
	}})

	return ops
}

func cleanClineOps(scope domain.Scope, home string, projectDir string) []cleanOp {
	var ops []cleanOp
	if scope == domain.ScopeProject {
		ops = append(ops,
			removePathOp{Path: filepath.Join(projectDir, ".clinerules")},
		)
		return ops
	}

	ops = append(ops,
		removePathOp{Path: filepath.Join(home, ".cline", "skills")},
		removePathOp{Path: clinepaths.RulesGlobalDir(home)},
		removePathOp{Path: clinepaths.AgentsGlobalDir(home)},
		removePathOp{Path: clinepaths.WorkflowsGlobalDir(home)},
	)

	settingsPath := clinepaths.SettingsGlobalPath(home)
	if settingsPath != "" && filepath.Clean(settingsPath) != "." {
		ops = append(ops, editJSONOp{FilePath: settingsPath, Edit: func(root map[string]any) {
			root["mcpServers"] = map[string]any{}
		}})
	}

	return ops
}

func cleanClaudeCodeOps(scope domain.Scope, home string, projectDir string) []cleanOp {
	var ops []cleanOp
	if scope == domain.ScopeProject {
		ops = append(ops,
			removePathOp{Path: filepath.Join(projectDir, ".claude", "rules")},
			removePathOp{Path: filepath.Join(projectDir, ".claude", "agents")},
			removePathOp{Path: filepath.Join(projectDir, ".claude", "commands")},
			removePathOp{Path: filepath.Join(projectDir, ".claude", "skills")},
		)
		mcpPath := filepath.Join(projectDir, ".mcp.json")
		ops = append(ops, editJSONOp{FilePath: mcpPath, Edit: func(root map[string]any) {
			root["mcpServers"] = map[string]any{}
		}})
		settingsPath := filepath.Join(projectDir, ".claude", "settings.local.json")
		ops = append(ops, editJSONOp{FilePath: settingsPath, Edit: func(root map[string]any) {
			delete(root, "permissions")
		}})
	} else if home != "" {
		ops = append(ops,
			removePathOp{Path: filepath.Join(home, ".claude", "rules")},
			removePathOp{Path: filepath.Join(home, ".claude", "agents")},
			removePathOp{Path: filepath.Join(home, ".claude", "commands")},
			removePathOp{Path: filepath.Join(home, ".claude", "skills")},
		)
	}
	return ops
}

func cleanPromptYesNo(r io.Reader, w io.Writer, msg string) (bool, error) {
	if _, err := fmt.Fprint(w, msg); err != nil {
		return false, err
	}
	br := bufio.NewReader(r)
	line, err := br.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	ans := strings.ToLower(strings.TrimSpace(line))
	return ans == "y" || ans == "yes", nil
}
