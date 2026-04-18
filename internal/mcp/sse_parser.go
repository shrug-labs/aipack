package mcp

import (
	"bufio"
	"fmt"
	"strings"
)

// sseEvent is one parsed Server-Sent Event. Only the fields the MCP transports
// actually consume are modeled; `id:` and other standard SSE fields pass through
// but are unused.
type sseEvent struct {
	event string // event type (e.g. "message", "endpoint")
	data  string // concatenated data: lines joined with "\n"
}

// readSSEEvent parses one SSE event from r. An event terminates on a blank
// line or EOF with at least one data: line accumulated. Per-event size is
// capped at maxMCPMessageBytes so a malicious server can't stream forever
// without a blank line.
func readSSEEvent(r *bufio.Reader) (sseEvent, error) {
	var ev sseEvent
	var dataLines []string
	totalBytes := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if len(dataLines) > 0 {
				ev.data = strings.Join(dataLines, "\n")
				return ev, nil
			}
			return ev, err
		}
		line = strings.TrimRight(line, "\r\n")
		totalBytes += len(line) + 1
		if totalBytes > maxMCPMessageBytes {
			return ev, fmt.Errorf("SSE event exceeds max %d bytes", maxMCPMessageBytes)
		}
		if line == "" {
			if len(dataLines) > 0 {
				ev.data = strings.Join(dataLines, "\n")
			}
			return ev, nil
		}
		if strings.HasPrefix(line, ":") {
			continue // comment
		}
		if after, ok := strings.CutPrefix(line, "data:"); ok {
			dataLines = append(dataLines, strings.TrimPrefix(after, " "))
			continue
		}
		if after, ok := strings.CutPrefix(line, "event:"); ok {
			ev.event = strings.TrimSpace(after)
			continue
		}
		// Other SSE fields (id, retry) pass through unconsumed per spec.
	}
}
