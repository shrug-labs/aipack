package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/source"
	"github.com/shrug-labs/aipack/internal/util"

	"gopkg.in/yaml.v3"
)

const maxImportedMarkdownBytes = 10 << 20

// PackImportRequest describes a single-file markdown import.
type PackImportRequest struct {
	Source     string
	Category   domain.PackCategory
	Name       string
	TargetPack string
	ID         string
	ConfigDir  string
	Add        bool
	Profile    string
	NowFn      func() time.Time
	Client     *http.Client
}

// PackImport imports one markdown file into a new local pack or an existing
// installed pack.
func PackImport(ctx context.Context, req PackImportRequest, stdout io.Writer) error {
	if strings.TrimSpace(req.Source) == "" {
		return fmt.Errorf("source is required")
	}
	if req.ConfigDir == "" {
		return fmt.Errorf("config dir is required")
	}
	name := strings.TrimSpace(req.Name)
	targetPack := strings.TrimSpace(req.TargetPack)
	if name == "" && targetPack == "" {
		return fmt.Errorf("pack target is required: use --name for a new pack or --pack for an existing pack")
	}
	if name != "" && targetPack != "" {
		return fmt.Errorf("use either --name for a new pack or --pack for an existing pack, not both")
	}
	packName := name
	if targetPack != "" {
		packName = targetPack
	}
	if err := validatePackName(packName); err != nil {
		return err
	}
	if !isImportCategory(req.Category) {
		return fmt.Errorf("unsupported import type %q (valid: skill, rule, prompt)", req.Category)
	}

	if req.Add {
		if _, err := config.EnsureInit(req.ConfigDir); err != nil {
			return fmt.Errorf("ensuring config: %w", err)
		}
		profile := packProfileName(req.Profile)
		profilePath := filepath.Join(req.ConfigDir, "profiles", profile+".yaml")
		if _, err := os.Stat(profilePath); os.IsNotExist(err) {
			return fmt.Errorf("profile %q does not exist at %s", profile, profilePath)
		}
	}

	raw, displayName, err := readImportSource(ctx, req)
	if err != nil {
		return err
	}

	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = inferImportID(displayName)
	}
	if err := validateImportID(id); err != nil {
		return err
	}

	if targetPack != "" {
		return packImportIntoExisting(req, targetPack, id, displayName, raw, stdout)
	}

	packsDir := PacksDir(req.ConfigDir)
	destDir := filepath.Join(packsDir, name)
	if _, err := os.Lstat(destDir); err == nil {
		return fmt.Errorf("pack %q already exists", name)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking pack %q: %w", name, err)
	}

	staging, err := makePackTempDir(req.ConfigDir, "import-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(staging)

	manifest := config.PackManifest{
		SchemaVersion: config.PackSchemaVersion,
		Name:          name,
		Version:       "0.1.0",
		Root:          ".",
	}
	setImportManifestID(&manifest, req.Category, id)

	content, err := ensureImportFrontmatter(req.Category, id, displayName, raw)
	if err != nil {
		return err
	}
	relPath := req.Category.PrimaryRelPath(id)
	contentPath := filepath.Join(staging, relPath)
	if err := os.MkdirAll(filepath.Dir(contentPath), 0o755); err != nil {
		return fmt.Errorf("creating content dir: %w", err)
	}
	if err := os.WriteFile(contentPath, content, 0o644); err != nil {
		return fmt.Errorf("writing imported content: %w", err)
	}
	if err := config.SavePackManifest(filepath.Join(staging, "pack.json"), manifest); err != nil {
		return fmt.Errorf("writing pack manifest: %w", err)
	}

	if err := os.MkdirAll(packsDir, 0o700); err != nil {
		return fmt.Errorf("creating packs directory: %w", err)
	}
	if err := util.ReplaceDirAtomic(destDir, staging); err != nil {
		return fmt.Errorf("installing imported pack: %w", err)
	}

	now := time.Now()
	if req.NowFn != nil {
		now = req.NowFn()
	}
	meta := config.InstalledPackMeta{
		Origin:      destDir,
		Method:      config.MethodLocal,
		InstalledAt: now.UTC().Format(time.RFC3339),
	}
	meta.Resolved = buildResolvedInventory(req.ConfigDir, name, destDir, "", now, stdout)
	if err := packRecordOrigin(req.ConfigDir, name, meta); err != nil {
		return fmt.Errorf("recording pack origin: %w", err)
	}

	if req.Add {
		if err := PackAdd(req.ConfigDir, packProfileName(req.Profile), name, nil, stdout); err != nil {
			return fmt.Errorf("adding pack to profile: %w", err)
		}
	}

	_ = indexInstalledPack(req.ConfigDir, name, destDir)
	if stdout != nil {
		fmt.Fprintf(stdout, "Imported %s %q into pack %q\n", req.Category.SingularLabel(), id, name)
	}
	return nil
}

