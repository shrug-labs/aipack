package app

import (
	"fmt"

	"github.com/shrug-labs/aipack/internal/domain"
)

// FatalSyncError marks failures that must stop sync/save execution.
type FatalSyncError struct {
	Step string
	Err  error
}

func (e FatalSyncError) Error() string {
	return fmt.Sprintf("%s: %v", e.Step, e.Err)
}

func (e FatalSyncError) Unwrap() error {
	return e.Err
}

func wrapFatalSync(step string, err error) error {
	if err == nil {
		return nil
	}
	return FatalSyncError{Step: step, Err: err}
}

func warningf(field, format string, args ...any) domain.Warning {
	return domain.Warning{
		Field:   field,
		Message: fmt.Sprintf(format, args...),
	}
}

func warningPathf(path, field, format string, args ...any) domain.Warning {
	return domain.Warning{
		Path:    path,
		Field:   field,
		Message: fmt.Sprintf(format, args...),
	}
}
