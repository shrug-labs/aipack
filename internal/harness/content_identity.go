package harness

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/shrug-labs/aipack/internal/domain"
)

type RenderedContentIdentity struct {
	Pack string
	ID   string
}

func RenderedContentName(pack, contentID string) string {
	if pack == "" || contentID == "" {
		return contentID
	}
	return contentID + domain.RenderedIdentitySeparator + pack
}

func ContentName(namespaced bool, pack, contentID string) string {
	if !namespaced {
		return contentID
	}
	return RenderedContentName(pack, contentID)
}

func ParseRenderedContentName(name string) (RenderedContentIdentity, bool) {
	id, pack, ok := strings.Cut(name, domain.RenderedIdentitySeparator)
	if !ok || pack == "" || id == "" {
		return RenderedContentIdentity{}, false
	}
	return RenderedContentIdentity{Pack: pack, ID: id}, true
}

func ParseKnownRenderedContentName(name string, knownPacks map[string]struct{}) (RenderedContentIdentity, bool) {
	id, ok := ParseRenderedContentName(name)
	if !ok || !knownPack(knownPacks, id.Pack) {
		return RenderedContentIdentity{}, false
	}
	return id, true
}

func StripRenderedContentName(name string) string {
	id, ok := ParseRenderedContentName(name)
	if !ok {
		return name
	}
	return id.ID
}

func StripKnownRenderedContentName(name string, knownPacks map[string]struct{}) (string, bool) {
	id, ok := ParseKnownRenderedContentName(name, knownPacks)
	if !ok {
		return name, false
	}
	return id.ID, true
}

func RenderedCategoryContentName(pack string, cat domain.PackCategory, contentID string) string {
	if cat == domain.CategoryRules {
		contentID = cat.HarnessFilename(contentID)
	}
	return RenderedContentName(pack, contentID)
}

func CategoryContentName(namespaced bool, pack string, cat domain.PackCategory, contentID string) string {
	if cat == domain.CategoryRules {
		contentID = cat.HarnessFilename(contentID)
	}
	return ContentName(namespaced, pack, contentID)
}

func StripRenderedCategoryContentName(name string, cat domain.PackCategory) (string, bool) {
	return stripRenderedCategoryContentName(name, cat, false)
}

func StripRenderedCategoryPathName(name string, cat domain.PackCategory) (string, bool) {
	return stripRenderedCategoryContentName(name, cat, true)
}

func StripKnownRenderedCategoryContentName(name string, cat domain.PackCategory, knownPacks map[string]struct{}) (string, bool) {
	return stripKnownRenderedCategoryContentName(name, cat, false, knownPacks)
}

func StripKnownRenderedCategoryPathName(name string, cat domain.PackCategory, knownPacks map[string]struct{}) (string, bool) {
	return stripKnownRenderedCategoryContentName(name, cat, true, knownPacks)
}

func stripRenderedCategoryContentName(name string, cat domain.PackCategory, pathName bool) (string, bool) {
	return stripCategoryContentName(name, cat, pathName, ParseRenderedContentName)
}

func stripKnownRenderedCategoryContentName(name string, cat domain.PackCategory, pathName bool, knownPacks map[string]struct{}) (string, bool) {
	return stripCategoryContentName(name, cat, pathName, func(name string) (RenderedContentIdentity, bool) {
		return ParseKnownRenderedContentName(name, knownPacks)
	})
}

func stripCategoryContentName(name string, cat domain.PackCategory, pathName bool, parse func(string) (RenderedContentIdentity, bool)) (string, bool) {
	id, ok := parse(name)
	if !ok {
		return "", false
	}
	if pathName && cat == domain.CategoryRules {
		return "", false
	}
	if cat == domain.CategoryRules {
		return cat.IDFromHarnessFilename(id.ID), true
	}
	return id.ID, true
}

func knownPack(knownPacks map[string]struct{}, pack string) bool {
	if len(knownPacks) == 0 {
		return false
	}
	_, ok := knownPacks[pack]
	return ok
}

type SkillRefResolver struct {
	namespaced bool
	byName     map[string][]domain.Skill
}

func NewSkillRefResolver(skills []domain.Skill, namespaced bool) SkillRefResolver {
	resolver := SkillRefResolver{namespaced: namespaced}
	if !namespaced {
		return resolver
	}
	resolver.byName = map[string][]domain.Skill{}
	for _, s := range skills {
		resolver.byName[s.Name] = append(resolver.byName[s.Name], s)
	}
	return resolver
}

func (r SkillRefResolver) Render(sourcePack string, refs []string) ([]string, error) {
	if !r.namespaced || len(refs) == 0 {
		return refs, nil
	}

	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if _, ok := ParseRenderedContentName(ref); ok {
			out = append(out, ref)
			continue
		}
		candidates := r.byName[ref]
		if len(candidates) == 0 {
			out = append(out, ref)
			continue
		}
		chosen := domain.Skill{}
		for _, s := range candidates {
			if s.SourcePack == sourcePack {
				chosen = s
				break
			}
		}
		if chosen.Name == "" {
			if len(candidates) != 1 {
				return nil, fmt.Errorf("skill reference %q from pack %q is ambiguous across packs; add the skill to the same pack or make the reference unique", ref, sourcePack)
			}
			chosen = candidates[0]
		}
		out = append(out, RenderedContentName(chosen.SourcePack, ref))
	}
	return out, nil
}

func RenderedSkillRefsForPack(sourcePack string, refs []string, skills []domain.Skill, namespaced bool) ([]string, error) {
	return NewSkillRefResolver(skills, namespaced).Render(sourcePack, refs)
}

func StripKnownRenderedSkillRefs(refs []string, knownPacks map[string]struct{}) []string {
	if len(refs) == 0 {
		return refs
	}
	out := make([]string, len(refs))
	for i, ref := range refs {
		if stripped, ok := StripKnownRenderedContentName(ref, knownPacks); ok {
			out[i] = stripped
			continue
		}
		out[i] = ref
	}
	return out
}

func RewriteFrontmatterName(raw []byte, name string) ([]byte, error) {
	return rewriteFrontmatter(raw, func(fm map[string]any) {
		fm["name"] = name
	})
}

func RewriteAgentFrontmatter(raw []byte, name string, skills []string, rewriteSkills bool) ([]byte, error) {
	return rewriteFrontmatter(raw, func(fm map[string]any) {
		fm["name"] = name
		if rewriteSkills {
			if len(skills) == 0 {
				delete(fm, "skills")
			} else {
				fm["skills"] = skills
			}
		}
	})
}

func rewriteFrontmatter(raw []byte, mutate func(map[string]any)) ([]byte, error) {
	fmBytes, body, err := domain.SplitFrontmatter(raw)
	if err != nil {
		return nil, err
	}
	fm := map[string]any{}
	if len(fmBytes) > 0 {
		if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
			return nil, err
		}
	}
	mutate(fm)

	out, err := yaml.Marshal(fm)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(bytes.TrimRight(out, "\n"))
	buf.WriteString("\n---\n")
	buf.Write(body)
	return buf.Bytes(), nil
}
