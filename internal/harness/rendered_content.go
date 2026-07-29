package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/engine"
	"github.com/shrug-labs/aipack/internal/util"
)

type renderedMarkdownWrite struct {
	sourceID     string
	renderedName string
	content      []byte
	sourcePack   string
	sourcePath   string
}

func addRenderedMarkdownWrites[T any](f *domain.Fragment, baseDir, subDir string, cat domain.PackCategory, items []T, render func(T) (renderedMarkdownWrite, error)) error {
	for _, item := range items {
		rendered, err := render(item)
		if err != nil {
			return fmt.Errorf("render content identity %s: %w", rendered.sourceID, err)
		}
		dst := filepath.Join(baseDir, subDir, rendered.renderedName+".md")
		f.Writes = append(f.Writes, domain.WriteAction{
			Dst:        dst,
			Content:    rendered.content,
			SourcePack: rendered.sourcePack,
			Src:        rendered.sourcePath,
			Category:   cat,
		})
		f.Desired = append(f.Desired, dst)
	}
	return nil
}

func AddRenderedRuleWrites(f *domain.Fragment, baseDir, subDir string, namespaced bool, rules []domain.Rule) error {
	if !namespaced {
		f.AddRuleWrites(baseDir, subDir, rules)
		return nil
	}
	return addRenderedMarkdownWrites(f, baseDir, subDir, domain.CategoryRules, rules, func(r domain.Rule) (renderedMarkdownWrite, error) {
		sourceName := ruleSourceID(r)
		_, renderedName, raw, err := RenderedRuleForHarness(r, namespaced)
		if err != nil {
			return renderedMarkdownWrite{sourceID: sourceName}, err
		}
		return renderedMarkdownWrite{
			sourceID:     sourceName,
			renderedName: renderedName,
			content:      raw,
			sourcePack:   r.SourcePack,
			sourcePath:   r.SourcePath,
		}, nil
	})
}

func RenderedRuleForHarness(r domain.Rule, namespaced bool) (domain.Rule, string, []byte, error) {
	name := ruleSourceID(r)
	renderedName := CategoryContentName(namespaced, r.SourcePack, domain.CategoryRules, name)
	out := r
	out.Name = renderedName
	out.Frontmatter.Name = renderedName

	raw, err := renderMarkdownWithFrontmatter(r.Raw, func(raw []byte) ([]byte, error) {
		return RewriteFrontmatterName(raw, renderedName)
	}, func() ([]byte, error) {
		return engine.RenderRuleBytes(out)
	})
	if err != nil {
		return domain.Rule{}, "", nil, err
	}
	out.Raw = raw
	return out, renderedName, raw, nil
}

func renderedWorkflowForHarness(w domain.Workflow, namespaced bool) (domain.Workflow, string, []byte, error) {
	name := workflowSourceID(w)
	renderedName := ContentName(namespaced, w.SourcePack, name)

	out := w
	out.Name = renderedName
	out.Frontmatter.Name = renderedName

	raw, err := renderMarkdownWithFrontmatter(w.Raw, func(raw []byte) ([]byte, error) {
		return RewriteFrontmatterName(raw, renderedName)
	}, func() ([]byte, error) {
		return engine.RenderWorkflowBytes(out)
	})
	if err != nil {
		return domain.Workflow{}, "", nil, err
	}
	out.Raw = raw
	return out, renderedName, raw, nil
}

func AddRenderedWorkflowWrites(f *domain.Fragment, baseDir, subDir string, namespaced bool, workflows []domain.Workflow) error {
	if !namespaced {
		f.AddWorkflowWrites(baseDir, subDir, workflows)
		return nil
	}
	return addRenderedMarkdownWrites(f, baseDir, subDir, domain.CategoryWorkflows, workflows, func(w domain.Workflow) (renderedMarkdownWrite, error) {
		name := workflowSourceID(w)
		_, renderedName, raw, err := renderedWorkflowForHarness(w, namespaced)
		if err != nil {
			return renderedMarkdownWrite{sourceID: name}, err
		}
		return renderedMarkdownWrite{
			sourceID:     name,
			renderedName: renderedName,
			content:      raw,
			sourcePack:   w.SourcePack,
			sourcePath:   w.SourcePath,
		}, nil
	})
}

