package harness

import (
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestMCPTransportCodec_StreamableHTTPRoundTrip(t *testing.T) {
	t.Parallel()
	codec := MCPTransportCodec{StreamableHTTP: "streamableHttp"}

	native := codec.ToNative(domain.TransportStreamableHTTP)
	if native != "streamableHttp" {
		t.Fatalf("ToNative: got %q want streamableHttp", native)
	}
	canonical := codec.ToCanonical(native)
	if canonical != domain.TransportStreamableHTTP {
		t.Fatalf("ToCanonical: got %q want %q", canonical, domain.TransportStreamableHTTP)
	}
}

func TestMCPTransportCodec_DefaultsEmptyToStdio(t *testing.T) {
	t.Parallel()
	codec := MCPTransportCodec{}

	if got := codec.ToNative(""); got != domain.TransportStdio {
		t.Fatalf("ToNative empty: got %q want %q", got, domain.TransportStdio)
	}
	if got := codec.ToCanonical(""); got != domain.TransportStdio {
		t.Fatalf("ToCanonical empty: got %q want %q", got, domain.TransportStdio)
	}
}
