package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const githubRepo = "srbouffard/arok"

// Release holds the relevant fields from the GitHub releases API response.
type Release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// LatestRelease fetches the latest published release from GitHub.
func LatestRelease(ctx context.Context) (Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", githubRepo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var r Release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return Release{}, err
	}
	return r, nil
}

// CheckTimeout is the default timeout for version checks in doctor.
const CheckTimeout = 4 * time.Second

// IsNewer reports whether candidate is a newer semver than current.
// Both should be in "vMAJOR.MINOR.PATCH" form; any parse failure returns false.
func IsNewer(current, candidate string) bool {
	cv := parseSemver(current)
	nv := parseSemver(candidate)
	if cv == nil || nv == nil {
		return false
	}
	for i := range cv {
		if nv[i] > cv[i] {
			return true
		}
		if nv[i] < cv[i] {
			return false
		}
	}
	return false
}

func parseSemver(v string) []int {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return nil
	}
	out := make([]int, 3)
	for i, p := range parts {
		// strip any pre-release suffix (e.g. "1-g123abc-dirty" → "1")
		p, _, _ = strings.Cut(p, "-")
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out[i] = n
	}
	return out
}

// SelfUpdate downloads the latest release binary and replaces the running executable.
// It returns the new version tag on success.
func SelfUpdate(ctx context.Context) (string, error) {
	release, err := LatestRelease(ctx)
	if err != nil {
		return "", fmt.Errorf("fetch latest release: %w", err)
	}

	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return "", fmt.Errorf("resolve symlinks: %w", err)
	}

	assetName := fmt.Sprintf("arok-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	downloadURL := fmt.Sprintf(
		"https://github.com/%s/releases/download/%s/%s",
		githubRepo, release.TagName, assetName,
	)

	newBinary, err := downloadBinary(ctx, downloadURL, assetName)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", assetName, err)
	}
	defer os.Remove(newBinary)

	if err := replaceBinary(exePath, newBinary); err != nil {
		return "", fmt.Errorf("replace binary: %w", err)
	}

	return release.TagName, nil
}

// downloadBinary fetches the tarball and extracts the binary to a temp file.
// Returns the path to the extracted binary.
func downloadBinary(ctx context.Context, url, assetName string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned %d", resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return "", fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)

	// The binary inside the tarball is named e.g. "arok-linux-amd64"
	binaryName := strings.TrimSuffix(assetName, ".tar.gz")

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read tarball: %w", err)
		}
		if filepath.Base(hdr.Name) != binaryName {
			continue
		}

		tmp, err := os.CreateTemp("", "arok-update-*")
		if err != nil {
			return "", err
		}

		if _, err := io.Copy(tmp, tr); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return "", fmt.Errorf("extract binary: %w", err)
		}
		tmp.Close()

		if err := os.Chmod(tmp.Name(), 0o755); err != nil {
			os.Remove(tmp.Name())
			return "", err
		}
		return tmp.Name(), nil
	}

	return "", fmt.Errorf("binary %q not found in tarball", binaryName)
}

// replaceBinary atomically replaces dst with src.
func replaceBinary(dst, src string) error {
	backup := dst + ".old"

	if err := os.Rename(dst, backup); err != nil {
		return fmt.Errorf("backup current binary: %w", err)
	}

	if err := os.Rename(src, dst); err != nil {
		// attempt to restore
		_ = os.Rename(backup, dst)
		return fmt.Errorf("install new binary: %w", err)
	}

	_ = os.Remove(backup)
	return nil
}
