package util

import "testing"

func FuzzWalkEnvRefs(f *testing.F) {
	f.Add("{env:HOME}")
	f.Add("{env:}")
	f.Add("{env:UNTERMINATED")
	f.Add("prefix {env:A} middle {env:B} suffix")

	f.Fuzz(func(t *testing.T, s string) {
		_ = WalkEnvRefs(s, func(ref EnvRef) error { return nil })
	})
}

func FuzzExpandEnvRefs(f *testing.F) {
	f.Add("{env:HOME}")
	f.Add("{env:}")
	f.Add("{env:UNTERMINATED")
	f.Add("no refs")
	f.Add("{env:A}{env:B}")

	f.Fuzz(func(t *testing.T, s string) {
		_, _ = ExpandEnvRefs(s)
	})
}
