package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
)

// CaptureContentDir reads files matching ext from srcDir, creates flat
// CopyActions (Dst = dstDir/{filename}), and calls parse for each file.
// The parse callback receives (raw bytes, name without extension, absolute
// source path) and should parse the content and append to results; returning
// an error adds a parse warning. If srcDir does not exist, returns nil slices.
func CaptureContentDir(
	srcDir, dstDir, ext string,
	parse func(raw []byte, name, srcPath string) error,
) ([]domain.CopyAction, []domain.Warning) {
	var copies []domain.CopyAction
	var warnings []domain.Warning

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		if !os.IsNotExist(err) {
			warnings = append(warnings, domain.Warning{Path: srcDir, Message: fmt.Sprintf("reading directory: %v", err)})
		}
		return copies, warnings
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ext) {
			continue
		}
		src := filepath.Join(srcDir, e.Name())
		dst := filepath.Join(dstDir, e.Name())
		copies = append(copies, domain.CopyAction{Src: src, Dst: dst, Kind: domain.CopyKindFile})

		raw, readErr := os.ReadFile(src)
		if readErr != nil {
			warnings = append(warnings, domain.Warning{Path: src, Message: fmt.Sprintf("reading file: %v", readErr)})
			continue
		}
		name := strings.TrimSuffix(e.Name(), ext)
		if parseErr := parse(raw, name, src); parseErr != nil {
			warnings = append(warnings, domain.Warning{Path: src, Message: fmt.Sprintf("parse error: %v", parseErr)})
		}
	}

	return copies, warnings
}
