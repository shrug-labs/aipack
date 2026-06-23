package cmdutil

import "github.com/shrug-labs/aipack/internal/util"

func ShellCommand(args ...string) string {
	return util.ShellCommand(args...)
}

func ShellCommandWithOperand(argsBeforeOperand []string, operand string, argsAfterOperand ...string) string {
	return util.ShellCommandWithOperand(argsBeforeOperand, operand, argsAfterOperand...)
}

func ShellQuoteArg(arg string) string {
	return util.ShellQuoteArg(arg)
}
