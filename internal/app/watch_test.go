package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/shrug-labs/aipack/internal/domain"
)

func TestIsRelevantEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event fsnotify.Event
		want  bool
	}{
		{
			name:  "write event on normal file",
			event: fsnotify.Event{Name: "/tmp/rules/foo.md", Op: fsnotify.Write},
			want:  true,
		},
		{
			name:  "create event on normal file",
			event: fsnotify.Event{Name: "/tmp/rules/bar.md", Op: fsnotify.Create},
			want:  true,
		},
		{
			name:  "chmod-only event ignored",
			event: fsnotify.Event{Name: "/tmp/rules/foo.md", Op: fsnotify.Chmod},
			want:  false,
		},
		{
			name:  "chmod+write event accepted",
			event: fsnotify.Event{Name: "/tmp/rules/foo.md", Op: fsnotify.Chmod | fsnotify.Write},
			want:  true,
		},
		{
			name:  "hidden file ignored",
			event: fsnotify.Event{Name: "/tmp/rules/.hidden", Op: fsnotify.Write},
			want:  false,
		},
		{
			name:  "dotfile like .gitignore ignored",
			event: fsnotify.Event{Name: "/tmp/.gitignore", Op: fsnotify.Write},
			want:  false,
		},
		{
			name:  "emacs lock file ignored",
			event: fsnotify.Event{Name: "/tmp/rules/#foo.md#", Op: fsnotify.Write},
			want:  false,
		},
		{
			name:  "tilde backup file ignored",
			event: fsnotify.Event{Name: "/tmp/rules/foo.md~", Op: fsnotify.Write},
			want:  false,
		},
		{
			name:  "vim swap file (.swp) ignored via dot prefix",
			event: fsnotify.Event{Name: "/tmp/rules/.foo.swp", Op: fsnotify.Write},
			want:  false,
		},
		{
			name:  "remove event on normal file",
			event: fsnotify.Event{Name: "/tmp/rules/foo.md", Op: fsnotify.Remove},
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isRelevantEvent(tt.event)
			if got != tt.want {
				t.Errorf("isRelevantEvent(%v) = %v, want %v", tt.event, got, tt.want)
			}
		})
	}
}

func TestPackSourceDirs(t *testing.T) {
	t.Parallel()

	t.Run("empty profile returns nil", func(t *testing.T) {
		t.Parallel()
		dirs := PackSourceDirs(domain.Profile{})
		if len(dirs) != 0 {
			t.Errorf("expected 0 dirs, got %d", len(dirs))
		}
	})

	t.Run("deduplicates same root", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		profile := domain.Profile{
			Packs: []domain.Pack{
				{Name: "a", Root: root},
				{Name: "b", Root: root},
			},
		}
		dirs := PackSourceDirs(profile)
		if len(dirs) != 1 {
			t.Errorf("expected 1 unique dir, got %d: %v", len(dirs), dirs)
		}
	})

	t.Run("skips packs with empty root", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		profile := domain.Profile{
			Packs: []domain.Pack{
				{Name: "a", Root: root},
				{Name: "b", Root: ""},
			},
		}
		dirs := PackSourceDirs(profile)
		if len(dirs) != 1 {
			t.Errorf("expected 1 dir, got %d: %v", len(dirs), dirs)
		}
	})

	t.Run("returns distinct roots", func(t *testing.T) {
		t.Parallel()
		rootA := t.TempDir()
		rootB := t.TempDir()
		profile := domain.Profile{
			Packs: []domain.Pack{
				{Name: "a", Root: rootA},
				{Name: "b", Root: rootB},
			},
		}
		dirs := PackSourceDirs(profile)
		if len(dirs) != 2 {
			t.Errorf("expected 2 dirs, got %d: %v", len(dirs), dirs)
		}
	})
}

func TestUpdateWatchDirs(t *testing.T) {
	t.Parallel()

	t.Run("adds directories recursively", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		sub := filepath.Join(root, "sub")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}

		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			t.Fatal(err)
		}
		defer watcher.Close()

		if err := updateWatchDirs(watcher, []string{root}, os.Stderr); err != nil {
			t.Fatal(err)
		}

		watchList := watcher.WatchList()
		found := map[string]bool{}
		for _, w := range watchList {
			found[w] = true
		}
		if !found[root] {
			t.Errorf("expected root %s in watch list", root)
		}
		if !found[sub] {
			t.Errorf("expected sub %s in watch list", sub)
		}
	})

	t.Run("removes stale directories", func(t *testing.T) {
		t.Parallel()
		dirA := t.TempDir()
		dirB := t.TempDir()

		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			t.Fatal(err)
		}
		defer watcher.Close()

		// Start watching both.
		if err := updateWatchDirs(watcher, []string{dirA, dirB}, os.Stderr); err != nil {
			t.Fatal(err)
		}
		if len(watcher.WatchList()) < 2 {
			t.Fatalf("expected at least 2 watches, got %d", len(watcher.WatchList()))
		}

		// Update to only watch dirA — dirB should be removed.
		if err := updateWatchDirs(watcher, []string{dirA}, os.Stderr); err != nil {
			t.Fatal(err)
		}

		watchList := watcher.WatchList()
		for _, w := range watchList {
			if w == dirB {
				t.Errorf("dirB %s should have been removed from watch list", dirB)
			}
		}
	})
}

