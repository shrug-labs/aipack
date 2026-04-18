package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shrug-labs/aipack/internal/app"
	"github.com/shrug-labs/aipack/internal/cmdutil"
	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
)

// MCPCmd groups MCP server operations.
type MCPCmd struct {
	InspectTools MCPInspectToolsCmd `cmd:"inspect-tools" help:"Query MCP servers for their live tool inventory"`
}

// MCPInspectToolsCmd probes MCP servers and reports their tool lists.
type MCPInspectToolsCmd struct {
	Server      string `help:"Server to inspect: 'name' or 'pack/name' for disambiguation" name:"server" arg:"" optional:""`
	All         bool   `help:"Inspect all servers across all installed packs" name:"all"`
	Profile     string `help:"Profile to source {params.*} from (default: sync-config default profile)" name:"profile" predictor:"profile"`
	ProfilePath string `help:"Explicit path to a profile YAML (wins over --profile)" name:"profile-path" type:"path"`
	Timeout     int    `help:"Per-server timeout in seconds" name:"timeout" default:"30"`
	Save        bool   `help:"Write discovered tools back to pack inventory JSON" name:"save"`
	DryRun      bool   `help:"Preview --save without writing (requires --save)" name:"dry-run"`
	JSON        bool   `help:"Emit machine-readable JSON output" name:"json"`
}

func (c *MCPInspectToolsCmd) Validate() error {
	if c.Server != "" && c.All {
		return fmt.Errorf("server argument and --all cannot be combined")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("--timeout must be > 0")
	}
	if c.DryRun && !c.Save {
		return fmt.Errorf("--dry-run has no effect without --save")
	}
	return nil
}

func (c *MCPInspectToolsCmd) Help() string {
	return `Start an MCP server, query its tool inventory via the MCP protocol
(initialize + tools/list), and compare against the static available_tools
in the pack's inventory JSON.

Without arguments, lists all MCP servers across installed packs.
With a server name, probes that server. With --all, probes every server.

Server names are looked up across all installed packs. If a name exists in
multiple packs, specify pack/server to disambiguate:

  aipack mcp inspect-tools my-team-pack/my-server

With --save, write the discovered tools back to the pack's mcp/<server>.json.
Combine with --dry-run to preview which inventories would be written and
what changed, without touching disk. Profile params (--profile) are used
to resolve {params.*} refs in server commands; the default is the active
profile.

Stdio, streamable-http, and SSE transports are all supported. Streamable-http
accepts both application/json and text/event-stream responses; SSE uses the
legacy bidirectional transport (GET stream + POST). HTTP transports include
status code and response body snippet in errors so auth failures are legible.

Examples:
  # List available MCP servers
  aipack mcp inspect-tools

  # Inspect a server
  aipack mcp inspect-tools my-server

  # Inspect and save to pack inventory
  aipack mcp inspect-tools my-server --save

  # Preview what --save would write, without touching disk
  aipack mcp inspect-tools my-server --save --dry-run

  # Inspect all servers
  aipack mcp inspect-tools --all

  # Use a different profile for params
  aipack mcp inspect-tools my-server --profile ops

See also: doctor, sync, status`
}

func (c *MCPInspectToolsCmd) Run(ctx context.Context, g *Globals) error {
	result := app.RunMCPInspectTools(ctx, app.MCPInspectToolsRequest{
		ConfigDir:   g.ConfigDir,
		ProfileName: c.Profile,
		ProfilePath: c.ProfilePath,
		Home:        config.HomeDir(),
		ServerRef:   c.Server,
		All:         c.All,
		Timeout:     time.Duration(c.Timeout) * time.Second,
		Save:        c.Save,
		DryRun:      c.DryRun,
	})

	if c.JSON {
		if err := cmdutil.WriteJSON(g.Stdout, result); err != nil {
			return err
		}
		return exitCodeFor(result)
	}

	printDiscoveryWarnings(result.Warnings, g)

	if result.ListMode {
		printServerList(result, g)
		return nil
	}

	printInspectToolsHuman(result, g)
	return exitCodeFor(result)
}

