package index

import "github.com/shrug-labs/aipack/internal/domain"

// ExtractFromPack converts a domain.Pack into index types.
// Packs produced by extraction are always installed (they came from a sync).
func ExtractFromPack(pack domain.Pack) (PackInfo, []Resource) {
	info := PackInfo{
		Name:      pack.Name,
		Version:   pack.Version,
		Installed: true,
		Source:    "sync",
	}

	var resources []Resource
	for _, r := range pack.Rules {
		resources = append(resources, ResourceFromMetadata("rule", r.Name, r.Frontmatter.Description, r.SourcePath, r.Frontmatter.Metadata, string(r.Body)))
	}
	for _, a := range pack.Agents {
		resources = append(resources, ResourceFromMetadata("agent", a.Name, a.Frontmatter.Description, a.SourcePath, nil, string(a.Body)))
	}
	for _, w := range pack.Workflows {
		resources = append(resources, ResourceFromMetadata("workflow", w.Name, w.Frontmatter.Description, w.SourcePath, w.Frontmatter.Metadata, string(w.Body)))
	}
	for _, s := range pack.Skills {
		resources = append(resources, ResourceFromMetadata("skill", s.Name, s.Frontmatter.Description, s.DirPath, s.Frontmatter.Metadata, string(s.Body)))
	}
	for _, h := range pack.Hooks {
		resources = append(resources, ResourceFromMetadata("hook", h.ID, h.Description, h.SourcePath, nil, h.Description))
	}
	for _, p := range pack.Plugins {
		body := p.Source
		if p.Marketplace != "" {
			body += "\n" + p.Marketplace
		}
		resources = append(resources, Resource{
			Kind:        "plugin",
			Name:        p.Name,
			Description: p.Source,
			Path:        p.SourcePath,
			Body:        body,
		})
	}
	return info, resources
}

// PromptResource builds a Resource from a prompt entry's fields.
// Prompts bypass the domain.Pack pipeline, so they're indexed from the app
// layer's PromptEntry rather than domain types.
func PromptResource(name, description, path, category, body string) Resource {
	return Resource{
		Kind:        "prompt",
		Name:        name,
		Description: description,
		Path:        path,
		Body:        body,
		Category:    category,
	}
}

// ResourceFromMetadata builds a Resource from a kind, name, description, path,
// raw frontmatter metadata map, and markdown body.
func ResourceFromMetadata(kind, name, description, path string, meta map[string]any, body string) Resource {
	return Resource{
		Kind:        kind,
		Name:        name,
		Description: description,
		Owner:       MetaString(meta, "owner"),
		LastUpdated: MetaString(meta, "last_updated"),
		Path:        path,
		Body:        body,
		Category:    MetaString(meta, "category"),
		Tags:        MetaStrings(meta, "tags"),
		Roles:       MetaStrings(meta, "role"),
		Requires:    MetaStrings(meta, "requires"),
	}
}

// MetaString reads a string value from a metadata map. Returns "" if the key
// is missing or the value is not a string.
func MetaString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// MetaStrings reads a string-slice value from a metadata map. Returns nil if
// the key is missing or the value is not a []any of strings.
func MetaStrings(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	slice, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(slice))
	for _, item := range slice {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
