//go:build unix

package mcp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// init registers the spawn_grandchild server so runTestServer can dispatch
// to it. On non-unix platforms this file is absent and the variable stays
// nil, so the spawn_grandchild case exits cleanly without hanging.
func init() {
	spawnGrandchildServer = runSpawnGrandchildServer
}

// runSpawnGrandchildServer acts as an MCP server that forks a long-running
// `sleep` and exits its own readReq loop waiting. The aipack client is
// expected to kill the whole process group on cleanup, reaping the sleep
// grandchild.
func runSpawnGrandchildServer() {
	r := newNewlineReader(os.Stdin)
	w := newlineWriter{os.Stdout}

	req := r.readReq()
	if req.Method != "initialize" {
		os.Exit(2)
	}
	w.writeResp(*req.ID, map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "gc-spawner", "version": "1.0"},
	})
	r.readReq() // initialized notification

	// Spawn `sleep 60` without detaching from the process group. On Unix
	// the child inherits the parent's pgid, so a group-kill from the
	// aipack client reaches it.
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		os.Exit(4)
	}
	fmt.Fprintln(os.Stderr, cmd.Process.Pid)

	// Hang so the probing client has to trigger cleanup.
	select {}
}

// TestProbeStdio_GrandchildReapedOnCleanup pins the process-group cleanup
// contract: a grandchild spawned inside the MCP server's pgid is killed
// when the probe is torn down. The sleep grandchild's PID is emitted on
// stderr; after cleanup the test polls for reaping.
func TestProbeStdio_GrandchildReapedOnCleanup(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	exe, _ := os.Executable()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^$")
	cmd.Env = append(os.Environ(), "MCP_TEST_SERVER=spawn_grandchild")
	setProcessGroup(cmd) // mirror the real ProbeStdio path

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go io.Copy(io.Discard, stdout)

	// Drive the server just far enough for it to spawn the grandchild and
	// print the PID. Both messages are framed with Content-Length because
	// the probe server supports either framing.
	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"t"}}}`
	if _, err := fmt.Fprintf(stdin, "Content-Length: %d\r\n\r\n%s\n", len(initBody), initBody); err != nil {
		cmd.Process.Kill()
		t.Fatalf("send initialize: %v", err)
	}
	initedBody := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	if _, err := fmt.Fprintf(stdin, "Content-Length: %d\r\n\r\n%s\n", len(initedBody), initedBody); err != nil {
		cmd.Process.Kill()
		t.Fatalf("send initialized: %v", err)
	}

	pidReader := bufio.NewReader(stderr)
	pidLine, err := pidReader.ReadString('\n')
	if err != nil {
		cmd.Process.Kill()
		t.Fatalf("read grandchild pid: %v", err)
	}
	gcPID, err := strconv.Atoi(strings.TrimSpace(pidLine))
	if err != nil {
		cmd.Process.Kill()
		t.Fatalf("parse grandchild pid %q: %v", pidLine, err)
	}

	// Trigger cleanup the same way ProbeStdio does on timeout.
	_ = stdin.Close()
	time.Sleep(200 * time.Millisecond)
	killProcessGroup(cmd)
	_ = cmd.Wait()

	// Poll for reaping; the OS is quick but not instantaneous.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !unixPIDExists(gcPID) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Clean up before failing so the test doesn't leak a sleep process.
	_ = syscall.Kill(gcPID, syscall.SIGKILL)
	t.Fatalf("grandchild pid %d survived cleanup — process group kill did not reach it", gcPID)
}

// unixPIDExists reports whether a process with the given PID is still
// reachable via signal 0. On Unix, sending signal 0 is a no-op that only
// succeeds on live processes we can address.
func unixPIDExists(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
