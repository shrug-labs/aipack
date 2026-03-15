package util

import (
	"fmt"
	"os"
	"strings"
)

// ParamRefPrefixes lists the recognized prefix strings for parameter references.
// {params.*} is canonical; {param.*} and {global.*} are legacy synonyms.
var ParamRefPrefixes = []string{"{params.", "{param.", "{global."}

// ParamRef represents a parsed parameter reference like {params.key}.
type ParamRef struct {
	Prefix string // one of ParamRefPrefixes
	Name   string // the key name (e.g. "region")
	Start  int    // byte offset of '{'
	End    int    // byte offset after '}'
}

// WalkParamRefs finds all {params.*}, {param.*}, and {global.*} references in s
// and calls fn for each. At each position the longest matching prefix wins,
// so {params.foo} is reported once (prefix "{params.", name "foo") and does
// not also produce a spurious "{param." match with name "s.foo".
// If fn returns a non-nil error, iteration stops.
func WalkParamRefs(s string, fn func(ref ParamRef) error) error {
	offset := 0
	for offset < len(s) {
		// Find the next '{' — all param refs start with one.
		idx := strings.IndexByte(s[offset:], '{')
		if idx < 0 {
			break
		}
		pos := offset + idx
		rest := s[pos:]

		// Try prefixes longest-first to avoid substring ambiguity.
		matched := false
		for _, prefix := range paramRefPrefixesByLength {
			if !strings.HasPrefix(rest, prefix) {
				continue
			}
			endRel := strings.Index(rest, "}")
			if endRel < 0 {
				// No closing brace — no more refs possible.
				return nil
			}
			name := s[pos+len(prefix) : pos+endRel]
			end := pos + endRel + 1
			if err := fn(ParamRef{Prefix: prefix, Name: name, Start: pos, End: end}); err != nil {
				return err
			}
			offset = end
			matched = true
			break
		}
		if !matched {
			offset = pos + 1
		}
	}
	return nil
}

// paramRefPrefixesByLength is ParamRefPrefixes sorted longest-first for
// unambiguous matching. "{params." must be tried before "{param.".
var paramRefPrefixesByLength = func() []string {
	sorted := make([]string, len(ParamRefPrefixes))
	copy(sorted, ParamRefPrefixes)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if len(sorted[j]) > len(sorted[i]) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	return sorted
}()

// EnvRef represents a parsed {env:VAR} reference with its position in the source string.
type EnvRef struct {
	Name  string // variable name
	Start int    // byte offset of '{'
	End   int    // byte offset after '}'
}

// WalkEnvRefs finds all {env:VAR} references in s and calls fn for each.
// Returns an error if a reference is unterminated or has an empty name.
// If fn returns a non-nil error, iteration stops and that error is returned.
func WalkEnvRefs(s string, fn func(ref EnvRef) error) error {
	offset := 0
	for {
		idx := strings.Index(s[offset:], "{env:")
		if idx < 0 {
			return nil
		}
		start := offset + idx
		rest := s[start:]
		endRel := strings.Index(rest, "}")
		if endRel < 0 {
			return fmt.Errorf("unterminated env reference in %q", s)
		}
		end := start + endRel + 1
		name := strings.TrimSpace(s[start+len("{env:") : start+endRel])
		if name == "" {
			return fmt.Errorf("empty env reference in %q", s)
		}
		if err := fn(EnvRef{Name: name, Start: start, End: end}); err != nil {
			return err
		}
		offset = end
	}
}

// ExpandEnvRefs replaces all {env:VAR} references in s with the value of the
// named environment variable. Returns an error if a reference is unterminated,
// empty, or the variable is not set.
func ExpandEnvRefs(s string) (string, error) {
	if !strings.Contains(s, "{env:") {
		return s, nil
	}
	// Collect refs first, then replace right-to-left to preserve offsets.
	var refs []EnvRef
	if err := WalkEnvRefs(s, func(ref EnvRef) error {
		refs = append(refs, ref)
		return nil
	}); err != nil {
		return "", err
	}
	out := s
	for i := len(refs) - 1; i >= 0; i-- {
		ref := refs[i]
		val, ok := os.LookupEnv(ref.Name)
		if !ok {
			return "", fmt.Errorf("env var %s is not set", ref.Name)
		}
		out = out[:ref.Start] + val + out[ref.End:]
	}
	return out, nil
}
