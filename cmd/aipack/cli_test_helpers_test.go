package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
)

func runApp(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return runAppWithInput(t, "", false, args...)
}

func runAppWithInput(t *testing.T, input string, stdinTTY bool, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return runAppWithOpts(t, nil, input, stdinTTY, args...)
}

func runAppWithOpts(t *testing.T, opts []kong.Option, input string, stdinTTY bool, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	stdin := strings.NewReader(input)
	code = run(args, stdin, &outBuf, &errBuf, stdinTTY, opts...)
	return outBuf.String(), errBuf.String(), code
}
