package harness

import "github.com/shrug-labs/aipack/internal/domain"

// MCPTransportCodec maps aipack's canonical MCP transport names to a harness's
// native config vocabulary and back during render/capture.
type MCPTransportCodec struct {
	StreamableHTTP string
}

func (c MCPTransportCodec) ToNative(canonical string) string {
	switch canonical {
	case "", domain.TransportStdio:
		return domain.TransportStdio
	case domain.TransportStreamableHTTP:
		return c.nativeStreamableHTTP()
	default:
		return canonical
	}
}

func (c MCPTransportCodec) ToCanonical(native string) string {
	switch native {
	case "", domain.TransportStdio:
		return domain.TransportStdio
	case c.nativeStreamableHTTP():
		return domain.TransportStreamableHTTP
	default:
		return native
	}
}

func (c MCPTransportCodec) nativeStreamableHTTP() string {
	if c.StreamableHTTP != "" {
		return c.StreamableHTTP
	}
	return domain.TransportStreamableHTTP
}
