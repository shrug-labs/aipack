package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

// GenericContentSelection identifies imports representable as ordinary content.
// Unselected native components do not prevent ordinary skill delivery.
func GenericContentSelection(s domain.NativePluginSelection, target domain.Harness) bool {
	if s.Package.Harness != domain.HarnessCodex || (s.Package.Format != CodexLegacy && s.Package.Format != AgentPlugins) || s.Package.ConverterVersion != ConverterVersion || !domain.ValidNativeName(s.Package.Name) || !domain.ValidNativeName(s.Package.Marketplace) {
		return false
	}
	for cat, ids := range s.Selected {
		if cat != domain.CategorySkills && cat != domain.CategoryMCP && cat != domain.CategoryHooks && len(ids) > 0 {
			return false
		}
	}
	if len(s.Selected[domain.CategoryHooks]) > 0 {
		if target == domain.HarnessClaudeCode {
			return false // Claude's native plugin route retains its event protocol.
		}
		if _, _, err := GenericHooks(s, target, "/aipack-data"); err != nil {
			return false
		}
	}
	for _, id := range s.Selected[domain.CategorySkills] {
		if genericSkillIssue(s, id) != "" {
			return false
		}
	}
	for _, id := range s.Selected[domain.CategoryMCP] {
		if _, err := GenericMCPServer(s, id, target, "/aipack-data"); err != nil {
			return false
		}
	}
	return !s.SettingsEnabled || len(s.Package.SettingsFiles) == 0
}

// GenericMCPServer reuses the stdio converter and ordinary MCP adapters. Data
// is created on first native launch, never during inspection or sync preview.
func GenericMCPServer(s domain.NativePluginSelection, id string, target domain.Harness, data string) (domain.MCPServer, error) {
	if target != domain.HarnessCline && target != domain.HarnessClaudeCode && target != domain.HarnessOpenCode {
		return domain.MCPServer{}, fmt.Errorf("generic MCP conversion to %s is not supported", target)
	}
	policy := s.MCPPolicy[id]
	if len(policy.AllowedTools)+len(policy.AlwaysAllowedTools)+len(policy.DisabledTools) > 0 {
		return domain.MCPServer{}, fmt.Errorf("selected plugin tool controls are unsupported on this %s delivery path", target)
	}
	entry, err := codexStdioMCPCommand(s, id, target, filepath.Join(s.Root, "upstream"), data)
	if err != nil {
		return domain.MCPServer{}, err
	}
	if entry["timeout"] != nil && (policy.StartupTimeout != "unified" || target == domain.HarnessClaudeCode) {
		return domain.MCPServer{}, fmt.Errorf("plugin tool timeout requires native delivery")
	}
	args := entry["args"].([]string)
	env, _ := entry["env"].(map[string]string)
	values := append([]string{}, args...)
	for key, value := range env {
		values = append(values, key, value)
	}
	for _, value := range values {
		if strings.Contains(value, "{env:") || strings.Contains(value, "{params.") || strings.Contains(value, "{pack:root}") {
			return domain.MCPServer{}, fmt.Errorf("literal pack placeholders require native delivery")
		}
	}
	args = append([]string{"-c", `(umask 077; mkdir -p -- "$1") && shift && ` + args[1], args[2], data}, args[3:]...)
	server := domain.MCPServer{Name: id, SourcePack: s.SourcePack, Transport: domain.TransportStdio, Command: append([]string{"sh"}, args...), Env: env}
	if timeout, ok := entry["timeout"].(int64); ok {
		server.Timeout = int(timeout / 1000)
	}
	return server, nil
}

func genericSkillIssue(s domain.NativePluginSelection, id string) string {
	if issue := codexOpenCodeSkillIssue(s, id); issue != "" {
		return issue
	}
	root := filepath.Join(s.Root, "upstream")
	dir := filepath.Dir(filepath.Join(root, s.Package.Components[domain.CategorySkills][id][0]))
	err := util.WalkFilesResolvingSymlinks(dir, root, func(path, _ string, _ os.FileInfo) error {
		if filepath.Base(path) == domain.SkillEntryFile && path != filepath.Join(dir, domain.SkillEntryFile) {
			return fmt.Errorf("nested skill entrypoints require native delivery")
		}
		return nil
	})
	if err != nil {
		return err.Error()
	}
	return ""
}

// GenericSkillBytes retains trigger metadata and resolves native asset/data
// references against the installed source and a retained, non-content data root.
func GenericSkillBytes(raw []byte, dir, root, data string) ([]byte, error) {
	_, body, err := domain.SplitFrontmatter(raw)
	if err != nil {
		return nil, err
	}
	translated := codexClaudeVariables.ReplaceAllStringFunc(string(body), func(variable string) string {
		if strings.Contains(variable, "DATA") {
			return data
		}
		return root
	})
	context := "Plugin asset directory: " + dir + ". Resolve relative plugin file paths against this directory.\n\n"
	return []byte(string(raw[:len(raw)-len(body)]) + context + translated), nil
}
