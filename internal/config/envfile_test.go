package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(`
# comment
PLAIN=value
export EXPORTED=from-export
EMPTY=
SPACED = spaced value
`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadDotEnv(path)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"PLAIN":    "value",
		"EXPORTED": "from-export",
		"EMPTY":    "",
		"SPACED":   "spaced value",
	}
	for key, wantVal := range want {
		if got[key] != wantVal {
			t.Fatalf("%s = %q, want %q", key, got[key], wantVal)
		}
	}
}

func TestLoadDotEnv_MissingFileReturnsEmpty(t *testing.T) {
	t.Parallel()
	got, err := LoadDotEnv(filepath.Join(t.TempDir(), ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("LoadDotEnv missing file returned %v, want empty map", got)
	}
}

func TestLoadDotEnv_InvalidLine(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("not valid\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadDotEnv(path)
	if err == nil {
		t.Fatal("expected invalid line error")
	}
	if strings.Contains(err.Error(), "not valid") {
		t.Fatalf("error leaks line content: %v", err)
	}
	if !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("error %q should mention line 1", err)
	}
}

func TestSetDotEnv_AppendsWhenMissing(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".env")
	if err := SetDotEnv(path, "API_TOKEN", "abc123"); err != nil {
		t.Fatalf("SetDotEnv: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "API_TOKEN=abc123\n" {
		t.Fatalf("file = %q, want %q", got, "API_TOKEN=abc123\n")
	}
}

func TestSetDotEnv_PreservesCommentsAndBlankLines(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".env")
	initial := "# header comment\n\nFOO=keep-me\nAPI_TOKEN=old\n# trailing comment\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SetDotEnv(path, "API_TOKEN", "new-value"); err != nil {
		t.Fatalf("SetDotEnv: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "# header comment\n\nFOO=keep-me\nAPI_TOKEN=new-value\n# trailing comment\n"
	if string(got) != want {
		t.Fatalf("file =\n%q\nwant\n%q", got, want)
	}
}

func TestSetDotEnv_PreservesExportPrefix(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("export PATH_PREFIX=/usr/local/bin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetDotEnv(path, "PATH_PREFIX", "/opt/bin"); err != nil {
		t.Fatalf("SetDotEnv: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "export PATH_PREFIX=/opt/bin\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestSetDotEnv_RejectsInvalidKeyAndNewlineValue(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".env")
	if err := SetDotEnv(path, "1bad", "x"); err == nil {
		t.Fatal("expected invalid key error")
	}
	if err := SetDotEnv(path, "GOOD", "line1\nline2"); err == nil {
		t.Fatal("expected newline value error")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file should not have been created on validation failure: %v", err)
	}
}

func TestUnsetDotEnv_RemovesFirstMatch(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("# comment\nFOO=keep\nAPI_TOKEN=remove-me\nBAR=keep-also\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := UnsetDotEnv(path, "API_TOKEN"); err != nil {
		t.Fatalf("UnsetDotEnv: %v", err)
	}
	got, _ := os.ReadFile(path)
	want := "# comment\nFOO=keep\nBAR=keep-also\n"
	if string(got) != want {
		t.Fatalf("file =\n%q\nwant\n%q", got, want)
	}
}

func TestUnsetDotEnv_MissingKeyIsNoOp(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".env")
	original := "FOO=bar\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UnsetDotEnv(path, "MISSING"); err != nil {
		t.Fatalf("UnsetDotEnv: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != original {
		t.Fatalf("no-op should preserve file, got %q", got)
	}
}

func TestUnsetDotEnv_MissingFileIsNoOp(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".env")
	if err := UnsetDotEnv(path, "ANY"); err != nil {
		t.Fatalf("UnsetDotEnv missing file: %v", err)
	}
}
