package mcp

import (
	"context"
	"fmt"

	"github.com/shrug-labs/aipack/internal/domain"
)

// Probe dispatches to the transport-specific probe based on server.Transport.
// Empty transport is treated as stdio (matches domain.MCPServer.IsStdio).
//
// Returns an error with the transport label when the value is unrecognized so
// mistyped pack inventories surface quickly ("transport %q" beats a silent skip).
func Probe(ctx context.Context, server domain.MCPServer) (*ProbeResult, error) {
	switch {
	case server.IsStdio():
		return ProbeStdio(ctx, server.Command, server.Env)
	case server.Transport == domain.TransportStreamableHTTP:
		return ProbeStreamableHTTP(ctx, server.URL, server.Headers)
	case server.Transport == domain.TransportSSE:
		return ProbeSSE(ctx, server.URL, server.Headers)
	default:
		return nil, fmt.Errorf("unknown transport %q", server.Transport)
	}
}
