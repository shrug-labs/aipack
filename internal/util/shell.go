package util

import "strings"

// ShellCommand renders argv as a copy-pasteable POSIX shell command.
func ShellCommand(args ...string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = ShellQuoteArg(arg)
	}
	return strings.Join(quoted, " ")
}

// ShellCommandWithOperand renders a command whose final operand may need "--".
// Args after the operand are treated as flags and are rendered before "--" when
// the operand itself starts with "-".
func ShellCommandWithOperand(argsBeforeOperand []string, operand string, argsAfterOperand ...string) string {
	args := append([]string{}, argsBeforeOperand...)
	if strings.HasPrefix(operand, "-") {
		args = append(args, argsAfterOperand...)
		args = append(args, "--", operand)
		return ShellCommand(args...)
	}
	args = append(args, operand)
	args = append(args, argsAfterOperand...)
	return ShellCommand(args...)
}

// ShellQuoteArg quotes one shell argument when needed.
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
