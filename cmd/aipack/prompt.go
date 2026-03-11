package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

func isTerminalFile(f *os.File) bool {
	if f == nil {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return (st.Mode() & os.ModeCharDevice) != 0
}

func promptLine(in io.Reader, out io.Writer, msg string) (string, error) {
	if _, err := fmt.Fprint(out, msg); err != nil {
		return "", err
	}
	r := bufio.NewReader(in)
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func promptYesNoDefault(in io.Reader, out io.Writer, msg string, def bool) (bool, error) {
	suffix := " [y/N]: "
	if def {
		suffix = " [Y/n]: "
	}
	line, err := promptLine(in, out, msg+suffix)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(line) == "" {
		return def, nil
	}
	ans := strings.ToLower(strings.TrimSpace(line))
	if ans == "y" || ans == "yes" {
		return true, nil
	}
	if ans == "n" || ans == "no" {
		return false, nil
	}
	return def, nil
}