func renderedAgentForHarness(a domain.Agent, namespaced bool, skillRefs SkillRefResolver) (domain.Agent, string, []byte, error) {
	name := agentSourceID(a)
	renderedName := ContentName(namespaced, a.SourcePack, name)

	out := a
	out.Name = renderedName
	out.Frontmatter.Name = renderedName
	renderedSkills, err := skillRefs.Render(a.SourcePack, a.Frontmatter.Skills)
	if err != nil {
		return domain.Agent{}, "", nil, err
	}
	out.Frontmatter.Skills = renderedSkills

	raw, err := renderMarkdownWithFrontmatter(a.Raw, func(raw []byte) ([]byte, error) {
		return RewriteAgentFrontmatter(raw, renderedName, renderedSkills, namespaced)
	}, func() ([]byte, error) {
		return engine.RenderAgentBytes(out)
	})
	if err != nil {
		return domain.Agent{}, "", nil, err
	}
	out.Raw = raw
	return out, renderedName, raw, nil
}

func renderMarkdownWithFrontmatter(raw []byte, rewriteRaw func([]byte) ([]byte, error), renderFresh func() ([]byte, error)) ([]byte, error) {
	if len(raw) > 0 {
		return rewriteRaw(raw)
	}
	return renderFresh()
}

func AddRenderedAgentWrites(f *domain.Fragment, baseDir, subDir string, namespaced bool, agents []domain.Agent, skills []domain.Skill) error {
	if !namespaced {
		f.AddAgentWrites(baseDir, subDir, agents)
		return nil
	}
	skillRefs := NewSkillRefResolver(skills, namespaced)
	return addRenderedMarkdownWrites(f, baseDir, subDir, domain.CategoryAgents, agents, func(a domain.Agent) (renderedMarkdownWrite, error) {
		name := agentSourceID(a)
		_, renderedName, raw, err := renderedAgentForHarness(a, namespaced, skillRefs)
		if err != nil {
			return renderedMarkdownWrite{sourceID: name}, err
		}
		return renderedMarkdownWrite{
			sourceID:     name,
			renderedName: renderedName,
			content:      raw,
			sourcePack:   a.SourcePack,
			sourcePath:   a.SourcePath,
		}, nil
	})
}

func ruleSourceID(r domain.Rule) string {
	if r.Name != "" {
		return r.Name
	}
	return strings.TrimSuffix(filepath.Base(r.SourcePath), filepath.Ext(r.SourcePath))
}

func agentSourceID(a domain.Agent) string {
	if a.Name != "" {
		return a.Name
	}
	return strings.TrimSuffix(filepath.Base(a.SourcePath), filepath.Ext(a.SourcePath))
}

func workflowSourceID(w domain.Workflow) string {
	if w.Name != "" {
		return w.Name
	}
	return strings.TrimSuffix(filepath.Base(w.SourcePath), filepath.Ext(w.SourcePath))
}

func AddRenderedSkillCopies(f *domain.Fragment, baseDir, subDir string, namespaced bool, skills []domain.Skill) {
	if !namespaced {
		f.AddSkillCopies(baseDir, subDir, skills)
		return
	}
	for _, s := range skills {
		renderedName := ContentName(namespaced, s.SourcePack, s.Name)
		dstDir := filepath.Join(baseDir, subDir, renderedName)
		f.Desired = append(f.Desired, dstDir)

		entryPath := filepath.Join(s.DirPath, domain.SkillEntryFile)
		raw, err := os.ReadFile(entryPath)
		if err != nil {
			f.Warnings = append(f.Warnings, domain.Warning{
				Path:    entryPath,
				Message: fmt.Sprintf("read SKILL.md for rendered identity: %v; falling back to directory copy", err),
			})
			f.AddSkillCopies(baseDir, subDir, []domain.Skill{s})
			continue
		}

		rendered, err := RewriteFrontmatterName(raw, renderedName)
		if err != nil {
			f.Warnings = append(f.Warnings, domain.Warning{
				Path:    entryPath,
				Message: fmt.Sprintf("rewrite SKILL.md frontmatter for rendered identity: %v; falling back to original content", err),
			})
			rendered = raw
		}

		dstEntry := filepath.Join(dstDir, domain.SkillEntryFile)
		f.Writes = append(f.Writes, domain.WriteAction{
			Dst:        dstEntry,
			Content:    rendered,
			SourcePack: s.SourcePack,
			Src:        entryPath,
			Category:   domain.CategorySkills,
		})
		f.Desired = append(f.Desired, dstEntry)
		addSkillAssetCopies(f, s, dstDir)
	}
}

