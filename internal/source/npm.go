package source

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

const npmArchiveLimit = 50 << 20
const npmExtractedLimit = 250 << 20

func ValidateNPMSource(spec domain.NPMSource) error {
	name := strings.TrimPrefix(spec.Package, "@")
	parts := strings.Split(name, "/")
	scoped := strings.HasPrefix(spec.Package, "@")
	if (!scoped && len(parts) != 1) || (scoped && len(parts) != 2) {
		return fmt.Errorf("invalid npm package name %q", spec.Package)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || (!scoped && (part[0] == '.' || part[0] == '_')) {
			return fmt.Errorf("invalid npm package name %q", spec.Package)
		}
		for _, ch := range part {
			if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || strings.ContainsRune("-_.", ch)) {
				return fmt.Errorf("invalid npm package name %q", spec.Package)
			}
		}
	}
	if spec.Version != "" && (strings.TrimSpace(spec.Version) != spec.Version || spec.Version == "." || spec.Version == ".." || strings.ContainsAny(spec.Version, "/\\:\x00\r\n")) {
		return fmt.Errorf("npm version must be a registry selector")
	}
	if spec.Registry != "" {
		u, err := url.Parse(spec.Registry)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(spec.Registry, "#") || strings.TrimSpace(spec.Registry) != spec.Registry {
			return fmt.Errorf("npm registry must be an HTTPS URL without credentials, query or fragment")
		}
	}
	return nil
}

// FetchNPM uses the user's npm credentials but never runs package scripts or
// installs dependencies. The caller owns and removes the fresh destination.
func FetchNPM(ctx context.Context, spec domain.NPMSource, destination string) (root, version, digest string, err error) {
	if err := ValidateNPMSource(spec); err != nil {
		return "", "", "", err
	}
	return fetchNPM(ctx, spec, destination, spec.Package)
}

// NormalizeClaudeNPMSource follows the independently measured Claude source
// forms without broadening Codex's registry-package-only contract.
func NormalizeClaudeNPMSource(spec domain.NPMSource) (domain.NPMSource, error) {
	if IsHTTPURL(spec.Package) {
		u, err := url.Parse(spec.Package)
		if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(spec.Package, "\x00\r\n") || strings.TrimSpace(spec.Package) != spec.Package {
			return domain.NPMSource{}, fmt.Errorf("npm tarball requires an HTTP or HTTPS URL without credentials or fragment")
		}
		if spec.Version != "" {
			return domain.NPMSource{}, fmt.Errorf("npm tarball sources do not accept a version selector")
		}
	} else {
		spec.Package = strings.TrimPrefix(spec.Package, "npm:")
		if at := strings.LastIndex(spec.Package, "@"); at > 0 {
			if spec.Version != "" {
				return domain.NPMSource{}, fmt.Errorf("npm inline versions cannot be combined with a version field")
			}
			spec.Package, spec.Version = spec.Package[:at], spec.Package[at+1:]
			if spec.Version == "" {
				return domain.NPMSource{}, fmt.Errorf("npm inline version is empty")
			}
		}
	}
	validated := spec
	if IsHTTPURL(validated.Package) {
		validated.Package = "tarball"
	}
	// Claude 2.1.284 accepts explicit HTTP registries, independently of the
	// user's default registry. Keep Codex's HTTPS requirement unchanged.
	validated.Registry = strings.TrimPrefix(validated.Registry, "http://")
	if validated.Registry != spec.Registry {
		validated.Registry = "https://" + validated.Registry
	}
	if err := ValidateNPMSource(validated); err != nil {
		return domain.NPMSource{}, err
	}
	return spec, nil
}

// FetchClaudeNPM acquires the source without running scripts or installing
// dependencies. Native delivery owns the subsequent lockfile setup step.
func FetchClaudeNPM(ctx context.Context, spec domain.NPMSource, destination string) (root, version, digest string, err error) {
	spec, err = NormalizeClaudeNPMSource(spec)
	if err != nil {
		return "", "", "", err
	}
	name := spec.Package
	if IsHTTPURL(name) {
		name = ""
	}
	return fetchNPM(ctx, spec, destination, name)
}

