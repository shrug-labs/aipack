package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
)

type SearchCmd struct {
	Terms     []string `arg:"" optional:"" help:"Search terms (FTS5 full-text search on name, description, and body)"`
	Tags      []string `help:"Filter by tags (comma-separated or repeated)" name:"tags" sep:","`
	Role      string   `help:"Filter by role" name:"role"`
	Kind      string   `help:"Filter by resource kind (rule, skill, workflow, agent, pack)" name:"kind"`
	Pack      string   `help:"Filter by pack name" name:"pack"`
	Category  string   `help:"Filter by category (ops, dev, infra, governance, meta)" name:"category"`
	Installed bool     `help:"Show only installed resources" name:"installed"`
	Available bool     `help:"Show only available (uninstalled) packs" name:"available"`
	JSON      bool     `help:"Emit machine-readable JSON" name:"json"`
	ConfigDir string   `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
}

func (c *SearchCmd) Help() string {
	return `Search the pack index for resources by name, description, body text, tags, role, kind, or category.
Uses FTS5 full-text search with BM25 ranking (name > description > body) when
search terms are provided. Includes body-text snippets showing match context.

Examples:
  # Full-text search (searches name, description, AND body text)
  aipack search 5xx triage

  # Filter by category
  aipack search --category ops

  # Filter by tags and role
  aipack search --tags observability --role oncall-operator

  # Combine text search with filters
  aipack search deploy --kind workflow --category infra

  # Show only installed resources
  aipack search 5xx --installed

  # Show only available (uninstalled) packs from the registry
  aipack search --available

  # JSON output for agent consumption
  aipack search 5xx --json

Categories: ops, dev, infra, governance, meta

See also: query (for raw SQL), registry list (for browsing the registry)`
}

func (c *SearchCmd) Run(g *Globals) error {
	var installed *bool
	if c.Installed {
		t := true
		installed = &t
	} else if c.Available {
		f := false
		installed = &f
	}

	results, err := app.RunIndexSearch(app.IndexSearchRequest{
		ConfigDir: c.ConfigDir,
		Home:      os.Getenv("HOME"),
		Terms:     strings.Join(c.Terms, " "),
		Tags:      c.Tags,
		Role:      c.Role,
		Kind:      c.Kind,
		Pack:      c.Pack,
		Category:  c.Category,
		Installed: installed,
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
	for _, r := range results {
		status := ""
		if !r.Installed {
			status = " (available)"
		}
		cat := ""
		if r.Category != "" {
			cat = fmt.Sprintf(" [%s]", r.Category)
		}
		fmt.Fprintf(g.Stdout, "  [%s] %s/%s%s%s\n", r.Kind, r.Pack, r.Name, cat, status)
		if r.Description != "" {
			fmt.Fprintf(g.Stdout, "    %s\n", r.Description)
		}
		if r.Snippet != "" {
			fmt.Fprintf(g.Stdout, "    %s\n", r.Snippet)
		}
	}
	return nil
}

type QueryCmd struct {
	SQL       string `arg:"" optional:"" help:"SQL query to execute against the index database"`
	Schema    bool   `help:"Print the index database schema" name:"schema"`
	ConfigDir string `help:"Config directory (default: ~/.config/aipack)" name:"config-dir" type:"path"`
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

func (c *QueryCmd) Run(g *Globals) error {
	home := os.Getenv("HOME")

	if c.Schema {
		schema, err := app.RunIndexSchema(c.ConfigDir, home)
		if err != nil {
			return err
		}
		fmt.Fprintln(g.Stdout, schema)
		return nil
	}

	if c.SQL == "" {
		return fmt.Errorf("provide a SQL query or use --schema")
	}

	rows, err := app.RunIndexQuery(c.ConfigDir, home, c.SQL)
	if err != nil {
		return err
	}
	return cmdutil.WriteJSON(g.Stdout, rows)
}
