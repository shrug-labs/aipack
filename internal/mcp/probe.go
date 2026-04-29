package mcp

import (
	"context"
	"fmt"
	"io"

	"github.com/shrug-labs/aipack/internal/domain"
)

// ProbePhase identifies a step in the probe lifecycle. Empty value indicates
// a terminal event (Result or Err is set).
type ProbePhase string

const (
	ProbePhaseStarting     ProbePhase = "starting"      // dispatcher entry; transport identified
	ProbePhaseConnected    ProbePhase = "connected"     // initialize handshake succeeded
	ProbePhaseListingTools ProbePhase = "listing_tools" // about to call tools/list
)

// ProbeEvent is one progress event emitted during a probe operation. Phase
// non-empty marks an intermediate state; Result or Err non-nil marks the
// terminal outcome.
type ProbeEvent struct {
	Server    string
	Transport string // populated on Starting/Connected so consumers don't have to remember
	Phase     ProbePhase
	Result    *ProbeResult
	Err       error
}

// emit pushes the event to events when non-nil. The send is blocking;
// callers must size the channel buffer to absorb the events emitted by a
// single probe (currently up to ~5: Starting, Connected, ListingTools,
// terminal Result/Err) or risk stalling the probe goroutine.
func emitProbeEvent(events chan<- ProbeEvent, ev ProbeEvent) {
	if events == nil {
		return
	}
	events <- ev
}

// Probe dispatches to the transport-specific probe based on server.Transport.
// Empty transport is treated as stdio (matches domain.MCPServer.IsStdio).
//
// Returns an error with the transport label when the value is unrecognized so
// mistyped pack inventories surface quickly ("transport %q" beats a silent skip).
//
// stdout receives human-readable phase progression lines; pass io.Discard or
// nil to suppress. events receives structured ProbeEvent values; pass nil to
// suppress (TUI streaming uses a channel; CLI passes nil).
func Probe(ctx context.Context, server domain.MCPServer, stdout io.Writer, events chan<- ProbeEvent) (*ProbeResult, error) {
	transport := transportLabel(server)
	if transport == "" {
		return nil, fmt.Errorf("unknown transport %q", server.Transport)
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "Probing %s (%s)\n", server.Name, transport)
	}
	emitProbeEvent(events, ProbeEvent{Server: server.Name, Transport: transport, Phase: ProbePhaseStarting})

	var result *ProbeResult
	var err error
	switch {
	case server.IsStdio():
		result, err = ProbeStdio(ctx, server.Command, server.Env, stdout, events)
	case server.Transport == domain.TransportStreamableHTTP:
		result, err = ProbeStreamableHTTP(ctx, server.URL, server.Headers, stdout, events)
	case server.Transport == domain.TransportSSE:
		result, err = ProbeSSE(ctx, server.URL, server.Headers, stdout, events)
	}

	if err != nil {
		if stdout != nil {
			fmt.Fprintf(stdout, "error: %s: %v\n", server.Name, err)
		}
		emitProbeEvent(events, ProbeEvent{Server: server.Name, Err: err})
		return nil, err
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "%s: %d tools\n", server.Name, len(result.Tools))
	}
	emitProbeEvent(events, ProbeEvent{Server: server.Name, Result: result})
	return result, nil
}

// transportLabel returns the canonical transport identifier for a server.
// Empty string indicates an unrecognized transport; callers handle it as an
// error before any probe machinery starts.
func transportLabel(server domain.MCPServer) string {
	switch {
	case server.IsStdio():
		return "stdio"
	case server.Transport == domain.TransportStreamableHTTP:
		return "streamable-http"
	case server.Transport == domain.TransportSSE:
		return "sse"
	default:
		return ""
	}
}
