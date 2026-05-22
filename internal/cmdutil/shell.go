package cmdutil

import "strings"

func ShellCommand(args ...string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = ShellQuoteArg(arg)
	}
	return strings.Join(quoted, " ")
}

func ShellQuoteArg(arg string) string {
	if arg == "" {
		return "''"
	}
	safe := true
	for _, r := range arg {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("_@%+=:,./-", r):
		default:
			safe = false
		}
		if !safe {
			break
		}
	}
	if safe {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
}