func addSkillAssetCopies(f *domain.Fragment, skill domain.Skill, dstDir string) {
	for _, rel := range skill.Assets {
		if rel == "" || rel == domain.SkillEntryFile || ignoredAsset(rel) {
			continue
		}
		src := filepath.Join(skill.DirPath, filepath.FromSlash(rel))
		dst := filepath.Join(dstDir, filepath.FromSlash(rel))
		f.Copies = append(f.Copies, domain.CopyAction{
			Src:            src,
			Dst:            dst,
			Kind:           domain.CopyKindFile,
			SourcePack:     skill.SourcePack,
			SourceBoundary: skill.SourceBoundary,
		})
		f.Desired = append(f.Desired, dst)
	}
}

func ignoredAsset(rel string) bool {
	return slices.ContainsFunc(strings.Split(filepath.ToSlash(rel), "/"), util.IgnoredName)
}

func StripRenderedPromotedFrontmatter(fm *PromotedFrontmatter, knownPacks map[string]struct{}) {
	switch fm.SourceType {
	case SourceTypeAgent:
		if name, ok := StripKnownRenderedContentName(fm.Name, knownPacks); ok {
			fm.Name = name
		}
		fm.Skills = StripKnownRenderedSkillRefs(fm.Skills, knownPacks)
	case SourceTypeWorkflow:
		if name, ok := StripKnownRenderedContentName(fm.Name, knownPacks); ok {
			fm.Name = name
		}
	}
}

type CapturedSkillEntry struct {
	Abs         string
	DirName     string
	SkillFile   string
	Raw         []byte
	Body        []byte
	Frontmatter PromotedFrontmatter
}

func CaptureRenderedPlainSkill(entry CapturedSkillEntry, knownPacks map[string]struct{}, res *CaptureResult) {
	name := entry.DirName
	pathRendered := false
	if id, ok := StripKnownRenderedCategoryContentName(entry.DirName, domain.CategorySkills, knownPacks); ok {
		name = id
		pathRendered = true
	}
	frontmatterRendered := false
	if id, ok := StripKnownRenderedCategoryContentName(entry.Frontmatter.Name, domain.CategorySkills, knownPacks); ok {
		name = id
		frontmatterRendered = true
	}
	if !pathRendered && !frontmatterRendered {
		capturePlainSkillCopy(entry.Abs, entry.DirName, res)
		return
	}

	content := entry.Raw
	if frontmatterRendered {
		var err error
		content, err = RewriteFrontmatterName(entry.Raw, name)
		if err != nil {
			res.Warnings = append(res.Warnings, domain.Warning{
				Path:    entry.SkillFile,
				Message: fmt.Sprintf("strip rendered skill identity: %v", err),
			})
			capturePlainSkillCopy(entry.Abs, entry.DirName, res)
			return
		}
	}

	res.Writes = append(res.Writes, domain.WriteAction{
		Dst:          filepath.Join("skills", name, domain.SkillEntryFile),
		Content:      content,
		Src:          entry.SkillFile,
		Category:     domain.CategorySkills,
		IsContent:    true,
		SourceDigest: domain.SingleFileDigest(entry.Raw),
	})
	res.Skills = append(res.Skills, domain.Skill{Name: name, DirPath: entry.Abs})
	captureSkillAssets(entry.Abs, name, res)
}

func CaptureRenderedSkills(skillsDir string, knownPacks map[string]struct{}, res *CaptureResult) {
	if len(knownPacks) == 0 {
		copies, skills, warnings := CaptureSkills(skillsDir, "skills")
		res.Copies = append(res.Copies, copies...)
		res.Skills = append(res.Skills, skills...)
		res.Warnings = append(res.Warnings, warnings...)
		return
	}

	warnings := listSkillDirs(skillsDir, func(abs, dirName string) {
		if !renderedIdentityCandidate(dirName) {
			capturePlainSkillCopy(abs, dirName, res)
			return
		}
		entry, ok := readCapturedSkillEntry(abs, dirName, res)
		if ok {
			CaptureRenderedPlainSkill(entry, knownPacks, res)
		}
	})
	res.Warnings = append(res.Warnings, warnings...)
}

