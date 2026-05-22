package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/shrug-labs/aipack/cmd/aipack/tui"
	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
)

type SearchCmd struct {
	Terms     []string `arg:"" optional:"" help:"Search terms (FTS5 full-text search on name, description, and body)"`
	Tags      []string `help:"Filter by tags (comma-separated or repeated)" name:"tags" sep:","`
	Role      string   `help:"Filter by role" name:"role"`
	Kind      string   `help:"Filter by resource kind (rule, skill, workflow, agent, hook, prompt, plugin, mcp, pack)" name:"kind" predictor:"kind"`
	Pack      string   `help:"Filter by pack name" name:"pack" predictor:"pack"`
	Category  string   `help:"Filter by category (ops, dev, infra, governance, meta)" name:"category" predictor:"category"`
	Status    string   `help:"Filter by discovery status (installed, registered, inspected)" name:"status"`
	Installed bool     `help:"Show only installed resources" name:"installed"`
	Available bool     `help:"Show only available (uninstalled) packs" name:"available"`
	JSON      bool     `help:"Emit machine-readable JSON" name:"json"`
}

func (c *SearchCmd) Help() string {
	return `Open the manage TUI Search tab for interactive search and install flows.
Search terms and common filters seed the TUI. Use --json for machine-readable
index search output; advanced filters such as --tags, --role, and --pack also
use text output.

Examples:
  # Open Search in the manage TUI
  aipack search 5xx triage

  # Open Search filtered by category
  aipack search --category ops

  # Text output with advanced filters
  aipack search --tags observability --role oncall-operator

  # Open Search with text and filters
  aipack search deploy --kind workflow --category infra

  # Show only installed resources
  aipack search 5xx --installed

  # Show only available (uninstalled) packs from the registry
  aipack search --available

  # Show one-off inspected packs
  aipack search --status inspected

  # JSON output for agent consumption
  aipack search 5xx --json

Categories: ops, dev, infra, governance, meta

See also: query (for raw SQL), registry list (for browsing the registry)`
}

func (c *SearchCmd) Run(ctx context.Context, g *Globals) error {
	var installed *bool
	if c.Status != "" && (c.Installed || c.Available) {
		return fmt.Errorf("--status cannot be combined with --installed or --available")
	}
	if c.Status != "" && c.Status != "installed" && c.Status != "registered" && c.Status != "inspected" {
		return fmt.Errorf("invalid --status %q (valid: installed, registered, inspected)", c.Status)
	}
	if c.Installed {
		t := true
		installed = &t
	} else if c.Available {
		f := false
		installed = &f
	}

	if !c.JSON && g.StdinTTY && len(c.Tags) == 0 && c.Role == "" && c.Pack == "" {
		cfgDir, err := cmdutil.EnsureConfigDir(g.ConfigDir, config.HomeDir(), g.Stderr)
		if err != nil {
			return err
		}
		syncCfg, err := config.LoadSyncConfig(config.SyncConfigPath(cfgDir))
		if err != nil {
			return err
		}
		state := c.Status
		if c.Installed {
			state = "installed"
		} else if c.Available {
			state = "available"
		}
		_, err = tui.Run(ctx, tui.RunConfig{
			ConfigDir:          cfgDir,
			SyncCfg:            syncCfg,
			Registry:           g.Registry,
			InitialTab:         "search",
			InitialSearchQuery: strings.Join(c.Terms, " "),
			InitialSearchKind:  c.Kind,
			InitialSearchCat:   c.Category,
			InitialSearchState: state,
		})
		return err
	}

	results, err := app.RunIndexSearch(app.IndexSearchRequest{
		ConfigDir: g.ConfigDir,
		Home:      config.HomeDir(),
		Terms:     strings.Join(c.Terms, " "),
		Tags:      c.Tags,
		Role:      c.Role,
		Kind:      c.Kind,
		Pack:      c.Pack,
		Category:  c.Category,
		Installed: installed,
		Status:    c.Status,
	})
	if err != nil {
		return err
	}

	if c.JSON {
		return cmdutil.WriteJSON(g.Stdout, results)
	}

	if len(results) == 0 {
		fmt.Fprintln(g.Stdout, "No matching resources.")
		return nil
	}

	hasTerms := strings.TrimSpace(strings.Join(c.Terms, " ")) != ""
	printSearchResults(g.Stdout, results, hasTerms)
	return nil
}

