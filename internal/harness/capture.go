package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
)

// CaptureContentDir reads files matching ext directly under srcDir and
// produces CopyActions whose Dst is the pack-relative path the file would
// occupy in a saved pack. The cat parameter routes the filename through
// IDFromHarnessFilename (rules decode `__` → `/`; other categories pass
// through). The parse callback receives raw bytes, the canonical id, and
// the absolute source path; returning an error adds a parse warning.
func CaptureContentDir(
	srcDir, dstDir, ext string,
	cat domain.PackCategory,
	parse func(raw []byte, name, srcPath string) error,
) ([]domain.CopyAction, []domain.Warning) {
	var copies []domain.CopyAction
	var warnings []domain.Warning

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		if !os.IsNotExist(err) {
			warnings = append(warnings, domain.Warning{Path: srcDir, Message: fmt.Sprintf("reading directory: %v", err)})
		}
		return copies, warnings
	}

	lowerExt := strings.ToLower(ext)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), lowerExt) {
			continue
		}
		src := filepath.Join(srcDir, e.Name())
		harnessName := strings.TrimSuffix(e.Name(), ext)
		canonicalID := cat.IDFromHarnessFilename(harnessName)
		dst := filepath.Join(dstDir, filepath.FromSlash(canonicalID)+ext)
		copies = append(copies, domain.CopyAction{Src: src, Dst: dst, Kind: domain.CopyKindFile})

		raw, readErr := os.ReadFile(src)
		if readErr != nil {
			warnings = append(warnings, domain.Warning{Path: src, Message: fmt.Sprintf("reading file: %v", readErr)})
			continue
		}
		if parseErr := parse(raw, canonicalID, src); parseErr != nil {
			warnings = append(warnings, domain.Warning{Path: src, Message: fmt.Sprintf("parse error: %v", parseErr)})
		}
	}

	return copies, warnings
}

// CaptureContent captures rules, agents, workflows, and skills from the given
// directory layout. This is the standard capture loop used by harnesses that
// store each content type in a separate directory.
//
// ParseAgent is an optional custom agent parser (e.g., for reverse-transforming
// harness-native schema). If nil, agents are parsed with engine.ParseAgentBytes.
func CaptureContent(res *CaptureResult, dirs ContentDirs, parseAgent func(raw []byte, name, src string) (domain.Agent, error)) {
	copies, warnings := CaptureContentDir(dirs.Rules, "rules", ".md", domain.CategoryRules,
		func(raw []byte, name, src string) error {
			r, err := engine.ParseRuleBytes(raw, name, "")
			if err != nil {
				return err
			}
			r.SourcePath = src
			res.Rules = append(res.Rules, r)
			return nil
		})
	res.Copies = append(res.Copies, copies...)
	res.Warnings = append(res.Warnings, warnings...)

	agentParser := parseAgent
	if agentParser == nil {
		agentParser = func(raw []byte, name, _ string) (domain.Agent, error) {
			return engine.ParseAgentBytes(raw, name, "")
		}
	}
	copies, warnings = CaptureContentDir(dirs.Agents, "agents", ".md", domain.CategoryAgents,
		func(raw []byte, name, src string) error {
			a, err := agentParser(raw, name, src)
			if err != nil {
				return err
			}
			a.SourcePath = src
			res.Agents = append(res.Agents, a)
			return nil
		})
	res.Copies = append(res.Copies, copies...)
	res.Warnings = append(res.Warnings, warnings...)

	copies, warnings = CaptureContentDir(dirs.Workflows, "workflows", ".md", domain.CategoryWorkflows,
		func(raw []byte, name, src string) error {
			w, err := engine.ParseWorkflowBytes(raw, name, "")
			if err != nil {
				return err
			}
			w.SourcePath = src
			res.Workflows = append(res.Workflows, w)
			return nil
		})
	res.Copies = append(res.Copies, copies...)
	res.Warnings = append(res.Warnings, warnings...)

	skillCopies, skills, skillWarnings := CaptureSkills(dirs.Skills, "skills")
	res.Copies = append(res.Copies, skillCopies...)
	res.Skills = append(res.Skills, skills...)
	res.Warnings = append(res.Warnings, skillWarnings...)
}
