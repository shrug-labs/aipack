package util

import (
	"fmt"
	"os"
	"strings"
)

// ExpandEnvRefs replaces all {env:VAR} references in s with the value of the
// named environment variable. Returns an error if a reference is unterminated,
// empty, or the variable is not set.
func ExpandEnvRefs(s string) (string, error) {
	if !strings.Contains(s, "{env:") {
		return s, nil
	}
	out := s
	for {
		start := strings.Index(out, "{env:")
		if start < 0 {
			return out, nil
		}
		rest := out[start:]
		endRel := strings.Index(rest, "}")
		if endRel < 0 {
			return "", fmt.Errorf("unterminated env reference in %q", s)
		}
		end := start + endRel
		name := strings.TrimSpace(out[start+len("{env:") : end])
		if name == "" {
			return "", fmt.Errorf("empty env reference in %q", s)
		}
		val := os.Getenv(name)
		if val == "" {
			return "", fmt.Errorf("env var %s is not set", name)
		}
		out = out[:start] + val + out[end+1:]
	}
}
