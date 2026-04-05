package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

type taskRunner struct {
	root         string
	version      string
	commit       string
	distribution string
	tags         string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	runner, err := newTaskRunner(root)
	if err != nil {
		return err
	}

	if len(os.Args) < 2 {
		printHelp()
		return nil
	}

	switch os.Args[1] {
	case "build":
		return runner.build(runtime.GOOS, runtime.GOARCH, runner.binaryPath(runtime.GOOS, runtime.GOARCH))
	case "install":
		return runner.install()
	case "fmt":
		return runner.goCmd("fmt", "./...")
	case "fmt-check":
		return runner.fmtCheck()
	case "lint":
		return runner.lint()
	case "test":
		return runner.goCmd("test", runner.goTagArgs("./...")...)
	case "dist":
		return runner.dist()
	case "clean":
		return os.RemoveAll(filepath.Join(runner.root, "dist"))
	case "release-tag-check":
		return runner.releaseTagCheck()
	case "help":
		printHelp()
		return nil
	default:
		return fmt.Errorf("unknown task %q", os.Args[1])
	}
}

func newTaskRunner(root string) (*taskRunner, error) {
	versionBytes, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		return nil, fmt.Errorf("read VERSION: %w", err)
	}

	commit := "unknown"
	if out, err := exec.Command("git", "rev-parse", "--short", "HEAD").CombinedOutput(); err == nil {
		commit = strings.TrimSpace(string(out))
	}

	return &taskRunner{
		root:         root,
		version:      strings.TrimSpace(string(versionBytes)),
		commit:       commit,
		distribution: getenvDefault("DISTRIBUTION", "github"),
		tags:         strings.TrimSpace(os.Getenv("TAGS")),
	}, nil
}

func (r *taskRunner) build(goos, goarch, out string) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	args := []string{"build"}
	args = append(args, r.goTagArgs()...)
	args = append(args,
		"-ldflags", r.ldflags(),
		"-o", out,
		"./cmd/aipack",
	)
	if err := r.runCmd(r.goEnv(goos, goarch), "go", args...); err != nil {
		return err
	}
	return maybeCodeSign(goos, out)
}

func (r *taskRunner) install() error {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	out := r.binaryPath(goos, goarch)
	if err := r.build(goos, goarch, out); err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dstDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(dstDir, filepath.Base(out))
	if err := copyFile(out, dst); err != nil {
		return err
	}
	if err := maybeCodeSign(goos, dst); err != nil {
		return err
	}
	fmt.Printf("Installed: %s (%s)\n", dst, r.version)
	return nil
}

func (r *taskRunner) fmtCheck() error {
	var dirty []string
	err := filepath.WalkDir(r.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if base == ".git" || base == "dist" || base == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		rel, err := filepath.Rel(r.root, path)
		if err != nil {
			return err
		}
		out, err := exec.Command("gofmt", "-l", path).CombinedOutput()
		if err != nil {
			return fmt.Errorf("gofmt -l %s: %w", rel, err)
		}
		if strings.TrimSpace(string(out)) != "" {
			dirty = append(dirty, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(dirty) == 0 {
		return nil
	}
	slices.Sort(dirty)
	for _, path := range dirty {
		fmt.Println(path)
	}
	return errors.New("go files need formatting. Run: go run ./tools/task fmt")
}

func (r *taskRunner) lint() error {
	if err := r.goCmd("vet", r.goTagArgs("./...")...); err != nil {
		return err
	}
	if _, err := exec.LookPath("staticcheck"); err == nil {
		if err := r.runCmd(nil, "staticcheck", r.goTagArgs("./...")...); err != nil {
			return err
		}
	}
	return r.goCmd("fix", "./...")
}

func (r *taskRunner) dist() error {
	targets := [][2]string{
		{"darwin", "arm64"},
		{"darwin", "amd64"},
		{"linux", "amd64"},
		{"windows", "amd64"},
		{"windows", "arm64"},
	}
	for _, target := range targets {
		// Always use platform-suffixed names for dist artifacts so that
		// release pipelines (SHA256SUMS, Homebrew) can find them by name.
		out := r.distArtifactPath(target[0], target[1])
		if err := r.build(target[0], target[1], out); err != nil {
			return err
		}
		fmt.Printf("  %s\n", filepath.ToSlash(out))
	}
	return nil
}

// distArtifactPath returns the platform-suffixed binary path for release artifacts.
// Unlike binaryPath, this always includes the GOOS-GOARCH suffix.
func (r *taskRunner) distArtifactPath(goos, goarch string) string {
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	return filepath.Join(r.root, "dist", fmt.Sprintf("aipack-%s-%s%s", goos, goarch, ext))
}

func (r *taskRunner) releaseTagCheck() error {
	tag := strings.TrimSpace(os.Getenv("TAG"))
	if tag == "" {
		return errors.New("usage: TAG=vX.Y.Z[-suffix] go run ./tools/task release-tag-check")
	}
	base := "v" + r.version
	if tag == base || strings.HasPrefix(tag, base+"-") {
		return nil
	}
	return fmt.Errorf("release tag %s does not match VERSION %s", tag, r.version)
}

func (r *taskRunner) goCmd(subcommand string, args ...string) error {
	cmdArgs := append([]string{subcommand}, args...)
	return r.runCmd(nil, "go", cmdArgs...)
}

func (r *taskRunner) goTagArgs(extra ...string) []string {
	var args []string
	if r.tags != "" {
		args = append(args, "-tags", r.tags)
	}
	args = append(args, extra...)
	return args
}

func (r *taskRunner) ldflags() string {
	return strings.Join([]string{
		"-X main.version=" + r.version,
		"-X main.commit=" + r.commit,
		"-X github.com/shrug-labs/aipack/internal/update.distribution=" + r.distribution,
	}, " ")
}

func (r *taskRunner) binaryPath(goos, goarch string) string {
	name := "aipack"
	if goos == "windows" {
		name += ".exe"
	}
	if goos == runtime.GOOS && goarch == runtime.GOARCH {
		return filepath.Join(r.root, "dist", name)
	}
	return filepath.Join(r.root, "dist", fmt.Sprintf("aipack-%s-%s%s", goos, goarch, filepath.Ext(name)))
}

func (r *taskRunner) goEnv(goos, goarch string) []string {
	env := os.Environ()
	env = append(env, "GOOS="+goos, "GOARCH="+goarch)
	return env
}

func (r *taskRunner) runCmd(env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = r.root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if env != nil {
		cmd.Env = env
	}
	return cmd.Run()
}

func maybeCodeSign(goos, path string) error {
	if goos != "darwin" || runtime.GOOS != "darwin" {
		return nil
	}
	if _, err := exec.LookPath("codesign"); err != nil {
		return nil
	}
	cmd := exec.Command("codesign", "-s", "-", "-f", path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()

	if _, err := out.ReadFrom(in); err != nil {
		return err
	}
	return out.Close()
}

func getenvDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func printHelp() {
	fmt.Println("Available tasks:")
	fmt.Println("  build")
	fmt.Println("  install")
	fmt.Println("  fmt")
	fmt.Println("  fmt-check")
	fmt.Println("  lint")
	fmt.Println("  release-tag-check")
	fmt.Println("  test")
	fmt.Println("  dist")
	fmt.Println("  clean")
	fmt.Println("  help")
}