func fetchNPM(ctx context.Context, spec domain.NPMSource, destination, expectedName string) (root, version, digest string, err error) {
	args := []string{"pack", "--ignore-scripts", "--pack-destination", destination}
	if spec.Registry != "" {
		args = append(args, "--registry", spec.Registry)
	}
	packageSpec := spec.Package
	if spec.Version != "" {
		packageSpec += "@" + spec.Version
	}
	args = append(args, "--", packageSpec)
	if _, err := runNPM(ctx, destination, args); err != nil {
		return "", "", "", err
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		return "", "", "", err
	}
	var archives []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".tgz") {
			archives = append(archives, filepath.Join(destination, entry.Name()))
		}
	}
	if len(archives) != 1 {
		return "", "", "", fmt.Errorf("npm pack created %d archives; expected one", len(archives))
	}
	archive := archives[0]
	info, err := os.Stat(archive)
	if err != nil || info.Size() > npmArchiveLimit {
		return "", "", "", fmt.Errorf("npm archive exceeds size limit or cannot be read: %v", err)
	}
	file, err := os.Open(archive)
	if err != nil {
		return "", "", "", err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return "", "", "", err
	}
	defer gz.Close()
	extracted := filepath.Join(destination, "extracted")
	if err := extractNPMArchive(ctx, gz, extracted); err != nil {
		return "", "", "", err
	}
	root = filepath.Join(extracted, "package")
	metadata, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return "", "", "", err
	}
	var pkg struct {
		Name    string
		Version json.RawMessage
	}
	if err := json.Unmarshal(metadata, &pkg); err != nil || expectedName != "" && pkg.Name != expectedName {
		return "", "", "", fmt.Errorf("npm package metadata does not match requested package %q: %v", spec.Package, err)
	}
	if err := ValidateNPMSource(domain.NPMSource{Package: pkg.Name}); err != nil {
		return "", "", "", fmt.Errorf("npm package metadata: %w", err)
	}
	_ = json.Unmarshal(pkg.Version, &version)
	digest, err = util.FileDigest(archive)
	return root, version, digest, err
}

func ListNPMVersions(ctx context.Context, spec domain.NPMSource) ([]string, error) {
	if err := ValidateNPMSource(spec); err != nil {
		return nil, err
	}
	return listNPMVersions(ctx, spec)
}

func ListClaudeNPMVersions(ctx context.Context, spec domain.NPMSource) ([]string, error) {
	spec, err := NormalizeClaudeNPMSource(spec)
	if err != nil {
		return nil, err
	}
	if IsHTTPURL(spec.Package) {
		return nil, fmt.Errorf("version discovery requires an npm registry package; a tarball URL has no version listing")
	}
	return listNPMVersions(ctx, spec)
}

func listNPMVersions(ctx context.Context, spec domain.NPMSource) ([]string, error) {
	args := []string{"view", "--json"}
	if spec.Registry != "" {
		args = append(args, "--registry", spec.Registry)
	}
	args = append(args, "--", spec.Package, "versions")
	output, err := runNPM(ctx, "", args)
	if err != nil {
		return nil, err
	}
	var versions []string
	if err := json.Unmarshal(output, &versions); err != nil {
		return nil, fmt.Errorf("reading npm versions: %w", err)
	}
	return versions, nil
}

func runNPM(ctx context.Context, directory string, args []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	command := "npm"
	if runtime.GOOS == "windows" {
		shim, err := exec.LookPath("npm.cmd")
		if err != nil {
			return nil, err
		}
		// Invoke the standard npm entrypoint directly so registry selectors and
		// URL arguments never pass through Windows batch-file parsing.
		cli := filepath.Join(filepath.Dir(shim), "node_modules", "npm", "bin", "npm-cli.js")
		if info, err := os.Stat(cli); err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("npm JavaScript entrypoint is missing: %s", cli)
		}
		command = filepath.Join(filepath.Dir(shim), "node.exe")
		if _, err := os.Stat(command); err != nil {
			command = "node"
		}
		args = append([]string{cli}, args...)
	}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir, cmd.WaitDelay = directory, time.Second
	restore, err := prepareGitProcess(cmd)
	if err != nil {
		return nil, err
	}
	defer restore()
	// Acquisition never writes installed packs and uses a unique staging tree.
	// Windows native delivery still requires inherited locks and remains gated separately.
	if runtime.GOOS != "windows" {
		if err := util.InheritConfigLock(ctx, cmd); err != nil {
			return nil, err
		}
	}
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("npm: %w", ctx.Err())
	}
	if err != nil {
		if failure, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("npm failed: %w\n%s", err, strings.TrimSpace(string(failure.Stderr)))
		}
		return nil, fmt.Errorf("npm failed: %w", err)
	}
	return output, nil
}

func extractNPMArchive(ctx context.Context, reader io.Reader, destination string) error {
	tarReader := tar.NewReader(reader)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := tarReader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if !filepath.IsLocal(header.Name) || strings.Contains(header.Name, "\\") || slices.Contains(strings.Split(header.Name, "/"), "..") {
			return fmt.Errorf("npm archive path escapes destination: %q", header.Name)
		}
		path := filepath.Join(destination, filepath.FromSlash(header.Name))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			total += header.Size
			if header.Size < 0 || total > npmExtractedLimit {
				return fmt.Errorf("npm archive exceeds extracted size limit")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tarReader)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			if err := os.Chmod(path, os.FileMode(header.Mode)&0o777); err != nil {
				return err
			}
		default:
			return fmt.Errorf("npm archive entry %q has unsupported type %d", header.Name, header.Typeflag)
		}
	}
}
