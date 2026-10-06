//go:build !windows

package util

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestConfigCommandCancellation(t *testing.T) {
	root := os.Getenv("AIPACK_LOCK_TEST_ROOT")
	role := os.Getenv("AIPACK_LOCK_TEST_ROLE")
	if role != "" {
		if role == "parent" {
			child := exec.Command(os.Args[0], "-test.run=^TestConfigCommandCancellation$")
			child.Env = append(os.Environ(), "AIPACK_LOCK_TEST_ROLE=child")
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			time.Sleep(20 * time.Second)
			return
		}
		if err := os.WriteFile(filepath.Join(root, "started"), []byte("started"), 0o600); err != nil {
			t.Fatal(err)
		}
		for !PathExists(filepath.Join(root, "release")) {
			time.Sleep(10 * time.Millisecond)
		}
		if err := os.WriteFile(filepath.Join(root, "late-write"), []byte("unsafe"), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	root = t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx, unlock, err := LockConfigContext(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestConfigCommandCancellation$")
	cmd.Env = append(os.Environ(), "AIPACK_LOCK_TEST_ROOT="+root, "AIPACK_LOCK_TEST_ROLE=parent")
	if err := InheritConfigLock(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !PathExists(filepath.Join(root, "started")) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !PathExists(filepath.Join(root, "started")) {
		cancel()
		_ = cmd.Wait()
		t.Fatal("nested command did not start")
	}
	cancel()
	if err := cmd.Wait(); err == nil {
		t.Fatal("canceled native command succeeded")
	}
	if err := os.WriteFile(filepath.Join(root, "release"), []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if PathExists(filepath.Join(root, "late-write")) {
		t.Fatal("native command child wrote after cancellation")
	}
}

func TestLockConfigReleaseWithInheritedDescriptor(t *testing.T) {
	dir := t.TempDir()
	unlock, err := LockConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if unexpected, err := LockConfig(dir); err == nil {
		unexpected()
		t.Fatal("concurrent mutation acquired the lock")
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	// Simulate a fork retaining a copy of the parent's locked descriptor.
	file, err := os.OpenFile(filepath.Join(dir, ".mutation.lock"), os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		t.Fatal(err)
	}
	retained, err := unix.Dup(int(file.Fd()))
	if err != nil {
		unlockConfig(file)
		t.Fatal(err)
	}
	defer unix.Close(retained)
	if err := unlockConfig(file); err != nil {
		t.Fatal(err)
	}
	next, err := LockConfig(dir)
	if err != nil {
		t.Fatalf("inherited descriptor retained released lock: %v", err)
	}
	next()
}