func captureSkillEntries(skillsDir string, res *CaptureResult, visit func(CapturedSkillEntry)) {
	warnings := listSkillDirs(skillsDir, func(abs, dirName string) {
		entry, ok := readCapturedSkillEntry(abs, dirName, res)
		if ok {
			visit(entry)
		}
	})
	res.Warnings = append(res.Warnings, warnings...)
}

func readCapturedSkillEntry(abs, dirName string, res *CaptureResult) (CapturedSkillEntry, bool) {
	skillFile := filepath.Join(abs, domain.SkillEntryFile)
	raw, readErr := os.ReadFile(skillFile)
	if readErr != nil {
		res.Warnings = append(res.Warnings, domain.Warning{
			Path:    skillFile,
			Message: fmt.Sprintf("read SKILL.md: %v", readErr),
		})
		return CapturedSkillEntry{}, false
	}
	fm, body, parseErr := ParsePromotedFrontmatter(raw)
	if parseErr != nil {
		res.Warnings = append(res.Warnings, domain.Warning{
			Path:    skillFile,
			Message: fmt.Sprintf("parse skill frontmatter: %v", parseErr),
		})
		capturePlainSkillCopy(abs, dirName, res)
		return CapturedSkillEntry{}, false
	}
	return CapturedSkillEntry{
		Abs:         abs,
		DirName:     dirName,
		SkillFile:   skillFile,
		Raw:         raw,
		Body:        body,
		Frontmatter: fm,
	}, true
}

func capturePlainSkillCopy(abs, name string, res *CaptureResult) {
	res.Copies = append(res.Copies, domain.CopyAction{
		Src: abs, Dst: filepath.Join("skills", name), Kind: domain.CopyKindDir,
	})
	res.Skills = append(res.Skills, domain.Skill{Name: name, DirPath: abs})
}

func captureSkillAssets(abs, name string, res *CaptureResult) {
	err := filepath.WalkDir(abs, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if util.IgnoredName(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(abs, p)
		if err != nil || rel == domain.SkillEntryFile {
			return nil
		}
		res.Copies = append(res.Copies, domain.CopyAction{
			Src:  p,
			Dst:  filepath.Join("skills", name, rel),
			Kind: domain.CopyKindFile,
		})
		return nil
	})
	if err != nil {
		res.Warnings = append(res.Warnings, domain.Warning{
			Path:    abs,
			Message: fmt.Sprintf("capture skill assets: %v", err),
		})
	}
}

func CaptureRenderedMarkdownContentDir(
	srcDir, dstDir, ext string,
	cat domain.PackCategory,
	knownPacks map[string]struct{},
	parse func(raw []byte, name, srcPath string) error,
) ([]domain.WriteAction, []domain.CopyAction, []domain.Warning) {
	if len(knownPacks) == 0 {
		copies, warnings := CaptureContentDir(srcDir, dstDir, ext, cat, parse)
		return nil, copies, warnings
	}

	var writes []domain.WriteAction
	var copies []domain.CopyAction
	var warnings []domain.Warning

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		if !os.IsNotExist(err) {
			warnings = append(warnings, domain.Warning{Path: srcDir, Message: fmt.Sprintf("reading directory: %v", err)})
		}
		return writes, copies, warnings
	}

	capture := renderedMarkdownCaptureContext{
		srcDir:     srcDir,
		dstDir:     dstDir,
		ext:        ext,
		lowerExt:   strings.ToLower(ext),
		cat:        cat,
		knownPacks: knownPacks,
		parse:      parse,
	}
	for _, e := range entries {
		result := capture.captureFile(e)
		writes = append(writes, result.writes...)
		copies = append(copies, result.copies...)
		warnings = append(warnings, result.warnings...)
	}

	return writes, copies, warnings
}

type renderedMarkdownCaptureFileResult struct {
	writes   []domain.WriteAction
	copies   []domain.CopyAction
	warnings []domain.Warning
}

type renderedMarkdownCaptureContext struct {
	srcDir     string
	dstDir     string
	ext        string
	lowerExt   string
	cat        domain.PackCategory
	knownPacks map[string]struct{}
	parse      func(raw []byte, name, srcPath string) error
}

