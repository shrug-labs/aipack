package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
)

// CaptureContentDir reads files matching ext from srcDir, creates flat
// CopyActions (Dst = dstDir/{filename}), and calls parse for each file.
// The parse callback receives (raw bytes, name without extension, absolute
// source path) and should parse the content and append to results; returning
// an error adds a parse warning. If srcDir does not exist, returns nil slices.
func CaptureContentDir(
	srcDir, dstDir, ext string,
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

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ext) {
			continue
		}
		src := filepath.Join(srcDir, e.Name())
		dst := filepath.Join(dstDir, e.Name())
		copies = append(copies, domain.CopyAction{Src: src, Dst: dst, Kind: domain.CopyKindFile})

		raw, readErr := os.ReadFile(src)
		if readErr != nil {
			warnings = append(warnings, domain.Warning{Path: src, Message: fmt.Sprintf("reading file: %v", readErr)})
			continue
		}
		name := strings.TrimSuffix(e.Name(), ext)
		if parseErr := parse(raw, name, src); parseErr != nil {
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
	// Rules.
	copies, warnings := CaptureContentDir(dirs.Rules, "rules", ".md",
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

	// Agents.
	agentParser := parseAgent
	if agentParser == nil {
		agentParser = func(raw []byte, name, _ string) (domain.Agent, error) {
			return engine.ParseAgentBytes(raw, name, "")
		}
	}
	copies, warnings = CaptureContentDir(dirs.Agents, "agents", ".md",
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

	// Workflows.
	copies, warnings = CaptureContentDir(dirs.Workflows, "workflows", ".md",
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

	// Skills.
	skillCopies, skills := CaptureSkills(dirs.Skills, "skills")
	res.Copies = append(res.Copies, skillCopies...)
	res.Skills = append(res.Skills, skills...)
}
