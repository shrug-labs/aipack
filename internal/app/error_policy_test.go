package app

import (
	"errors"
	"testing"
)

func TestWrapFatalSync(t *testing.T) {
	t.Parallel()
	root := errors.New("boom")
	err := wrapFatalSync("plan sync", root)
	if err == nil {
		t.Fatal("expected wrapped error")
	}
	var fatal FatalSyncError
	if !errors.As(err, &fatal) {
		t.Fatalf("expected FatalSyncError, got %T", err)
	}
	if fatal.Step != "plan sync" {
		t.Fatalf("Step = %q, want %q", fatal.Step, "plan sync")
	}
	if !errors.Is(err, root) {
		t.Fatal("wrapped error should unwrap to original")
	}
}

func TestWarningHelpers(t *testing.T) {
	t.Parallel()
	w := warningf("index", "failed: %s", "x")
	if w.Field != "index" || w.Message != "failed: x" {
		t.Fatalf("warningf = %+v", w)
	}
	wp := warningPathf("/tmp/a", "registry", "bad: %d", 1)
	if wp.Path != "/tmp/a" || wp.Field != "registry" || wp.Message != "bad: 1" {
		t.Fatalf("warningPathf = %+v", wp)
	}
}