func packImportIntoExisting(req PackImportRequest, packName, id, displayName string, raw []byte, stdout io.Writer) error {
	destDir := filepath.Join(PacksDir(req.ConfigDir), packName)
	st, err := os.Lstat(destDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("pack %q is not installed at %s", packName, destDir)
		}
		return fmt.Errorf("checking pack %q: %w", packName, err)
	}
	if !st.IsDir() && st.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("pack %q is not a directory: %s", packName, destDir)
	}

	manifestPath := filepath.Join(destDir, "pack.json")
	manifest, err := config.LoadPackManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("loading pack manifest: %w", err)
	}

	content, err := ensureImportFrontmatter(req.Category, id, displayName, raw)
	if err != nil {
		return err
	}
	relPath := req.Category.PrimaryRelPath(id)
	contentPath := filepath.Join(destDir, relPath)
	if _, err := os.Lstat(contentPath); err == nil {
		return fmt.Errorf("%s %q already exists in pack %q", req.Category.SingularLabel(), id, packName)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking imported content: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(contentPath), 0o755); err != nil {
		return fmt.Errorf("creating content dir: %w", err)
	}
	if err := util.WriteFileAtomic(contentPath, content); err != nil {
		return fmt.Errorf("writing imported content: %w", err)
	}

	if appendImportManifestIDIfExplicit(&manifest, req.Category, id) {
		if err := config.SavePackManifest(manifestPath, manifest); err != nil {
			_ = os.Remove(contentPath)
			return fmt.Errorf("writing pack manifest: %w", err)
		}
	}

	refreshImportedPackInventory(req, packName, destDir, stdout)
	if req.Add {
		if err := PackAdd(req.ConfigDir, packProfileName(req.Profile), packName, nil, stdout); err != nil {
			return fmt.Errorf("adding pack to profile: %w", err)
		}
	}
	_ = indexInstalledPack(req.ConfigDir, packName, destDir)
	if stdout != nil {
		fmt.Fprintf(stdout, "Imported %s %q into pack %q\n", req.Category.SingularLabel(), id, packName)
	}
	return nil
}

func refreshImportedPackInventory(req PackImportRequest, packName, destDir string, stdout io.Writer) {
	lf, err := config.EnsureLockfileMigrated(req.ConfigDir)
	if err != nil {
		if stdout != nil {
			fmt.Fprintf(stdout, "Warning: failed to refresh lockfile inventory for %s: %v\n", packName, err)
		}
		return
	}
	meta, ok := lf.Packs[packName]
	if !ok {
		return
	}
	now := time.Now()
	if req.NowFn != nil {
		now = req.NowFn()
	}
	meta.Resolved = buildResolvedInventory(req.ConfigDir, packName, destDir, meta.Ref, now, stdout)
	if err := packRecordOrigin(req.ConfigDir, packName, meta); err != nil && stdout != nil {
		fmt.Fprintf(stdout, "Warning: failed to refresh lockfile inventory for %s: %v\n", packName, err)
	}
}

func isImportCategory(cat domain.PackCategory) bool {
	return cat == domain.CategorySkills || cat == domain.CategoryRules || cat == domain.CategoryPrompts
}