// printSearchResults formats search output with a summary header and
// truncated descriptions for readability.
func printSearchResults(w io.Writer, results []app.SearchResult, hasTerms bool) {
	// Summary header: count by kind, grouped if single pack.
	kindCounts := map[string]int{}
	packs := map[string]bool{}
	for _, r := range results {
		kindCounts[r.Kind]++
		packs[r.Pack] = true
	}
	var parts []string
	for _, kind := range []string{"rule", "skill", "workflow", "agent", "hook", "prompt", "plugin", "mcp", "pack"} {
		if n, ok := kindCounts[kind]; ok {
			label := kind + "s"
			if n == 1 {
				label = kind
			}
			parts = append(parts, fmt.Sprintf("%d %s", n, label))
		}
	}
	summary := strings.Join(parts, ", ")
	if len(packs) == 1 {
		for pack := range packs {
			fmt.Fprintf(w, "%s (%s)\n\n", pack, summary)
		}
	} else {
		fmt.Fprintf(w, "%s\n\n", summary)
	}

	for i, r := range results {
		if i > 0 {
			fmt.Fprintln(w)
		}
		status := ""
		if r.Status != "" && r.Status != "installed" {
			status = " (" + r.Status + ")"
		} else if !r.Installed {
			status = " (available)"
		}
		cat := ""
		if r.Category != "" {
			cat = fmt.Sprintf(" [%s]", r.Category)
		}
		if len(packs) == 1 {
			fmt.Fprintf(w, "  [%s] %s%s%s\n", r.Kind, r.Name, cat, status)
		} else {
			fmt.Fprintf(w, "  [%s] %s/%s%s%s\n", r.Kind, r.Pack, r.Name, cat, status)
		}
		if r.Description != "" {
			fmt.Fprintf(w, "    %s\n", truncateDescription(r.Description))
		}
		// Only show body snippets when searching with terms — browsing
		// mode (no terms) shows descriptions only for a cleaner catalog view.
		if hasTerms && r.Snippet != "" {
			fmt.Fprintf(w, "    %s\n", r.Snippet)
		}
	}
}

// truncateDescription returns a readable excerpt of a description, keeping
// complete sentences up to ~200 characters. Agent trigger instructions
// ("ALWAYS trigger when...", "Do NOT trigger for...") are stripped since
// they're useful for the model but noise for humans browsing the catalog.
func truncateDescription(desc string) string {
	const maxLen = 200

	// Find sentence boundaries and include as many as fit within maxLen.
	lastEnd := 0
	for i, ch := range desc {
		if (ch == '.' || ch == '!' || ch == '?') && i > 10 {
			rest := desc[i+1:]
			if rest == "" {
				lastEnd = i + 1
				break
			}
			trimmed := strings.TrimLeft(rest, " ")
			if len(trimmed) > 0 && trimmed[0] >= 'A' && trimmed[0] <= 'Z' {
				candidate := i + 1
				if candidate <= maxLen {
					lastEnd = candidate
				} else if lastEnd > 0 {
					break // already have a good cut point
				} else {
					lastEnd = candidate // first sentence exceeds limit, take it anyway
					break
				}
			}
		}
	}
	if lastEnd > 0 {
		return desc[:lastEnd]
	}
	// No sentence boundary found — hard truncate.
	if len(desc) > maxLen {
		return desc[:maxLen-3] + "..."
	}
	return desc
}

type QueryCmd struct {
	SQL    string `arg:"" optional:"" help:"SQL query to execute against the index database"`
	Schema bool   `help:"Print the index database schema" name:"schema"`
}

func (c *QueryCmd) Help() string {
	return `Execute raw SQL against the pack index database. Returns JSON.

Use --schema to inspect the database tables and columns.

Examples:
  # Show the schema
  aipack query --schema

  # Find all skills with a specific tag
  aipack query "SELECT r.name, r.description FROM resources r JOIN tags t ON t.resource_id = r.id WHERE r.kind = 'skill' AND t.tag = '5xx'"

  # Find resources that require a specific MCP server
  aipack query "SELECT r.name, r.kind FROM resources r JOIN requires q ON q.resource_id = r.id WHERE q.kind = 'mcp' AND q.target = 'monitoring'"

  # List all tags in use
  aipack query "SELECT tag, COUNT(*) as count FROM tags GROUP BY tag ORDER BY count DESC"

  # Show all available (uninstalled) packs
  aipack query "SELECT name, description, repo FROM packs WHERE installed = 0"

See also: search (for convenience FTS search)`
}

func (c *QueryCmd) Run(ctx context.Context, g *Globals) error {
	home := config.HomeDir()

	if c.Schema {
		schema, err := app.RunIndexSchema(g.ConfigDir, home)
		if err != nil {
			return err
		}
		fmt.Fprintln(g.Stdout, schema)
		return nil
	}

	if c.SQL == "" {
		return fmt.Errorf("provide a SQL query or use --schema")
	}

	rows, err := app.RunIndexQuery(g.ConfigDir, home, c.SQL)
	if err != nil {
		return err
	}
	return cmdutil.WriteJSON(g.Stdout, rows)
}
