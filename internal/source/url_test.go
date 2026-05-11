package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProbePackURL_GitHubRepo(t *testing.T) {
	t.Parallel()
	info, err := ProbePackURL("https://github.com/acme/my-pack")
	if err != nil {
		t.Fatalf("ProbePackURL: %v", err)
	}
	if info.RepoURL != "https://github.com/acme/my-pack" {
		t.Fatalf("RepoURL = %q, want github repo URL", info.RepoURL)
	}
	if info.PackURL == "" {
		t.Fatal("expected non-empty PackURL")
	}
	if info.Ref != "" {
		t.Fatalf("expected empty Ref for bare repo URL, got %q", info.Ref)
	}
}

func TestIsRemoteInstallInputRequiresKnownHostBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  bool
	}{
		{"github.com/acme/my-pack", true},
		{"bitbucket.org/team/repo", true},
		{"github.comevil/acme/my-pack", false},
		{"bitbucket.orgevil/team/repo", false},
		{"https://github.com/acme/my-pack", true},
		{"git@github.com:acme/my-pack.git", true},
	}
	for _, tt := range tests {
		if got := IsRemoteInstallInput(tt.input); got != tt.want {
			t.Errorf("IsRemoteInstallInput(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestIsHTTPArchiveURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  bool
	}{
		{"https://example.com/packs/team.zip", true},
		{"https://example.com/packs/team.tar.gz?download=1", true},
		{"http://example.com/packs/team.tgz", true},
		{"ssh://host/org/repo.zip", false},
		{"git@host:org/repo.zip", false},
		{"github.com/org/repo.zip", false},
		{"./team.zip", false},
	}
	for _, tt := range tests {
		if got := IsHTTPArchiveURL(tt.input); got != tt.want {
			t.Errorf("IsHTTPArchiveURL(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestProbePackURL_GitHubRepoWithGitSuffix(t *testing.T) {
	t.Parallel()
	info, err := ProbePackURL("https://github.com/acme/my-pack.git")
	if err != nil {
		t.Fatalf("ProbePackURL: %v", err)
	}
	if info.RepoURL != "https://github.com/acme/my-pack" {
		t.Fatalf("RepoURL = %q, expected .git stripped", info.RepoURL)
	}
}

func TestProbePackURL_GitHubBlobRef(t *testing.T) {
	t.Parallel()
	info, err := ProbePackURL("https://github.com/acme/my-pack/blob/v2/pack.json")
	if err != nil {
		t.Fatalf("ProbePackURL: %v", err)
	}
	if info.RepoURL != "https://github.com/acme/my-pack" {
		t.Fatalf("RepoURL = %q", info.RepoURL)
	}
	if info.Ref != "v2" {
		t.Fatalf("Ref = %q, want %q", info.Ref, "v2")
	}
	if info.PackURL == "" {
		t.Fatal("expected non-empty PackURL")
	}
}

func TestProbePackURL_GitHubBlobSubdirPack(t *testing.T) {
	t.Parallel()
	info, err := ProbePackURL("https://github.com/acme/my-pack/blob/main/packs/team/pack.json")
	if err != nil {
		t.Fatalf("ProbePackURL: %v", err)
	}
	if info.PackURL != "https://raw.githubusercontent.com/acme/my-pack/main/packs/team/pack.json" {
		t.Fatalf("PackURL = %q", info.PackURL)
	}
	if info.SubPath != "packs/team" {
		t.Fatalf("SubPath = %q", info.SubPath)
	}
	if info.Ref != "main" {
		t.Fatalf("Ref = %q", info.Ref)
	}
}

func TestProbePackURL_GitHubRaw(t *testing.T) {
	t.Parallel()
	info, err := ProbePackURL("https://raw.githubusercontent.com/acme/my-pack/main/pack.json")
	if err != nil {
		t.Fatalf("ProbePackURL: %v", err)
	}
	if info.RepoURL != "https://github.com/acme/my-pack" {
		t.Fatalf("RepoURL = %q", info.RepoURL)
	}
	if info.Ref != "main" {
		t.Fatalf("Ref = %q, want %q", info.Ref, "main")
	}
}

func TestProbePackURL_GitHubRawSubdirPack(t *testing.T) {
	t.Parallel()
	info, err := ProbePackURL("https://raw.githubusercontent.com/acme/my-pack/main/packs/team/pack.json")
	if err != nil {
		t.Fatalf("ProbePackURL: %v", err)
	}
	if info.SubPath != "packs/team" {
		t.Fatalf("SubPath = %q", info.SubPath)
	}
	if info.PackURL != "https://raw.githubusercontent.com/acme/my-pack/main/packs/team/pack.json" {
		t.Fatalf("PackURL = %q", info.PackURL)
	}
}

func TestProbePackURL_GitHubRepoWithPackJSON(t *testing.T) {
	t.Parallel()
	info, err := ProbePackURL("https://github.com/acme/my-pack/pack.json")
	if err != nil {
		t.Fatalf("ProbePackURL: %v", err)
	}
	if info.RepoURL != "https://github.com/acme/my-pack" {
		t.Fatalf("RepoURL = %q", info.RepoURL)
	}
}

func TestProbePackURL_GenericPackJSONSuffix(t *testing.T) {
	t.Parallel()
	info, err := ProbePackURL("https://example.com/some/path/pack.json")
	if err != nil {
		t.Fatalf("ProbePackURL: %v", err)
	}
	if info.PackURL != "https://example.com/some/path/pack.json" {
		t.Fatalf("PackURL = %q", info.PackURL)
	}
	if info.RepoURL != "https://example.com/some/path" {
		t.Fatalf("RepoURL = %q", info.RepoURL)
	}
}

func TestProbePackURL_GenericRepositoryURL(t *testing.T) {
	t.Parallel()
	info, err := ProbePackURL("https://example.com/no-pack-here")
	if err != nil {
		t.Fatalf("ProbePackURL: %v", err)
	}
	if info.RepoURL != "https://example.com/no-pack-here" {
		t.Fatalf("RepoURL = %q", info.RepoURL)
	}
	if info.PackURL != "" {
		t.Fatalf("PackURL = %q, want empty", info.PackURL)
	}
}

func TestProbePackURL_RejectsCredentials(t *testing.T) {
	t.Parallel()
	_, err := ProbePackURL("https://user:pass@example.com/repo.git")
	if err == nil {
		t.Fatal("expected error for URL with credentials")
	}
	if got := err.Error(); got != "credentials in URL are not supported" {
		t.Fatalf("error = %q", got)
	}
}

func TestProbePackURL_BitbucketRepo(t *testing.T) {
	t.Parallel()
	info, err := ProbePackURL("https://bitbucket.org/workspace/myrepo")
	if err != nil {
		t.Fatalf("ProbePackURL: %v", err)
	}
	if info.RepoURL != "https://bitbucket.org/workspace/myrepo.git" {
		t.Fatalf("RepoURL = %q", info.RepoURL)
	}
	if info.PackURL != "https://bitbucket.org/workspace/myrepo/raw/HEAD/pack.json" {
		t.Fatalf("PackURL = %q", info.PackURL)
	}
}

func TestProbePackURL_BitbucketServerBrowseURL(t *testing.T) {
	t.Parallel()
	info, err := ProbePackURL("https://bitbucket.example.internal/projects/TEAM/repos/demo/browse")
	if err != nil {
		t.Fatalf("ProbePackURL: %v", err)
	}
	if info.RepoURL != "https://bitbucket.example.internal/scm/TEAM/demo.git" {
		t.Fatalf("RepoURL = %q", info.RepoURL)
	}
	if info.PackURL != "https://bitbucket.example.internal/projects/TEAM/repos/demo/raw/pack.json" {
		t.Fatalf("PackURL = %q", info.PackURL)
	}
}

func TestProbePackURL_BitbucketServerBrowseSubdirPack(t *testing.T) {
	t.Parallel()
	info, err := ProbePackURL("https://bitbucket.example.internal/projects/TEAM/repos/demo/browse/packs/team/pack.json")
	if err != nil {
		t.Fatalf("ProbePackURL: %v", err)
	}
	if info.PackURL != "https://bitbucket.example.internal/projects/TEAM/repos/demo/raw/packs/team/pack.json" {
		t.Fatalf("PackURL = %q", info.PackURL)
	}
	if info.SubPath != "packs/team" {
		t.Fatalf("SubPath = %q", info.SubPath)
	}
}

func TestProbePackURL_CloudDevOpsDetailsURL(t *testing.T) {
	t.Parallel()
	info, err := ProbePackURL("https://devops.example.internal/devops-coderepository/namespaces/demo-ns/projects/TEAM/repositories/demo-repo/details?_ctx=us-region-1%2Cdevops_scm_central")
	if err != nil {
		t.Fatalf("ProbePackURL: %v", err)
	}
	if info.RepoURL != "https://devops.scmservice.us-region-1.example.internal/namespaces/demo-ns/projects/TEAM/repositories/demo-repo" {
		t.Fatalf("RepoURL = %q", info.RepoURL)
	}
	if info.PackURL != "" {
		t.Fatalf("PackURL = %q, want empty", info.PackURL)
	}
}

func TestProbePackURL_CloudDevOpsDetailsURL_MissingRegion(t *testing.T) {
	t.Parallel()
	_, err := ProbePackURL("https://devops.example.internal/devops-coderepository/namespaces/demo-ns/projects/TEAM/repositories/demo-repo/details")
	if err == nil {
		t.Fatal("expected error for Cloud DevOps details URL without _ctx region")
	}
}

func TestProbePackURL_GitHubTooFewParts(t *testing.T) {
	t.Parallel()
	_, err := ProbePackURL("https://github.com/acme")
	if err == nil {
		t.Fatal("expected error for github URL with only 1 path part")
	}
}

func TestProbePackURL_GitHubRawTooFewParts(t *testing.T) {
	t.Parallel()
	_, err := ProbePackURL("https://raw.githubusercontent.com/acme/repo/main")
	if err == nil {
		t.Fatal("expected error for raw github URL without pack.json")
	}
}

func TestParsePackPathFromURL_Browse(t *testing.T) {
	t.Parallel()
	got := parsePackPathFromURL("https://bb.example.com/projects/FOO/repos/bar/browse/subdir/pack.json")
	if got != "subdir/pack.json" {
		t.Fatalf("parsePackPathFromURL browse = %q", got)
	}
}

func TestParsePackPathFromURL_Raw(t *testing.T) {
	t.Parallel()
	got := parsePackPathFromURL("https://bb.example.com/projects/FOO/repos/bar/raw/pack.json")
	if got != "pack.json" {
		t.Fatalf("parsePackPathFromURL raw = %q", got)
	}
}

func TestParsePackPathFromURL_Suffix(t *testing.T) {
	t.Parallel()
	got := parsePackPathFromURL("https://example.com/repo/pack.json")
	if got != "pack.json" {
		t.Fatalf("parsePackPathFromURL suffix = %q", got)
	}
}

func TestParsePackPathFromURL_NoMatch(t *testing.T) {
	t.Parallel()
	got := parsePackPathFromURL("https://example.com/repo/README.md")
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestInferBitbucketCloneURL_BitbucketOrg(t *testing.T) {
	t.Parallel()
	got, ok := inferBitbucketCloneURL("https://bitbucket.org/workspace/myrepo")
	if !ok {
		t.Fatal("expected ok")
	}
	if got != "https://bitbucket.org/workspace/myrepo.git" {
		t.Fatalf("got %q", got)
	}
}

func TestInferBitbucketCloneURL_BitbucketServer(t *testing.T) {
	t.Parallel()
	got, ok := inferBitbucketCloneURL("https://bb.example.com/projects/FOO/repos/bar/browse")
	if !ok {
		t.Fatal("expected ok")
	}
	if got != "https://bb.example.com/scm/FOO/bar.git" {
		t.Fatalf("got %q", got)
	}
}

func TestInferBitbucketCloneURL_SCMPath(t *testing.T) {
	t.Parallel()
	got, ok := inferBitbucketCloneURL("https://bb.example.com/scm/FOO/bar.git")
	if !ok {
		t.Fatal("expected ok")
	}
	if got != "https://bb.example.com/scm/FOO/bar.git" {
		t.Fatalf("got %q", got)
	}
}

func TestInferBitbucketCloneURL_UnsupportedHost(t *testing.T) {
	t.Parallel()
	_, ok := inferBitbucketCloneURL("https://example.com/repo")
	if ok {
		t.Fatal("expected not ok for unsupported host")
	}
}

func TestInferCloudDevOpsCloneURL_Details(t *testing.T) {
	t.Parallel()
	got, ok := inferOCIDevOpsCloneURL("https://devops.example.internal/devops-coderepository/namespaces/demo-ns/projects/TEAM/repositories/demo-repo/details?_ctx=us-region-1%2Cdevops_scm_central")
	if !ok {
		t.Fatal("expected ok")
	}
	if got != "https://devops.scmservice.us-region-1.example.internal/namespaces/demo-ns/projects/TEAM/repositories/demo-repo" {
		t.Fatalf("got %q", got)
	}
}

func TestInferCloudDevOpsCloneURL_ExistingSCMServiceURL(t *testing.T) {
	t.Parallel()
	got, ok := inferOCIDevOpsCloneURL("https://devops.scmservice.us-region-1.example.internal/namespaces/demo-ns/projects/TEAM/repositories/demo-repo")
	if !ok {
		t.Fatal("expected ok")
	}
	if got != "https://devops.scmservice.us-region-1.example.internal/namespaces/demo-ns/projects/TEAM/repositories/demo-repo" {
		t.Fatalf("got %q", got)
	}
}

func TestInferBitbucketPackURL_BrowseToRaw(t *testing.T) {
	t.Parallel()
	got, ok := inferBitbucketPackURL("https://bb.example.com/projects/FOO/repos/bar/browse/pack.json", "pack.json")
	if !ok {
		t.Fatal("expected ok")
	}
	if got != "https://bb.example.com/projects/FOO/repos/bar/raw/pack.json" {
		t.Fatalf("got %q", got)
	}
}

func TestInferBitbucketPackURL_AlreadyRaw(t *testing.T) {
	t.Parallel()
	got, ok := inferBitbucketPackURL("https://bb.example.com/projects/FOO/repos/bar/raw/pack.json", "pack.json")
	if !ok {
		t.Fatal("expected ok")
	}
	if got != "https://bb.example.com/projects/FOO/repos/bar/raw/pack.json" {
		t.Fatalf("got %q", got)
	}
}

func TestInferBitbucketPackURL_BitbucketOrg(t *testing.T) {
	t.Parallel()
	got, ok := inferBitbucketPackURL("https://bitbucket.org/workspace/myrepo", "pack.json")
	if !ok {
		t.Fatal("expected ok")
	}
	if got != "https://bitbucket.org/workspace/myrepo/raw/HEAD/pack.json" {
		t.Fatalf("got %q", got)
	}
}

func TestInferBitbucketPackURL_BitbucketServer(t *testing.T) {
	t.Parallel()
	got, ok := inferBitbucketPackURL("https://bb.example.com/projects/FOO/repos/bar", "pack.json")
	if !ok {
		t.Fatal("expected ok")
	}
	if got != "https://bb.example.com/projects/FOO/repos/bar/raw/pack.json" {
		t.Fatalf("got %q", got)
	}
}

func TestParseBitbucketServerRepo_Projects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		raw     string
		project string
		repo    string
	}{
		{"https://bb.example.com/projects/FOO/repos/bar/browse", "FOO", "bar"},
		{"https://bb.example.com/projects/FOO/repos/bar.git/browse", "FOO", "bar"},
		{"https://bb.example.com/scm/FOO/bar.git", "FOO", "bar"},
	}
	for _, tc := range tests {
		info, ok := inferBitbucketCloneURL(tc.raw)
		if !ok {
			t.Fatalf("expected ok for %s", tc.raw)
		}
		_ = info
	}
}

func TestSelectFetchStrategy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		url  string
		want FetchStrategy
	}{
		{"github https", "https://github.com/owner/repo", StrategyHTTPTarball},
		{"github https with .git", "https://github.com/owner/repo.git", StrategyHTTPTarball},
		{"github http", "http://github.com/owner/repo", StrategyHTTPTarball},
		{"github ssh", "git@github.com:owner/repo.git", StrategyShallowClone},
		{"bitbucket server ssh port 7999", "ssh://git@bitbucket.example.com:7999/PROJ/tools.git", StrategyShallowClone},
		{"bitbucket server scp-style", "git@bitbucket.example.com:7999/PROJ/repo.git", StrategyShallowClone},
		{"oci devops scm ssh", "ssh://devops.scmservice.us-phoenix-1.example.internal/namespaces/ns/projects/p/repositories/r", StrategyShallowClone},
		{"devops scm ssh alt region", "ssh://devops.scmservice.us-region-1.example.internal/namespaces/ns/projects/p/repositories/r", StrategyShallowClone},
		{"bitbucket.org https", "https://bitbucket.org/workspace/repo", StrategyShallowClone},
		{"unknown https", "https://example.com/some/repo.git", StrategyShallowClone},
		{"unknown ssh", "git@example.com:org/repo.git", StrategyShallowClone},
		{"not a url", "not-a-url", StrategyShallowClone},
		{"unparseable", "://bad-url", StrategyShallowClone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := SelectFetchStrategy(tc.url)
			if got != tc.want {
				t.Fatalf("SelectFetchStrategy(%q) = %d, want %d", tc.url, got, tc.want)
			}
		})
	}
}

func TestURLOK_Success(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ok, err := urlOK(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("urlOK: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true for 200")
	}
}

func TestURLOK_NotFound(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	ok, err := urlOK(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("urlOK: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for 404")
	}
}

func TestURLOK_ServerError(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	_, err := urlOK(context.Background(), ts.URL)
	if err == nil {
		t.Fatal("expected error for 500")
	}
}

func TestURLOK_RejectsUnsupportedScheme(t *testing.T) {
	t.Parallel()
	_, err := urlOK(context.Background(), "file:///tmp/pack.json")
	if err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
	if !strings.Contains(err.Error(), "unsupported URL scheme") {
		t.Fatalf("error = %q", err)
	}
}
