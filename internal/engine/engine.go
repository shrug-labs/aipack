package engine

import (
	"io"
	"os"
)

// Engine holds injectable dependencies for filesystem access and interactive
// I/O. All engine functions that perform I/O are methods on *Engine.
type Engine struct {
	FS       FS
	Interact Interactor
}

// New creates an Engine with the given dependencies.
// nil fs defaults to OSFS; nil interact defaults to TTYInteractor on stdin/stderr.
func New(fs FS, interact Interactor) *Engine {
	if fs == nil {
		fs = OSFS{}
	}
	if interact == nil {
		interact = &TTYInteractor{
			Stdin:  os.Stdin,
			Stderr: os.Stderr,
		}
	}
	return &Engine{FS: fs, Interact: interact}
}

// NewWithStderr creates an Engine using OSFS and a TTYInteractor that writes
// prompts to the given writer (typically the command's stderr).
func NewWithStderr(stderr io.Writer) *Engine {
	if stderr == nil {
		stderr = os.Stderr
	}
	return &Engine{
		FS: OSFS{},
		Interact: &TTYInteractor{
			Stdin:  os.Stdin,
			Stderr: stderr,
		},
	}
}
