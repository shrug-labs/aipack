package common

import "time"

// DoubleClickTracker tracks repeated clicks on the same layer id.
type DoubleClickTracker struct {
	LastID string
	LastAt time.Time
}

// Hit records a click and reports whether it completes a double-click.
func (t *DoubleClickTracker) Hit(id string, window time.Duration) bool {
	now := time.Now()
	if t.LastID == id && !t.LastAt.IsZero() && now.Sub(t.LastAt) <= window {
		t.LastID = ""
		t.LastAt = time.Time{}
		return true
	}
	t.LastID = id
	t.LastAt = now
	return false
}
