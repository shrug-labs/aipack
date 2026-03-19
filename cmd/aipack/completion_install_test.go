package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallCompletionSnippet_Zsh(t *testing.T) {
	t.Parallel()
	snippet := completionSnippet("zsh", "/usr/local/bin/aipack", "aipack")
	if !strings.Contains(snippet, "bashcompinit") {
		t.Fatal("zsh snippet should contain bashcompinit")
	}
	if !strings.Contains(snippet, "complete -C /usr/local/bin/aipack aipack") {
		t.Fatal("zsh snippet should contain complete -C line")
	}
	if !strings.Contains(snippet, "# begin aipack completions") {
		t.Fatal("snippet should have begin marker")
	}
	if !strings.Contains(snippet, "# end aipack completions") {
		t.Fatal("snippet should have end marker")
	}
}

func TestInstallCompletionSnippet_Bash(t *testing.T) {
	t.Parallel()
	snippet := completionSnippet("bash", "/usr/local/bin/aipack", "aipack")
	if strings.Contains(snippet, "bashcompinit") {
		t.Fatal("bash snippet should not contain bashcompinit")
	}
	if !strings.Contains(snippet, "complete -C /usr/local/bin/aipack aipack") {
		t.Fatal("bash snippet should contain complete -C line")
	}
}

func TestInstallCompletionSnippet_Fish(t *testing.T) {
	t.Parallel()
	snippet := completionSnippet("fish", "/usr/local/bin/aipack", "aipack")
	if !strings.Contains(snippet, "__complete_aipack") {
		t.Fatal("fish snippet should contain completion function")
	}
}

func TestInstallCompletionSnippet_Unknown(t *testing.T) {
	t.Parallel()
	snippet := completionSnippet("csh", "/usr/local/bin/aipack", "aipack")
	if snippet != "" {
		t.Fatalf("unknown shell should return empty, got %q", snippet)
	}
}

func TestInstallToRC_FreshFile(t *testing.T) {
	t.Parallel()
	rc := filepath.Join(t.TempDir(), ".zshrc")

	installed, err := installToRC(rc, "zsh", "/usr/local/bin/aipack", "aipack")
	if err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Fatal("expected installed=true for fresh file")
	}

	content, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "complete -C /usr/local/bin/aipack aipack") {
		t.Fatal("rc file should contain completion line")
	}
}

func TestInstallToRC_ExistingContent(t *testing.T) {
	t.Parallel()
	rc := filepath.Join(t.TempDir(), ".zshrc")
	os.WriteFile(rc, []byte("# my zshrc\nexport FOO=bar\n"), 0o644)

	installed, err := installToRC(rc, "zsh", "/usr/local/bin/aipack", "aipack")
	if err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Fatal("expected installed=true")
	}

	content, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	s := string(content)
	if !strings.HasPrefix(s, "# my zshrc\nexport FOO=bar\n") {
		t.Fatal("should preserve existing content")
	}
	if !strings.Contains(s, "complete -C /usr/local/bin/aipack aipack") {
		t.Fatal("should contain completion line")
	}
}

func TestInstallToRC_Idempotent(t *testing.T) {
	t.Parallel()
	rc := filepath.Join(t.TempDir(), ".zshrc")

	installToRC(rc, "zsh", "/usr/local/bin/aipack", "aipack")
	first, _ := os.ReadFile(rc)

	installed, err := installToRC(rc, "zsh", "/usr/local/bin/aipack", "aipack")
	if err != nil {
		t.Fatal(err)
	}
	if installed {
		t.Fatal("expected installed=false on second call (already present)")
	}

	second, _ := os.ReadFile(rc)
	if string(first) != string(second) {
		t.Fatal("file should not change on second install")
	}
}

func TestUninstallFromRC(t *testing.T) {
	t.Parallel()
	rc := filepath.Join(t.TempDir(), ".zshrc")
	os.WriteFile(rc, []byte("# my zshrc\nexport FOO=bar\n"), 0o644)

	installToRC(rc, "zsh", "/usr/local/bin/aipack", "aipack")

	removed, err := uninstallFromRC(rc)
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("expected removed=true")
	}

	content, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	s := string(content)
	if strings.Contains(s, "aipack") {
		t.Fatalf("should have removed all aipack lines, got:\n%s", s)
	}
	if !strings.Contains(s, "export FOO=bar") {
		t.Fatal("should preserve non-aipack content")
	}
}

func TestUninstallFromRC_NotInstalled(t *testing.T) {
	t.Parallel()
	rc := filepath.Join(t.TempDir(), ".zshrc")
	os.WriteFile(rc, []byte("# my zshrc\n"), 0o644)

	removed, err := uninstallFromRC(rc)
	if err != nil {
		t.Fatal(err)
	}
	if removed {
		t.Fatal("expected removed=false when not installed")
	}
}

func TestUninstallFromRC_NoFile(t *testing.T) {
	t.Parallel()
	rc := filepath.Join(t.TempDir(), ".zshrc")

	removed, err := uninstallFromRC(rc)
	if err != nil {
		t.Fatal(err)
	}
	if removed {
		t.Fatal("expected removed=false when file doesn't exist")
	}
}
