//go:build windows

package source

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"time"
)

func prepareGitProcess(cmd *exec.Cmd) (func(), error) {
	cmd.Cancel = func() error {
		// taskkill is part of Windows; /T includes credential-helper children.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	return func() {}, nil
}

func gitProcessInterrupted(state *os.ProcessState) bool {
	return state != nil && uint32(state.ExitCode()) == 0xc000013a // STATUS_CONTROL_C_EXIT
}
