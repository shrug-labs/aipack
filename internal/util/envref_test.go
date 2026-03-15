package util

import (
	"os"
	"testing"
)

func TestWalkParamRefs_Basic(t *testing.T) {
	t.Parallel()
	var refs []ParamRef
	err := WalkParamRefs("{params.region}", func(ref ParamRef) error {
		refs = append(refs, ref)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].Prefix != "{params." || refs[0].Name != "region" {
		t.Fatalf("got prefix=%q name=%q", refs[0].Prefix, refs[0].Name)
	}
}

func TestWalkParamRefs_Legacy(t *testing.T) {
	t.Parallel()
	var refs []ParamRef
	_ = WalkParamRefs("{param.x} and {global.y}", func(ref ParamRef) error {
		refs = append(refs, ref)
		return nil
	})
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
	if refs[0].Prefix != "{param." || refs[0].Name != "x" {
		t.Fatalf("ref[0]: prefix=%q name=%q", refs[0].Prefix, refs[0].Name)
	}
	if refs[1].Prefix != "{global." || refs[1].Name != "y" {
		t.Fatalf("ref[1]: prefix=%q name=%q", refs[1].Prefix, refs[1].Name)
	}
}

func TestWalkParamRefs_NoSpuriousParamMatch(t *testing.T) {
	t.Parallel()
	// {params.foo} must NOT produce a second match with prefix "{param." and name "s.foo".
	var refs []ParamRef
	_ = WalkParamRefs("{params.foo}", func(ref ParamRef) error {
		refs = append(refs, ref)
		return nil
	})
	if len(refs) != 1 {
		t.Fatalf("expected exactly 1 ref, got %d: %+v", len(refs), refs)
	}
	if refs[0].Name != "foo" {
		t.Fatalf("expected name=foo, got %q", refs[0].Name)
	}
	if refs[0].Prefix != "{params." {
		t.Fatalf("expected prefix={params., got %q", refs[0].Prefix)
	}
}

func TestWalkParamRefs_Multiple(t *testing.T) {
	t.Parallel()
	var names []string
	_ = WalkParamRefs("a{params.x}b{params.y}c", func(ref ParamRef) error {
		names = append(names, ref.Name)
		return nil
	})
	if len(names) != 2 || names[0] != "x" || names[1] != "y" {
		t.Fatalf("expected [x y], got %v", names)
	}
}

func TestWalkParamRefs_NoRefs(t *testing.T) {
	t.Parallel()
	var count int
	_ = WalkParamRefs("no refs here", func(ref ParamRef) error {
		count++
		return nil
	})
	if count != 0 {
		t.Fatalf("expected 0 refs, got %d", count)
	}
}

func TestWalkEnvRefs_Basic(t *testing.T) {
	t.Parallel()
	var refs []EnvRef
	err := WalkEnvRefs("{env:HOME}", func(ref EnvRef) error {
		refs = append(refs, ref)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Name != "HOME" {
		t.Fatalf("expected [HOME], got %+v", refs)
	}
}

func TestWalkEnvRefs_EmptyName(t *testing.T) {
	t.Parallel()
	err := WalkEnvRefs("{env:}", func(ref EnvRef) error { return nil })
	if err == nil {
		t.Fatal("expected error for empty env ref name")
	}
}

func TestWalkEnvRefs_Unterminated(t *testing.T) {
	t.Parallel()
	err := WalkEnvRefs("{env:FOO", func(ref EnvRef) error { return nil })
	if err == nil {
		t.Fatal("expected error for unterminated env ref")
	}
}

func TestExpandEnvRefs_Set(t *testing.T) {
	// Cannot use t.Parallel with t.Setenv.
	t.Setenv("AIPACK_TEST_VAR", "hello")
	out, err := ExpandEnvRefs("{env:AIPACK_TEST_VAR}")
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello" {
		t.Fatalf("expected hello, got %q", out)
	}
}

func TestExpandEnvRefs_Empty(t *testing.T) {
	// Empty-but-set env vars should expand to the empty string.
	t.Setenv("AIPACK_TEST_EMPTY", "")
	out, err := ExpandEnvRefs("{env:AIPACK_TEST_EMPTY}")
	if err != nil {
		t.Fatalf("unexpected error for set-but-empty var: %v", err)
	}
	if out != "" {
		t.Fatalf("expected empty string, got %q", out)
	}
}

func TestExpandEnvRefs_Unset(t *testing.T) {
	// Truly unset env vars should error.
	os.Unsetenv("AIPACK_TEST_TRULY_UNSET")
	_, err := ExpandEnvRefs("{env:AIPACK_TEST_TRULY_UNSET}")
	if err == nil {
		t.Fatal("expected error for unset env var")
	}
}

func TestExpandEnvRefs_NoRefs(t *testing.T) {
	t.Parallel()
	out, err := ExpandEnvRefs("no refs")
	if err != nil {
		t.Fatal(err)
	}
	if out != "no refs" {
		t.Fatalf("expected passthrough, got %q", out)
	}
}