// printDiscoveryWarnings writes per-file discovery warnings to stderr so the
// inventory the human-mode output is built from stays trustworthy.
func printDiscoveryWarnings(warnings []string, g *Globals) {
	for _, w := range warnings {
		fmt.Fprintf(g.Stderr, "warning: %s\n", w)
	}
}

// exitCodeFor maps a result to an ExitError. Bad user input (unknown or
// ambiguous server name) returns ExitUsage; other failures return ExitFail.
func exitCodeFor(result app.MCPInspectToolsResult) error {
	if result.OK {
		return nil
	}
	if result.InputError {
		return ExitError{Code: cmdutil.ExitUsage}
	}
	return ExitError{Code: cmdutil.ExitFail}
}

func printServerList(result app.MCPInspectToolsResult, g *Globals) {
	if len(result.Servers) == 0 {
		fmt.Fprintln(g.Stdout, "No MCP servers found in installed packs.")
		return
	}
	fmt.Fprintf(g.Stdout, "MCP servers across installed packs (%d):\n\n", len(result.Servers))

	// Find max widths for alignment.
	maxName, maxPack := 0, 0
	for _, s := range result.Servers {
		if len(s.ServerName) > maxName {
			maxName = len(s.ServerName)
		}
		if len(s.PackName) > maxPack {
			maxPack = len(s.PackName)
		}
	}

	for _, s := range result.Servers {
		transport := s.Transport
		if transport == "" {
			transport = domain.TransportStdio
		}
		tools := fmt.Sprintf("%d tools", s.ToolCount)
		if s.ToolCount == 0 {
			tools = "no inventory"
		}
		fmt.Fprintf(g.Stdout, "  %-*s  %-*s  %-6s  %s\n",
			maxName, s.ServerName, maxPack, s.PackName, transport, tools)
	}
	fmt.Fprintf(g.Stdout, "\nInspect a server:  aipack mcp inspect-tools <server>\n")
	fmt.Fprintf(g.Stdout, "Inspect all:       aipack mcp inspect-tools --all\n")
}

func printInspectToolsHuman(result app.MCPInspectToolsResult, g *Globals) {
	var okCount, skipCount, errCount int

	for _, sr := range result.Results {
		switch sr.Status {
		case app.InspectStatusOK:
			okCount++
			suffix := ""
			if sr.Saved || sr.WouldSave {
				diff := ""
				if len(sr.Added) > 0 || len(sr.Removed) > 0 {
					diff = fmt.Sprintf(": +%d -%d", len(sr.Added), len(sr.Removed))
				}
				tag := "saved"
				if sr.WouldSave {
					tag = "would save"
				}
				suffix = fmt.Sprintf(" [%s%s]", tag, diff)
			}
			fmt.Fprintf(g.Stdout, "inspecting %s... ok (%d tools, %s)%s\n",
				sr.ServerName, sr.ToolCount, sr.Duration, suffix)
			if len(sr.Added) > 0 {
				fmt.Fprintf(g.Stdout, "  + %s\n", strings.Join(sr.Added, ", "))
			}
			if len(sr.Removed) > 0 {
				fmt.Fprintf(g.Stdout, "  - %s\n", strings.Join(sr.Removed, ", "))
			}

		case app.InspectStatusSkipped:
			skipCount++
			reason := sr.Error
			if reason == "" {
				reason = "unknown"
			}
			fmt.Fprintf(g.Stdout, "inspecting %s... skipped (%s)\n", sr.ServerName, reason)

		case app.InspectStatusError:
			errCount++
			name := sr.ServerName
			if name == "" {
				name = "(setup)"
			}
			fmt.Fprintf(g.Stderr, "inspecting %s... error: %s\n", name, sr.Error)
		}
	}

	total := okCount + skipCount + errCount
	if total > 1 {
		fmt.Fprintf(g.Stdout, "\n%d servers inspected, %d ok", total, okCount)
		if skipCount > 0 {
			fmt.Fprintf(g.Stdout, ", %d skipped", skipCount)
		}
		if errCount > 0 {
			fmt.Fprintf(g.Stdout, ", %d failed", errCount)
		}
		fmt.Fprintln(g.Stdout)
	}
}
