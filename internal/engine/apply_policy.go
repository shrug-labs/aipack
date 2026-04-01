package engine

import "fmt"

// FatalApplyError marks failures that must stop sync to avoid inconsistent state.
type FatalApplyError struct {
	Step string
	Err  error
}

func (e FatalApplyError) Error() string {
	return fmt.Sprintf("%s: %v", e.Step, e.Err)
}

func (e FatalApplyError) Unwrap() error {
	return e.Err
}

func wrapFatalApply(step string, err error) error {
	if err == nil {
		return nil
	}
	return FatalApplyError{Step: step, Err: err}
}
