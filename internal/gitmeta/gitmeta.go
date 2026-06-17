package gitmeta

import (
	"bytes"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
)

type Metadata struct {
	RepoRoot     string
	WorktreeRoot string
	GitCommonDir string
	RepoRemote   string
	RepoBranch   string
	RepoHead     string
}

func Inspect(cwd string) Metadata {
	if cwd == "" {
		return Metadata{}
	}

	repoRoot := gitValue(cwd, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if repoRoot == "" {
		return Metadata{}
	}

	worktreeRoot := repoRoot
	gitCommonDir := gitValue(repoRoot, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if gitCommonDir != "" && !filepath.IsAbs(gitCommonDir) {
		gitCommonDir = filepath.Clean(filepath.Join(repoRoot, gitCommonDir))
	}

	repoRemote := gitValue(repoRoot, "remote", "get-url", "origin")
	if repoRemote == "" {
		remotes := strings.Fields(gitValue(repoRoot, "remote"))
		if len(remotes) > 0 {
			repoRemote = gitValue(repoRoot, "remote", "get-url", remotes[0])
		}
	}

	return Metadata{
		RepoRoot:     repoRoot,
		WorktreeRoot: worktreeRoot,
		GitCommonDir: gitCommonDir,
		RepoRemote:   NormalizeRemote(repoRemote),
		RepoBranch:   gitValue(repoRoot, "symbolic-ref", "--quiet", "--short", "HEAD"),
		RepoHead:     gitValue(repoRoot, "rev-parse", "HEAD"),
	}
}

func NormalizeRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}

	if strings.HasPrefix(remote, "git@") {
		parts := strings.SplitN(strings.TrimPrefix(remote, "git@"), ":", 2)
		if len(parts) == 2 {
			return normalizeHTTPURL("https://" + parts[0] + "/" + parts[1])
		}
	}

	if strings.HasPrefix(remote, "ssh://") {
		if parsed, err := url.Parse(remote); err == nil {
			host := parsed.Host
			path := strings.TrimPrefix(parsed.Path, "/")
			return normalizeHTTPURL("https://" + host + "/" + path)
		}
	}

	return normalizeHTTPURL(remote)
}

func normalizeHTTPURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return strings.TrimSuffix(strings.TrimSuffix(raw, ".git"), "/")
	}

	parsed.Scheme = "https"
	parsed.User = nil
	parsed.Fragment = ""
	parsed.RawQuery = ""
	parsed.Path = strings.TrimSuffix(parsed.Path, ".git")
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	return parsed.String()
}

func gitValue(cwd string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	cmd.Stdin = nil
	cmd.Stderr = nil
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(stdout.String())
}
