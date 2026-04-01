package engine

import (
	"errors"
	"testing"
)

func TestWrapFatalApply(t *testing.T) {
	t.Parallel()
	root := errors.New("boom")
	err := wrapFatalApply("apply diff", root)
	if err == nil {
		t.Fatal("expected wrapped error")
	}
	var fatal FatalApplyError
	if !errors.As(err, &fatal) {
		t.Fatalf("expected FatalApplyError, got %T", err)
	}
	if fatal.Step != "apply diff" {
		t.Fatalf("Step = %q, want %q", fatal.Step, "apply diff")
	}
	if !errors.Is(err, root) {
		t.Fatal("wrapped error should unwrap to original")
	}
}

func TestNewDefaultInteractor(t *testing.T) {
	t.Parallel()
	eng := New(nil, nil)
	if eng.Interact == nil {
		t.Fatal("expected default interactor")
	}
}

func TestNewPreservesInjectedInteractor(t *testing.T) {
	t.Parallel()
	injected := &FixedInteractor{Terminal: true}
	eng := New(nil, injected)
	if eng.Interact != injected {
		t.Fatal("expected injected interactor to be preserved")
	}
}
