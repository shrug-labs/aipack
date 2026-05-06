package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// LoadDotEnv loads KEY=value pairs from path. Missing files are treated as an
// empty env map so callers can always use the result as an optional override.
func LoadDotEnv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("reading .env: %w", err)
	}
	defer f.Close()

	values := map[string]string{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if after, ok := strings.CutPrefix(line, "export "); ok {
			line = strings.TrimSpace(after)
		}
		key, val, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !validDotEnvKey(key) {
			return nil, fmt.Errorf("invalid .env entry at line %d", lineNo)
		}
		values[key] = strings.TrimSpace(val)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading .env: %w", err)
	}
	return values, nil
}

// IsValidDotEnvKey reports whether key is a valid identifier for a .env entry.
// The rule matches LoadDotEnv's parser: starts with letter/underscore, body
// can include letters, digits, and underscores. Exported so the app layer can
// validate user input before invoking SetDotEnv.
func IsValidDotEnvKey(key string) bool {
	return validDotEnvKey(key)
}

func validDotEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if r == '_' || unicode.IsLetter(r) || (i > 0 && unicode.IsDigit(r)) {
			continue
		}
		return false
	}
	return true
}

// SetDotEnv writes (or replaces) a KEY=value entry in the .env file at path.
// Existing comments, blank lines, and unrelated entries are preserved; the
// entry is updated in place when present and appended at end-of-file when
// absent. Existing `export KEY=...` prefixes survive a value rewrite, and the
// file is rewritten atomically so a failed write doesn't truncate the file.
func SetDotEnv(path, key, value string) error {
	if !validDotEnvKey(key) {
		return fmt.Errorf("invalid .env key %q", key)
	}
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("invalid .env value: must not contain newlines")
	}
	lines, err := readDotEnvLines(path)
	if err != nil {
		return err
	}
	updated := false
	for i, raw := range lines {
		k, prefix, ok := dotEnvLineKey(raw)
		if !ok || k != key {
			continue
		}
		lines[i] = prefix + key + "=" + value
		updated = true
		break
	}
	if !updated {
		lines = append(lines, key+"="+value)
	}
	return writeDotEnvLines(path, lines)
}

// UnsetDotEnv removes a KEY entry from the .env file at path. Missing entries
// are not an error — the call is a no-op. Comments and unrelated entries are
// preserved.
func UnsetDotEnv(path, key string) error {
	if !validDotEnvKey(key) {
		return fmt.Errorf("invalid .env key %q", key)
	}
	lines, err := readDotEnvLines(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	out := lines[:0]
	removed := false
	for _, raw := range lines {
		k, _, ok := dotEnvLineKey(raw)
		if ok && k == key && !removed {
			removed = true
			continue
		}
		out = append(out, raw)
	}
	if !removed {
		return nil
	}
	return writeDotEnvLines(path, out)
}

// readDotEnvLines reads the file as a slice of lines without a trailing empty
// element. Missing files become an empty slice so SetDotEnv can append-create.
func readDotEnvLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading .env: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	text := string(data)
	// Drop one trailing newline so the round-trip doesn't grow the file.
	text = strings.TrimSuffix(text, "\n")
	return strings.Split(text, "\n"), nil
}

func writeDotEnvLines(path string, lines []string) error {
	body := strings.Join(lines, "\n")
	if body != "" {
		body += "\n"
	}
	return atomicWriteFile(path, []byte(body), 0o600)
}

// atomicWriteFile writes data to path via a sibling temp file + rename.
// Defined locally to avoid a dependency from internal/config on internal/util.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".env.*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp .env: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("writing temp .env: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("chmod temp .env: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("closing temp .env: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("renaming temp .env: %w", err)
	}
	return nil
}

// dotEnvLineKey returns the key declared on a raw .env line plus any leading
// "export " prefix (with trailing space) so a rewrite preserves it. Returns
// ok=false for blank lines, comments, and malformed entries.
func dotEnvLineKey(raw string) (key, prefix string, ok bool) {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	prefix = ""
	if after, hadExport := strings.CutPrefix(line, "export "); hadExport {
		prefix = "export "
		line = strings.TrimSpace(after)
	}
	k, _, hasEq := strings.Cut(line, "=")
	k = strings.TrimSpace(k)
	if !hasEq || !validDotEnvKey(k) {
		return "", "", false
	}
	return k, prefix, true
}
