package source

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

func TestNPMCommandWithConfigLock(t *testing.T) {
	t.Parallel()
	command := "npm"
	if runtime.GOOS == "windows" {
		command = "npm.cmd"
	}
	if _, err := exec.LookPath(command); err != nil {
		t.Skip("npm is not installed")
	}
	ctx, unlock, err := util.LockConfigContext(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	out, err := runNPM(ctx, t.TempDir(), []string{"--version"})
	if err != nil || strings.TrimSpace(string(out)) == "" {
		t.Fatalf("npm acquisition command rejected the config lock: %v %s", err, out)
	}
}

func TestClaudeNPMSourceBoundary(t *testing.T) {
	for _, test := range []struct {
		input, expected domain.NPMSource
	}{
		{domain.NPMSource{Package: "@team/probe@1.0.0"}, domain.NPMSource{Package: "@team/probe", Version: "1.0.0"}},
		{domain.NPMSource{Package: "npm:@team/probe", Registry: "http://localhost:1234"}, domain.NPMSource{Package: "@team/probe", Registry: "http://localhost:1234"}},
		{domain.NPMSource{Package: "https://registry.invalid/archive.tgz?signature=fixture"}, domain.NPMSource{Package: "https://registry.invalid/archive.tgz?signature=fixture"}},
	} {
		actual, err := NormalizeClaudeNPMSource(test.input)
		if err != nil || actual != test.expected {
			t.Fatalf("Claude npm normalization: %+v %v", actual, err)
		}
	}
	for _, spec := range []domain.NPMSource{
		{Package: "file:./probe"}, {Package: "git+https://registry.invalid/repo"}, {Package: "--script=probe"},
		{Package: "probe@1", Version: "2"}, {Package: "probe@"}, {Package: "https://registry.invalid/archive.tgz", Version: "1"},
		{Package: "https://user@registry.invalid/archive.tgz"}, {Package: "https://registry.invalid/archive.tgz#fragment"},
		{Package: "https://registry.invalid/archive.tgz\n"}, {Package: "https://"}, {Package: "ftp://registry.invalid/archive.tgz"},
		{Package: "probe", Registry: "http://user@registry.invalid"},
	} {
		if _, err := NormalizeClaudeNPMSource(spec); err == nil {
			t.Fatalf("accepted invalid Claude npm source: %+v", spec)
		}
	}
}

func TestNPMSourceBoundary(t *testing.T) {
	for _, spec := range []domain.NPMSource{
		{Package: "probe"}, {Package: "@team/probe", Version: "^1.0.0", Registry: "https://registry.example.invalid/path"},
	} {
		if err := ValidateNPMSource(spec); err != nil {
			t.Fatal(err)
		}
	}
	for _, spec := range []domain.NPMSource{
		{}, {Package: "../probe"}, {Package: "@team"}, {Package: "probe@1"},
		{Package: "probe", Version: "file:payload"}, {Package: "probe", Version: "../payload"},
		{Package: "probe", Registry: "http://localhost"}, {Package: "probe", Registry: "https://user@registry.invalid"},
		{Package: "probe", Registry: "https://registry.invalid?token=x"}, {Package: "probe", Registry: "https://registry.invalid#fragment"},
	} {
		if err := ValidateNPMSource(spec); err == nil {
			t.Fatalf("accepted unsafe npm source: %+v", spec)
		}
	}
	for _, entry := range []struct {
		name string
		kind byte
	}{
		{"package/run.sh", tar.TypeReg}, {"../escape", tar.TypeReg}, {"package/a/../escape", tar.TypeReg},
		{"/absolute", tar.TypeReg}, {"package/link", tar.TypeSymlink}, {"package/hard", tar.TypeLink}, {"package/fifo", tar.TypeFifo},
	} {
		t.Run(entry.name, func(t *testing.T) {
			var buffer bytes.Buffer
			writer := tar.NewWriter(&buffer)
			size := int64(0)
			if entry.kind == tar.TypeReg {
				size = 4
			}
			if err := writer.WriteHeader(&tar.Header{Name: entry.name, Typeflag: entry.kind, Size: size, Mode: 0o755, Linkname: "run.sh"}); err != nil {
				t.Fatal(err)
			}
			if size > 0 {
				if _, err := writer.Write([]byte("exec")); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			destination := t.TempDir()
			err := extractNPMArchive(context.Background(), &buffer, destination)
			if entry.name != "package/run.sh" {
				if err == nil {
					t.Fatal("unsafe archive accepted")
				}
				return
			}
			info, statErr := os.Stat(filepath.Join(destination, entry.name))
			if err != nil || statErr != nil || info.Mode().Perm() != 0o755 {
				t.Fatalf("executable mode not retained: %v %v", err, statErr)
			}
		})
	}
	var tooLarge bytes.Buffer
	writer := tar.NewWriter(&tooLarge)
	if err := writer.WriteHeader(&tar.Header{Name: "package/large", Mode: 0o644, Size: npmExtractedLimit + 1}); err != nil {
		t.Fatal(err)
	}
	if err := extractNPMArchive(context.Background(), &tooLarge, t.TempDir()); err == nil {
		t.Fatal("extracted size limit ignored")
	}
}