func validateImportID(id string) error {
	if id == "" {
		return fmt.Errorf("content id is required")
	}
	if err := validatePackName(id); err != nil {
		return fmt.Errorf("invalid content id %q: must not contain path separators or traversal sequences", id)
	}
	return nil
}

func inferImportID(displayName string) string {
	base := filepath.Base(displayName)
	if u, err := url.Parse(displayName); err == nil && u.Path != "" {
		base = path.Base(u.Path)
	}
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.TrimSpace(base)
	if base == "" || base == "." || base == "/" {
		return "imported"
	}
	return base
}

func readImportSource(ctx context.Context, req PackImportRequest) ([]byte, string, error) {
	if source.IsHTTPURL(req.Source) {
		client := req.Client
		if client == nil {
			client = http.DefaultClient
		}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.Source, nil)
		if err != nil {
			return nil, "", fmt.Errorf("creating request: %w", err)
		}
		resp, err := client.Do(httpReq)
		if err != nil {
			return nil, "", fmt.Errorf("fetching %s: %w", req.Source, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return nil, "", fmt.Errorf("fetching %s: HTTP %d", req.Source, resp.StatusCode)
		}
		raw, err := readLimitedImport(resp.Body)
		if err != nil {
			return nil, "", fmt.Errorf("reading %s: %w", req.Source, err)
		}
		return raw, req.Source, nil
	}

	path, err := filepath.Abs(req.Source)
	if err != nil {
		return nil, "", fmt.Errorf("resolving source path: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, "", fmt.Errorf("reading source: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, "", fmt.Errorf("reading source: %w", err)
	}
	if st.IsDir() {
		return nil, "", fmt.Errorf("source is a directory, expected markdown file: %s", path)
	}
	raw, err := readLimitedImport(f)
	if err != nil {
		return nil, "", fmt.Errorf("reading source: %w", err)
	}
	return raw, path, nil
}

func readLimitedImport(r io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	_, err := io.Copy(&buf, io.LimitReader(r, maxImportedMarkdownBytes+1))
	if err != nil {
		return nil, err
	}
	if buf.Len() > maxImportedMarkdownBytes {
		return nil, fmt.Errorf("markdown file exceeds %d bytes", maxImportedMarkdownBytes)
	}
	return buf.Bytes(), nil
}

type importFrontmatter struct {
	Name        string `yaml:"name,omitempty"`
	Description string `yaml:"description,omitempty"`
}

func ensureImportFrontmatter(cat domain.PackCategory, id, displayName string, raw []byte) ([]byte, error) {
	if domain.HasFrontmatterPrefix(raw) {
		return raw, nil
	}
	description := "Imported from " + filepath.Base(displayName)
	if u, err := url.Parse(displayName); err == nil && u.Path != "" {
		description = "Imported from " + path.Base(u.Path)
	}
	fm := importFrontmatter{Description: description}
	if cat != domain.CategoryPrompts {
		fm.Name = id
	}
	fmBytes, err := yaml.Marshal(fm)
	if err != nil {
		return nil, fmt.Errorf("marshalling generated frontmatter: %w", err)
	}
	content := make([]byte, 0, len(fmBytes)+len(raw)+10)
	content = append(content, "---\n"...)
	content = append(content, fmBytes...)
	content = append(content, "---\n\n"...)
	content = append(content, raw...)
	return content, nil
}

func setImportManifestID(manifest *config.PackManifest, cat domain.PackCategory, id string) {
	switch cat {
	case domain.CategoryRules:
		manifest.Rules = []string{id}
	case domain.CategorySkills:
		manifest.Skills = []string{id}
	case domain.CategoryPrompts:
		manifest.Prompts = []string{id}
	}
}

func appendImportManifestIDIfExplicit(manifest *config.PackManifest, cat domain.PackCategory, id string) bool {
	ids := manifest.ContentIDsPtr(cat)
	if ids == nil || len(*ids) == 0 || slices.Contains(*ids, id) {
		return false
	}
	*ids = append(*ids, id)
	return true
}
