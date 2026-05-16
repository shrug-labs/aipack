package common

import "strings"

// AppendSingleLinePaste appends bracketed-paste content to a single-line input.
func AppendSingleLinePaste(value, paste string) string {
	paste = strings.ReplaceAll(paste, "\r\n", "\n")
	paste = strings.ReplaceAll(paste, "\r", "\n")
	paste = strings.TrimRight(paste, "\n")
	paste = strings.ReplaceAll(paste, "\n", " ")
	return value + paste
}
