package main

import (
	"io"
	"os"

	"github.com/charmbracelet/x/term"
)

func isTerminalFile(f *os.File) bool {
	if f == nil {
		return false
	}
	return term.IsTerminal(f.Fd())
}

func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && isTerminalFile(f)
}