func TestRunWatch_StopsImmediatelyWhenIdle(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stderr strings.Builder
	calls := 0
	err := RunWatch(ctx, func() ([]string, error) {
		calls++
		cancel()
		return nil, nil
	}, nil, &stderr)
	if err != nil {
		t.Fatalf("RunWatch() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("syncFn calls = %d, want 1", calls)
	}
	if got := stderr.String(); !strings.Contains(got, "watch: stopped") {
		t.Fatalf("stderr missing stop message: %q", got)
	}
}

func TestRunWatch_StopsAfterInitialSyncCancellationBeforeWalkingDirs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for i := range 200 {
		dir := filepath.Join(root, "dir", strings.Repeat("x", i%8), strconv.Itoa(i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stderr strings.Builder
	err := RunWatch(ctx, func() ([]string, error) {
		cancel()
		return []string{root}, nil
	}, nil, &stderr)
	if err != nil {
		t.Fatalf("RunWatch() error = %v", err)
	}
	if got := stderr.String(); strings.Contains(got, "watch: watching") {
		t.Fatalf("expected stop before watcher setup, got %q", got)
	}
	if got := stderr.String(); !strings.Contains(got, "watch: stopped") {
		t.Fatalf("stderr missing stop message: %q", got)
	}
}

func TestRunWatch_DrainsSyncInProgressBeforeStopping(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	file := filepath.Join(root, "rule.md")
	if err := os.WriteFile(file, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stderr strings.Builder
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	done := make(chan error, 1)

	var mu sync.Mutex
	calls := 0
	go func() {
		done <- RunWatch(ctx, func() ([]string, error) {
			mu.Lock()
			calls++
			callNum := calls
			mu.Unlock()
			if callNum == 1 {
				return []string{root}, nil
			}
			started <- struct{}{}
			<-release
			return []string{root}, nil
		}, nil, &stderr)
	}()

	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(file, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for re-sync to start")
	}

	cancel()

	select {
	case err := <-done:
		t.Fatalf("RunWatch returned before sync drained: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	close(release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunWatch() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for RunWatch to stop")
	}

	mu.Lock()
	gotCalls := calls
	mu.Unlock()
	if gotCalls != 2 {
		t.Fatalf("syncFn calls = %d, want 2", gotCalls)
	}
	if got := stderr.String(); !strings.Contains(got, "watch: stopped") {
		t.Fatalf("stderr missing stop message: %q", got)
	}
}

func TestRunWatch_DoesNotStartNewSyncAfterShutdown(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	file := filepath.Join(root, "rule.md")
	if err := os.WriteFile(file, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stderr strings.Builder
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	done := make(chan error, 1)

	var mu sync.Mutex
	calls := 0
	go func() {
		done <- RunWatch(ctx, func() ([]string, error) {
			mu.Lock()
			calls++
			callNum := calls
			mu.Unlock()
			if callNum == 1 {
				return []string{root}, nil
			}
			started <- struct{}{}
			<-release
			return []string{root}, nil
		}, nil, &stderr)
	}()

	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(file, []byte("after-1"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for re-sync to start")
	}

	cancel()
	if err := os.WriteFile(file, []byte("after-2"), 0o644); err != nil {
		t.Fatal(err)
	}
	close(release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunWatch() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for RunWatch to stop")
	}

	time.Sleep(700 * time.Millisecond)

	mu.Lock()
	gotCalls := calls
	mu.Unlock()
	if gotCalls != 2 {
		t.Fatalf("syncFn calls = %d, want 2", gotCalls)
	}
}

func TestRunWatch_InitialSyncFailureStillStopsOnCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stderr strings.Builder
	first := true
	err := RunWatch(ctx, func() ([]string, error) {
		if first {
			first = false
			cancel()
			return nil, errors.New("boom")
		}
		return nil, nil
	}, nil, &stderr)
	if err != nil {
		t.Fatalf("RunWatch() error = %v", err)
	}
	got := stderr.String()
	if !strings.Contains(got, "watch: initial sync failed: boom") {
		t.Fatalf("stderr missing initial sync failure: %q", got)
	}
	if !strings.Contains(got, "watch: stopped") {
		t.Fatalf("stderr missing stop message: %q", got)
	}
}
