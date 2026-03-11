package config

import (
	"fmt"
	"net/http"
	"net/url"
	pathpkg "path"
	"strings"
	"time"
)

// PackURLInfo holds the resolved repository URL, raw pack.json URL, and git ref
// extracted from a user-provided URL.
type PackURLInfo struct {
	RepoURL string
	PackURL string
	Ref     string
	SubPath string
}

// ProbePackURL resolves a raw URL into its repository, pack.json, and ref components.
func ProbePackURL(raw string) (PackURLInfo, error) {
	return probePackURL(raw)
}

// URLOK checks whether the given URL returns an HTTP 2xx response.
func URLOK(raw string) (bool, error) {
	return urlOK(raw)
}

func probePackURL(raw string) (PackURLInfo, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return PackURLInfo{}, err
	}
	if u.Scheme == "" || u.Host == "" {
		return PackURLInfo{}, fmt.Errorf("unsupported url format: %s", raw)
	}
	host := strings.ToLower(u.Host)
	path := strings.Trim(u.Path, "/")
	parts := strings.Split(path, "/")
	if host == "github.com" {
		if info, ok := probeGitHub(parts); ok {
			return info, nil
		}
		return PackURLInfo{}, fmt.Errorf("unsupported url format: %s", raw)
	}
	if host == "raw.githubusercontent.com" {
		if info, ok := probeGitHubRaw(parts); ok {
			return info, nil
		}
		return PackURLInfo{}, fmt.Errorf("unsupported url format: %s", raw)
	}
	if repoURL, ok := inferBitbucketCloneURL(raw); ok {
		info := PackURLInfo{RepoURL: repoURL, SubPath: subPathFromPackPath(parsePackPathFromURL(raw))}
		if packURL, ok := inferBitbucketPackURL(raw, "pack.json"); ok {
			info.PackURL = packURL
		}
		return info, nil
	}
	if repoURL, ok := inferOCIDevOpsCloneURL(raw); ok {
		return PackURLInfo{RepoURL: repoURL}, nil
	}
	if host == "bitbucket.org" || strings.Contains(path, "/projects/") || strings.HasPrefix(path, "scm/") {
		return PackURLInfo{}, fmt.Errorf("unsupported url format: %s", raw)
	}
	if looksLikeOCIDevOpsRepoURL(u) {
		return PackURLInfo{}, fmt.Errorf("unsupported url format: %s", raw)
	}
	if strings.HasSuffix(path, "pack.json") {
		repoURL := strings.TrimSuffix(raw, "/pack.json")
		return PackURLInfo{RepoURL: repoURL, PackURL: raw}, nil
	}
	return PackURLInfo{RepoURL: raw}, nil
}

func probeGitHub(parts []string) (PackURLInfo, bool) {
	if len(parts) < 2 {
		return PackURLInfo{}, false
	}
	owner := parts[0]
	repo := strings.TrimSuffix(parts[1], ".git")
	if len(parts) >= 5 && parts[2] == "blob" && parts[len(parts)-1] == "pack.json" {
		ref := parts[3]
		packPath := strings.Join(parts[4:], "/")
		packURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", owner, repo, ref, packPath)
		repoURL := fmt.Sprintf("https://github.com/%s/%s", owner, repo)
		return PackURLInfo{RepoURL: repoURL, PackURL: packURL, Ref: ref, SubPath: subPathFromPackPath(packPath)}, true
	}
	if len(parts) == 2 {
		repoURL := fmt.Sprintf("https://github.com/%s/%s", owner, repo)
		packURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/HEAD/pack.json", owner, repo)
		return PackURLInfo{RepoURL: repoURL, PackURL: packURL, Ref: ""}, true
	}
	if len(parts) == 3 && parts[2] == "pack.json" {
		repoURL := fmt.Sprintf("https://github.com/%s/%s", owner, repo)
		packURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/HEAD/pack.json", owner, repo)
		return PackURLInfo{RepoURL: repoURL, PackURL: packURL, Ref: ""}, true
	}
	return PackURLInfo{}, false
}

func probeGitHubRaw(parts []string) (PackURLInfo, bool) {
	if len(parts) < 4 {
		return PackURLInfo{}, false
	}
	if parts[len(parts)-1] != "pack.json" {
		return PackURLInfo{}, false
	}
	owner := parts[0]
	repo := strings.TrimSuffix(parts[1], ".git")
	ref := parts[2]
	repoURL := fmt.Sprintf("https://github.com/%s/%s", owner, repo)
	packPath := strings.Join(parts[3:], "/")
	packURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", owner, repo, ref, packPath)
	return PackURLInfo{RepoURL: repoURL, PackURL: packURL, Ref: ref, SubPath: subPathFromPackPath(packPath)}, true
}

func subPathFromPackPath(packPath string) string {
	packPath = strings.Trim(strings.TrimSpace(packPath), "/")
	if packPath == "" || packPath == "pack.json" {
		return ""
	}
	dir := strings.Trim(pathpkg.Dir(packPath), "/")
	if dir == "." {
		return ""
	}
	return dir
}

func parsePackPathFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	path := strings.Trim(u.Path, "/")
	if strings.Contains(path, "/browse/") {
		parts := strings.SplitN(path, "/browse/", 2)
		if len(parts) == 2 {
			return strings.TrimPrefix(parts[1], "/")
		}
	}
	if strings.Contains(path, "/raw/") {
		parts := strings.SplitN(path, "/raw/", 2)
		if len(parts) == 2 {
			return strings.TrimPrefix(parts[1], "/")
		}
	}
	if strings.HasSuffix(path, "pack.json") {
		return "pack.json"
	}
	return ""
}

func inferBitbucketCloneURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	host := strings.ToLower(u.Host)
	if host == "" {
		return "", false
	}
	if host == "bitbucket.org" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) < 2 {
			return "", false
		}
		workspace := parts[0]
		repo := strings.TrimSuffix(parts[1], ".git")
		return fmt.Sprintf("https://bitbucket.org/%s/%s.git", workspace, repo), true
	}
	info, ok := parseBitbucketServerRepo(u)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("https://%s/scm/%s/%s.git", info.Host, info.Project, info.Repo), true
}

func inferBitbucketPackURL(raw string, packPath string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	path := strings.Trim(u.Path, "/")
	if strings.Contains(path, "/browse/") {
		rel := strings.SplitN(path, "/browse/", 2)
		if len(rel) == 2 {
			path = rel[0] + "/raw/" + rel[1]
			u.Path = "/" + path
			return u.String(), true
		}
	}
	if strings.Contains(path, "/raw/") && strings.HasSuffix(path, "pack.json") {
		return u.String(), true
	}
	if strings.ToLower(u.Host) == "bitbucket.org" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) < 2 {
			return "", false
		}
		workspace := parts[0]
		repo := strings.TrimSuffix(parts[1], ".git")
		return fmt.Sprintf("https://bitbucket.org/%s/%s/raw/HEAD/%s", workspace, repo, packPath), true
	}
	info, ok := parseBitbucketServerRepo(u)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("https://%s/projects/%s/repos/%s/raw/%s", info.Host, info.Project, info.Repo, packPath), true
}

func inferOCIDevOpsCloneURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	info, ok := parseOCIDevOpsRepo(u)
	if !ok {
		return "", false
	}
	if strings.HasPrefix(strings.ToLower(u.Host), "devops.scmservice.") {
		return fmt.Sprintf("%s://%s/namespaces/%s/projects/%s/repositories/%s", u.Scheme, u.Host, info.Namespace, info.Project, info.Repo), true
	}
	region, ok := parseOCIDevOpsRegion(u)
	if !ok {
		return "", false
	}
	hostSuffix, ok := strings.CutPrefix(u.Host, "devops.")
	if !ok || hostSuffix == "" {
		return "", false
	}
	return fmt.Sprintf("%s://devops.scmservice.%s.%s/namespaces/%s/projects/%s/repositories/%s", u.Scheme, region, hostSuffix, info.Namespace, info.Project, info.Repo), true
}

type ociDevOpsRepoInfo struct {
	Namespace string
	Project   string
	Repo      string
}

func parseOCIDevOpsRepo(u *url.URL) (ociDevOpsRepoInfo, bool) {
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i+5 < len(parts); i++ {
		if parts[i] != "namespaces" || parts[i+2] != "projects" || parts[i+4] != "repositories" {
			continue
		}
		namespace := parts[i+1]
		project := parts[i+3]
		repo := strings.TrimSuffix(parts[i+5], ".git")
		if namespace == "" || project == "" || repo == "" {
			return ociDevOpsRepoInfo{}, false
		}
		return ociDevOpsRepoInfo{Namespace: namespace, Project: project, Repo: repo}, true
	}
	return ociDevOpsRepoInfo{}, false
}

func parseOCIDevOpsRegion(u *url.URL) (string, bool) {
	ctx := strings.TrimSpace(u.Query().Get("_ctx"))
	if ctx == "" {
		return "", false
	}
	region := strings.TrimSpace(strings.SplitN(ctx, ",", 2)[0])
	if region == "" {
		return "", false
	}
	return region, true
}

func looksLikeOCIDevOpsRepoURL(u *url.URL) bool {
	path := strings.Trim(u.Path, "/")
	if strings.Contains(path, "/devops-coderepository/") || strings.HasPrefix(path, "devops-coderepository/") {
		return true
	}
	_, ok := parseOCIDevOpsRepo(u)
	return ok
}

type bitbucketRepoInfo struct {
	Host    string
	Project string
	Repo    string
}

func parseBitbucketServerRepo(u *url.URL) (bitbucketRepoInfo, bool) {
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i+3 < len(parts); i++ {
		if parts[i] != "projects" {
			continue
		}
		if parts[i+2] != "repos" {
			continue
		}
		project := parts[i+1]
		repo := strings.TrimSuffix(parts[i+3], ".git")
		if project == "" || repo == "" {
			return bitbucketRepoInfo{}, false
		}
		return bitbucketRepoInfo{Host: u.Host, Project: project, Repo: repo}, true
	}
	if len(parts) >= 3 && parts[0] == "scm" {
		project := parts[1]
		repo := strings.TrimSuffix(parts[2], ".git")
		if project == "" || repo == "" {
			return bitbucketRepoInfo{}, false
		}
		return bitbucketRepoInfo{Host: u.Host, Project: project, Repo: repo}, true
	}
	return bitbucketRepoInfo{}, false
}

func urlOK(raw string) (bool, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", raw, nil)
	if err != nil {
		return false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return true, nil
}
