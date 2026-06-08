package selfupdate

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// (DefaultReleaseAPIURL and the GitHub releases fetch live in apply.go as
// LatestReleaseTag; CheckRelease below reuses them so the update-notify path
// and the self-replace path agree on the upstream tag.)

// InstallMethod identifies how the running binary was installed, which decides
// the upgrade command shown to the user and whether an in-place self-replace is
// safe (only "binary" installs own their executable; npm and Homebrew manage it
// and would clobber a self-replace on their next operation).
type InstallMethod string

const (
	InstallNpm     InstallMethod = "npm"
	InstallBrew    InstallMethod = "brew"
	InstallBinary  InstallMethod = "binary"
	InstallUnknown InstallMethod = "unknown"
)

// ReleaseStatus is the outcome of a release-tag update check.
type ReleaseStatus struct {
	// Current is the running binary's version tag (e.g. "v0.2.0").
	Current string
	// Latest is the newest published release tag (e.g. "v0.2.1").
	Latest string
	// UpdateAvailable is true only when Latest is confidently newer than
	// Current. When versions are unparseable or equal it is false, so the
	// user is never nagged on uncertainty.
	UpdateAvailable bool
}

// parseVersion splits a "vMAJOR.MINOR.PATCH" tag into numeric components. It
// tolerates a leading "v" and a trailing pre-release/build suffix (e.g.
// "v1.2.3-rc1" -> 1,2,3). ok is false when no numeric components parse, so
// callers can fall back to a conservative "no update" decision.
func parseVersion(tag string) (parts []int, ok bool) {
	s := strings.TrimSpace(tag)
	s = strings.TrimPrefix(s, "v")
	// Drop any pre-release/build metadata after the numeric core.
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return nil, false
	}
	for _, seg := range strings.Split(s, ".") {
		n, err := strconv.Atoi(strings.TrimSpace(seg))
		if err != nil {
			return nil, false
		}
		parts = append(parts, n)
	}
	return parts, len(parts) > 0
}

// compareParts returns -1 if a < b, 0 if equal, 1 if a > b, comparing
// component by component and treating a missing trailing component as 0
// (so 1.2 == 1.2.0).
func compareParts(a, b []int) int {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		var av, bv int
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}

// CompareVersions decides whether latest is a newer release than current. It is
// conservative: if either tag is unparseable it reports no update (rather than
// nagging on a string mismatch), and it never reports an update when the build
// version is unknown (empty or the dev placeholder "v0.0.0"/"unknown").
func CompareVersions(current, latest string) ReleaseStatus {
	st := ReleaseStatus{Current: strings.TrimSpace(current), Latest: strings.TrimSpace(latest)}
	if st.Current == "" || st.Current == "unknown" || st.Current == "v0.0.0" {
		return st
	}
	cur, curOK := parseVersion(current)
	lat, latOK := parseVersion(latest)
	if !curOK || !latOK {
		return st
	}
	st.UpdateAvailable = compareParts(lat, cur) > 0
	return st
}

// CheckRelease performs a full release-tag update check: fetch the latest tag
// (via LatestReleaseTag in apply.go) and compare it against currentVersion.
// Must not be called in offline mode.
func CheckRelease(ctx context.Context, apiURL, currentVersion string) (ReleaseStatus, error) {
	latest, err := LatestReleaseTag(ctx, apiURL)
	if err != nil {
		return ReleaseStatus{Current: strings.TrimSpace(currentVersion)}, err
	}
	return CompareVersions(currentVersion, latest), nil
}

// DetectInstallMethod inspects the running executable's path to infer how it was
// installed. npm is checked before Homebrew because a global npm package on a
// Homebrew-managed Node lives under ".../lib/node_modules/..." inside the
// Homebrew prefix — the node_modules marker must win.
func DetectInstallMethod() InstallMethod {
	exe, err := os.Executable()
	if err != nil {
		return InstallUnknown
	}
	return classifyExePath(exe)
}

// classifyExePath is the pure core of DetectInstallMethod, split out for tests.
func classifyExePath(exe string) InstallMethod {
	p := strings.ToLower(filepathToSlash(exe))
	switch {
	case strings.Contains(p, "/node_modules/"):
		return InstallNpm
	case strings.Contains(p, "/cellar/"), strings.Contains(p, "/homebrew/"), strings.Contains(p, "/linuxbrew/"):
		return InstallBrew
	default:
		return InstallBinary
	}
}

// filepathToSlash normalises path separators so detection works the same on
// Windows (where os.Executable may return backslashes) as on Unix.
func filepathToSlash(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}

// UpgradeCommand returns the command a user should run to update, tailored to
// how they installed BharatCode.
func UpgradeCommand(method InstallMethod) string {
	switch method {
	case InstallNpm:
		return "npm install -g bharatcode-cli@latest"
	case InstallBrew:
		return "brew upgrade bharatcode"
	case InstallBinary:
		return "bharatcode update"
	default:
		return "bharatcode update"
	}
}

// AdviceFor renders the one-line, user-facing update notice for the given
// install method, or the empty string when no update is available.
func (s ReleaseStatus) AdviceFor(method InstallMethod) string {
	if !s.UpdateAvailable {
		return ""
	}
	return fmt.Sprintf(
		"A new BharatCode is available (%s -> %s). Update: %s",
		s.Current, s.Latest, UpgradeCommand(method),
	)
}
