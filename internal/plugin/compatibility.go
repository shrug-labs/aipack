package plugin

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
)

// TargetCompatibility describes component delivery, not installation readiness,
// credential availability, executable trust, or a running client's loaded state.
type TargetCompatibility struct {
	Target      domain.Harness `json:"target"`
	Delivery    string         `json:"delivery"`
	Supported   []string       `json:"supported,omitempty"`
	Unsupported []string       `json:"unsupported,omitempty"`
	Warnings    []string       `json:"warnings,omitempty"`
}

func (c TargetCompatibility) Summary() string {
	if c.Delivery == "native" {
		return string(c.Target) + ": native"
	}
	if len(c.Supported) == 0 {
		text := string(c.Target) + ": unsupported"
		if c.Delivery == "portable" {
			text = string(c.Target) + ": portable (no available components)"
		}
		if len(c.Unsupported) > 0 {
			text += "; " + strings.Join(c.Unsupported, ", ")
		}
		if len(c.Warnings) > 0 {
			text += "; " + strings.Join(c.Warnings, "; ")
		}
		return text
	}
	text := string(c.Target) + ": portable " + strings.Join(c.Supported, ", ")
	if len(c.Unsupported) > 0 {
		text += "; exclude " + strings.Join(c.Unsupported, ", ")
	}
	if len(c.Warnings) > 0 {
		text += "; " + strings.Join(c.Warnings, "; ")
	}
	return text
}

// Compatibility uses the same selection checks as sync preflight. It reads
// selected skill entrypoints but neither renders nor installs the payload.
func Compatibility(s domain.NativePluginSelection, target domain.Harness) TargetCompatibility {
	c := TargetCompatibility{Target: target, Delivery: "unsupported"}
	if target == s.Package.Harness {
		c.Delivery = "native"
	} else if (target == domain.HarnessClaudeCode || target == domain.HarnessOpenCode || target == domain.HarnessCline) && s.Package.Harness == domain.HarnessCodex &&
		(s.Package.Format == CodexLegacy || s.Package.Format == AgentPlugins) && s.Package.ConverterVersion == ConverterVersion {
		c.Delivery = "portable"
	}
	generic := c.Delivery == "portable" && len(s.Selected[domain.CategorySkills]) > 0 && target != domain.HarnessCline && GenericContentSelection(s, target)
	for category, ids := range s.Selected {
		for _, id := range ids {
			label := string(category) + "/" + id
			switch {
			case c.Delivery == "native":
				c.Supported = append(c.Supported, label)
			case c.Delivery == "portable" && category == domain.CategorySkills:
				var reason string
				if generic || target == domain.HarnessCline {
					reason = genericSkillIssue(s, id)
				} else if target == domain.HarnessOpenCode {
					reason = codexOpenCodeSkillIssue(s, id)
				} else {
					reason = codexClaudeSkillIssue(s, id)
				}
				if reason != "" {
					c.Unsupported = append(c.Unsupported, label+" ("+reason+")")
				} else {
					c.Supported = append(c.Supported, label)
				}
			case c.Delivery == "portable" && category == domain.CategoryMCP:
				var err error
				if target == domain.HarnessCline {
					_, err = GenericMCPServer(s, id, target, "/aipack-data")
				} else if target == domain.HarnessOpenCode {
					_, err = codexStdioMCP(s, id, target, "/aipack-payload", "/aipack-data")
				} else {
					_, err = codexClaudeMCP(s, id)
				}
				if err != nil {
					c.Unsupported = append(c.Unsupported, label+" ("+err.Error()+")")
				} else {
					c.Supported = append(c.Supported, label)
					if notice := StartupTimeoutNotice(s, id, target); notice != "" {
						c.Warnings = append(c.Warnings, notice)
					}
				}
			case c.Delivery == "portable" && category == domain.CategoryHooks && target == domain.HarnessClaudeCode:
				if _, err := codexClaudeHookGroups(s, id); err != nil {
					if errors.Is(err, errHookUnavailable) {
						c.Warnings = append(c.Warnings, label+": unavailable on Claude; "+err.Error())
					} else {
						c.Unsupported = append(c.Unsupported, label+" ("+err.Error()+")")
					}
				} else {
					c.Supported = append(c.Supported, label)
					c.Warnings = append(c.Warnings, label+": Claude supplies its native event input and context sizing; Codex-only input fields and per-handler context spilling are not provided")
				}
			case c.Delivery == "portable" && category == domain.CategoryHooks && (target == domain.HarnessOpenCode || target == domain.HarnessCline):
				one := s
				one.Selected = maps.Clone(s.Selected)
				one.Selected[domain.CategoryHooks] = []string{id}
				hooks, warnings, err := GenericHooks(one, target, "/aipack-data")
				if err != nil {
					c.Unsupported = append(c.Unsupported, label+" ("+err.Error()+")")
				} else if len(hooks) > 0 {
					c.Supported = append(c.Supported, label)
				}
				for _, warning := range warnings {
					c.Warnings = append(c.Warnings, warning.Message)
				}
			default:
				c.Unsupported = append(c.Unsupported, label+" (translation is not supported)")
			}
		}
	}
	if s.SettingsEnabled {
		for _, path := range s.Package.SettingsFiles {
			label := "settings/" + path
			if c.Delivery == "native" {
				c.Supported = append(c.Supported, label)
			} else {
				c.Unsupported = append(c.Unsupported, fmt.Sprintf("%s (requires native %s)", label, s.Package.Harness))
			}
		}
	}
	slices.Sort(c.Supported)
	slices.Sort(c.Unsupported)
	slices.Sort(c.Warnings)
	return c
}

// InventoryCompatibility checks all available components, independently of
// profile exclusions. Sync checks the resolved profile selection instead.
func InventoryCompatibility(root string, pkg domain.NativePlugin) []TargetCompatibility {
	s := domain.NativePluginSelection{Root: root, Package: pkg, SettingsEnabled: true, Selected: map[domain.PackCategory][]string{}}
	for category, components := range pkg.Components {
		s.Selected[category] = slices.Sorted(maps.Keys(components))
	}
	var out []TargetCompatibility
	for _, target := range domain.AllHarnesses() {
		out = append(out, Compatibility(s, target))
	}
	return out
}
