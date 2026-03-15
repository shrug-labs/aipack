package app

import (
	"os"
	"path/filepath"
	"testing"

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