func (c renderedMarkdownCaptureContext) captureFile(e os.DirEntry) renderedMarkdownCaptureFileResult {
	var result renderedMarkdownCaptureFileResult
	if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), c.lowerExt) {
		return result
	}
	src := filepath.Join(c.srcDir, e.Name())
	harnessName := strings.TrimSuffix(e.Name(), c.ext)
	name := c.cat.IDFromHarnessFilename(harnessName)
	pathRendered := false
	if id, ok := StripKnownRenderedCategoryPathName(harnessName, c.cat, c.knownPacks); ok {
		name = id
		pathRendered = true
	}
	dst := filepath.Join(c.dstDir, filepath.FromSlash(name)+c.ext)

	raw, readErr := os.ReadFile(src)
	if readErr != nil {
		result.warnings = append(result.warnings, domain.Warning{Path: src, Message: fmt.Sprintf("reading file: %v", readErr)})
		return result
	}

	if !pathRendered && !renderedIdentityCandidate(harnessName) {
		if parseErr := c.parse(raw, name, src); parseErr != nil {
			result.warnings = append(result.warnings, domain.Warning{Path: src, Message: fmt.Sprintf("parse error: %v", parseErr)})
		}
		result.copies = append(result.copies, domain.CopyAction{Src: src, Dst: dst, Kind: domain.CopyKindFile})
		return result
	}

	frontmatterRendered := false
	fm := renderedMarkdownFrontmatter(src, raw, &result.warnings)
	if fm != nil {
		if id, ok := StripKnownRenderedCategoryContentName(fm.Name, c.cat, c.knownPacks); ok {
			name = id
			dst = filepath.Join(c.dstDir, filepath.FromSlash(name)+c.ext)
			frontmatterRendered = true
		}
	}

	content, err := renderedMarkdownCaptureContent(raw, name, c.cat, c.knownPacks, pathRendered, frontmatterRendered, fm)
	if err != nil {
		result.warnings = append(result.warnings, domain.Warning{Path: src, Message: fmt.Sprintf("strip rendered content identity: %v", err)})
		return result
	}

	if parseErr := c.parse(content, name, src); parseErr != nil {
		result.warnings = append(result.warnings, domain.Warning{Path: src, Message: fmt.Sprintf("parse error: %v", parseErr)})
	}

	if pathRendered || frontmatterRendered {
		result.writes = append(result.writes, domain.WriteAction{
			Src:          src,
			Dst:          dst,
			Content:      content,
			Category:     c.cat,
			IsContent:    true,
			SourceDigest: domain.SingleFileDigest(raw),
		})
		return result
	}

	result.copies = append(result.copies, domain.CopyAction{Src: src, Dst: dst, Kind: domain.CopyKindFile})
	return result
}

func renderedIdentityCandidate(name string) bool {
	return strings.Contains(name, domain.RenderedIdentitySeparator)
}

func renderedMarkdownFrontmatter(src string, raw []byte, warnings *[]domain.Warning) *PromotedFrontmatter {
	fm, _, err := ParsePromotedFrontmatter(raw)
	if err != nil {
		*warnings = append(*warnings, domain.Warning{Path: src, Message: fmt.Sprintf("parse frontmatter name: %v", err)})
		return nil
	}
	return &fm
}

func renderedMarkdownCaptureContent(raw []byte, name string, cat domain.PackCategory, knownPacks map[string]struct{}, pathRendered, frontmatterRendered bool, fm *PromotedFrontmatter) ([]byte, error) {
	if !pathRendered && !frontmatterRendered {
		return raw, nil
	}
	if cat == domain.CategoryAgents {
		return rewriteRenderedAgentCapture(raw, name, knownPacks, frontmatterRendered, fm)
	}
	if frontmatterRendered {
		return RewriteFrontmatterName(raw, name)
	}
	return raw, nil
}

func rewriteRenderedAgentCapture(raw []byte, name string, knownPacks map[string]struct{}, rewriteName bool, fm *PromotedFrontmatter) ([]byte, error) {
	if fm == nil {
		parsed, _, err := ParsePromotedFrontmatter(raw)
		if err != nil {
			return nil, err
		}
		fm = &parsed
	}
	skills, changed := renderedAgentSkills(*fm, knownPacks)
	if !rewriteName && !changed {
		return raw, nil
	}
	return RewriteAgentFrontmatter(raw, name, skills, changed)
}

func renderedAgentSkills(fm PromotedFrontmatter, knownPacks map[string]struct{}) ([]string, bool) {
	stripped := StripKnownRenderedSkillRefs(fm.Skills, knownPacks)
	if len(stripped) != len(fm.Skills) {
		return stripped, true
	}
	for i := range stripped {
		if stripped[i] != fm.Skills[i] {
			return stripped, true
		}
	}
	return stripped, false
}
