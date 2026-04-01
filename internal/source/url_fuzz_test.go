package source

import "testing"

func FuzzProbePackURL(f *testing.F) {
	f.Add("https://github.com/acme/repo")
	f.Add("https://github.com/acme/repo/blob/main/pack.json")
	f.Add("https://raw.githubusercontent.com/acme/repo/main/pack.json")
	f.Add("https://bitbucket.org/workspace/repo")
	f.Add("https://example.com/repo.git")
	f.Add("ssh://git@example.com/PROJ/repo.git")
	f.Add("https://user:pass@example.com/repo.git")
	f.Add("not-a-url")

	f.Fuzz(func(t *testing.T, raw string) {
		_, _ = ProbePackURL(raw)
	})
}
