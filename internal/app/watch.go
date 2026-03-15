package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/util"
)

// WatchSyncFunc performs a sync and returns the current set of pack source
// directories to watch. The watch loop uses these to update the watcher
// after each sync (in case packs were added or removed).
type WatchSyncFunc func() (watchDirs []string, err error)

// RunWatch watches pack source directories and re-syncs on changes.
// It performs an initial sync, then enters a debounced watch loop.
// Cancel the context to stop watching.
func RunWatch(ctx context.Context, syncFn WatchSyncFunc, configFiles []string, stderr io.Writer) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("creating watcher: %w", err)
	}
	defer watcher.Close()

	// Initial sync.
	fmt.Fprintln(stderr, "watch: initial sync")
	watchDirs, err := syncFn()
	if err != nil {
		fmt.Fprintf(stderr, "watch: initial sync failed: %v\n", err)
		// Continue watching — the user may fix the error.
	}

	// Watch config files (sync-config, profile YAML).
	for _, f := range configFiles {
		if err := watcher.Add(f); err != nil {
			fmt.Fprintf(stderr, "watch: cannot watch %s: %v\n", f, err)
		}
	}

	// Watch pack source directories recursively.
	if err := updateWatchDirs(watcher, watchDirs, stderr); err != nil {
		return err
	}

	fmt.Fprintf(stderr, "watch: watching %d directories, press Ctrl+C to stop\n", len(watcher.WatchList()))

	const debounce = 500 * time.Millisecond
	var timer *time.Timer
	var timerC <-chan time.Time // nil channel, never fires

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(stderr, "watch: stopped")
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if !isRelevantEvent(event) {
				continue
			}
			// If a new directory was created, add it to the watcher.
			if event.Has(fsnotify.Create) {
				if info, serr := os.Stat(event.Name); serr == nil && info.IsDir() {
					addDirRecursive(watcher, event.Name, stderr)
				}
			}
			// Debounce: reset timer on each event.
			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(debounce)
			timerC = timer.C

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(stderr, "watch: error: %v\n", err)

		case <-timerC:
			timerC = nil
			fmt.Fprintln(stderr, "\nwatch: changes detected, re-syncing...")
			newDirs, serr := syncFn()
			if serr != nil {
				fmt.Fprintf(stderr, "watch: sync failed: %v\n", serr)
				continue
			}
			if err := updateWatchDirs(watcher, newDirs, stderr); err != nil {
				fmt.Fprintf(stderr, "watch: updating watchers: %v\n", err)
			}
			fmt.Fprintf(stderr, "watch: watching %d directories\n", len(watcher.WatchList()))
		}
	}
}

// isRelevantEvent filters out events we don't care about.
func isRelevantEvent(event fsnotify.Event) bool {
	if event.Has(fsnotify.Chmod) && !event.Has(fsnotify.Write) {
		return false
	}
	base := filepath.Base(event.Name)
	// Ignore editor temp files and hidden files.
	if base == "" || base[0] == '.' || base[0] == '#' || base[len(base)-1] == '~' {
		return false
	}
	return true
}

// updateWatchDirs replaces the current set of watched pack directories.
func updateWatchDirs(watcher *fsnotify.Watcher, dirs []string, stderr io.Writer) error {
	// Build set of desired watch paths.
	desired := map[string]struct{}{}
	for _, dir := range dirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		desired[abs] = struct{}{}
		// Walk to add subdirectories.
		_ = filepath.WalkDir(abs, func(p string, d os.DirEntry, werr error) error {
			if werr != nil {
				return werr
			}
			if d.IsDir() {
				if util.IgnoredName(d.Name()) {
					return filepath.SkipDir
				}
				desired[p] = struct{}{}
			}
			return nil
		})
	}

	// Remove watches no longer needed (but keep config file watches).
	for _, w := range watcher.WatchList() {
		if _, ok := desired[w]; !ok {
			// Only remove directory watches we manage — config files
			// are managed separately and should not be removed.
			if info, err := os.Stat(w); err == nil && info.IsDir() {
				_ = watcher.Remove(w)
			}
		}
	}

	// Add new watches.
	for p := range desired {
		if err := watcher.Add(p); err != nil {
			fmt.Fprintf(stderr, "watch: cannot watch %s: %v\n", p, err)
		}
	}

	return nil
}

// addDirRecursive adds a directory and all its subdirectories to the watcher.
func addDirRecursive(watcher *fsnotify.Watcher, dir string, stderr io.Writer) {
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			if util.IgnoredName(d.Name()) {
				return filepath.SkipDir
			}
			if err := watcher.Add(p); err != nil {
				fmt.Fprintf(stderr, "watch: cannot watch %s: %v\n", p, err)
			}
		}
		return nil
	})
}

// PackSourceDirs extracts the list of pack source root directories from a profile.
func PackSourceDirs(profile domain.Profile) []string {
	seen := map[string]bool{}
	var dirs []string
	for _, pack := range profile.Packs {
		if pack.Root == "" {
			continue
		}
		abs, err := filepath.Abs(pack.Root)
		if err != nil {
			continue
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		dirs = append(dirs, abs)
	}
	return dirs
}
