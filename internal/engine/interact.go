package engine

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Interactor abstracts interactive terminal I/O so that code requiring user
// confirmation (e.g. stale-file deletion) can be tested without real stdin.
type Interactor interface {
	// IsTerminal reports whether stdin is connected to an interactive terminal.
	IsTerminal() bool
	// Prompt displays msg and waits for a single line of input, returning
	// the trimmed, lowercased response. It respects context cancellation.
	Prompt(ctx context.Context, msg string) (string, error)
}

// TTYInteractor reads from a real terminal via the provided reader/writer.
type TTYInteractor struct {
	Stdin  io.Reader
	Stderr io.Writer
}

func (t *TTYInteractor) IsTerminal() bool {
	f, ok := t.Stdin.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return (st.Mode() & os.ModeCharDevice) != 0
}

func (t *TTYInteractor) Prompt(ctx context.Context, msg string) (string, error) {
	if _, err := fmt.Fprint(t.Stderr, msg); err != nil {
		return "", err
	}
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		r := bufio.NewReader(t.Stdin)
		line, err := r.ReadString('\n')
		ch <- result{line, err}
	}()
	select {
	case <-ctx.Done():
		// Unblock the reading goroutine if stdin supports deadlines
		// (true for *os.File on supported platforms). Without this the
		// goroutine stays pinned on ReadString until process exit.
		if f, ok := t.Stdin.(interface{ SetReadDeadline(time.Time) error }); ok {
			f.SetReadDeadline(time.Now())
			<-ch
		}
		return "", ctx.Err()
	case res := <-ch:
		if res.err != nil {
			// EOF means stdin was closed (e.g. Ctrl+D or piped input
			// exhausted). Treat as empty response — callers interpret
			// empty as "declined".
			if errors.Is(res.err, io.EOF) {
				return strings.ToLower(strings.TrimSpace(res.line)), nil
			}
			return "", res.err
		}
		return strings.ToLower(strings.TrimSpace(res.line)), nil
	}
}

// FixedInteractor is a test double that returns canned responses.
type FixedInteractor struct {
	Terminal  bool
	Responses []string // consumed in order; empty slice → always ""
	pos       int
}

func (f *FixedInteractor) IsTerminal() bool { return f.Terminal }

func (f *FixedInteractor) Prompt(_ context.Context, _ string) (string, error) {
	if f.pos >= len(f.Responses) {
		return "", nil
	}
	resp := f.Responses[f.pos]
	f.pos++
	return resp, nil
}
