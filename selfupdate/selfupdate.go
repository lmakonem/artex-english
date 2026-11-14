// Package selfupdate implements ARTEX's one-click in-page update: it pulls the new binary from a
// GitHub Release, verifies it, stages it, and atomically swaps it in on the next startup.
//
// Overall division of labor (see start.sh / start.bat):
//
//	startup script = a dumb supervisor loop, responsible only for "after the process exits, decide by exit code whether to relaunch it"
//	this package    = all the error-prone logic (download / SHA256 verify / smoke test / swap / rollback on failure)
//
// The swap lives in Go rather than the script because the sha256 verification and smoke test would
// have to be written twice for sh and bat (sha256sum / shasum / certutil), and this is precisely
// the step that can least afford a mistake: swap in a binary that won't run and the supervisor will
// faithfully relaunch it over and over, leaving the user to rescue it by hand on the machine.
//
// A complete upgrade goes through three process launches:
//
//	1. The old server receives /api/update/apply -> download and verify -> stage artex.new -> exit 75
//	2. The script relaunches the old version -> Bootstrap finds artex.new -> verify+smoke -> swap -> exit 75
//	3. The script relaunches, now the new version -> Bootstrap records one attempt -> clears the marker after a successful start
//
// Any failing step falls back to the old version: at step 2, if verification fails, delete the
// staged file and keep running the old version; at step 3, if it fails to survive to the marker
// clear 3 times in a row (crashing before it comes up), artex.old is automatically swapped back in.
package selfupdate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ExitRestart is the "please relaunch me, supervisor" exit code (EX_TEMPFAIL). When the startup
// script sees it, it reruns immediately without counting toward crash backoff. 0 means the user
// stopped normally (the script exits the loop); anything else is treated as a crash.
const ExitRestart = 75

// maxAttempts is the number of startup attempts allowed after a swap. Each startup of the new
// version increments the counter by 1, and surviving past settleDelay clears the marker; crashing
// maxAttempts times in a row means the new version simply won't start, so it rolls back automatically.
const maxAttempts = 3

// Paths holds all the files involved in one upgrade, all kept under the **directory of the
// executable**. CWD is deliberately not used: when run as a service, the working directory may be /
// or any path, and using CWD would land the staged file elsewhere, breaking the swap logic entirely.
type Paths struct {
	Dir     string // directory of the executable
	Current string // currently running binary        artex      / artex.exe
	New     string // staged new version              artex.new  / artex.new.exe
	Sum     string // sha256 of the new version (hex) artex.new.sha256 / artex.new.exe.sha256
	Old     string // backup of the old version before the swap  artex.old  / artex.old.exe
	Marker  string // upgrade state marker            artex.upgrade.json
}

// ResolvePaths derives all upgrade paths from the current executable.
//
// On Windows the .new/.old files must also carry the .exe suffix, or the smoke test and
// post-swap execution would both fail, so the suffix is stripped off first and then reassembled,
// keeping the naming symmetric across both platforms.
func ResolvePaths() (Paths, error) {
	exe, err := os.Executable()
	if err != nil {
		return Paths{}, fmt.Errorf("locating the executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	name := filepath.Base(exe)
	ext := filepath.Ext(name) // ".exe" on Windows, usually empty on Unix
	stem := strings.TrimSuffix(name, ext)

	join := func(suffix string) string { return filepath.Join(dir, stem+suffix+ext) }
	return Paths{
		Dir:     dir,
		Current: exe,
		New:     join(".new"),
		Sum:     join(".new") + ".sha256",
		Old:     join(".old"),
		Marker:  filepath.Join(dir, stem+".upgrade.json"),
	}, nil
}

// marker records the progress of one swap, used to trigger an automatic rollback when the new version won't start.
type marker struct {
	From     string `json:"from"`     // version before the upgrade
	To       string `json:"to"`       // target version
	Attempts int    `json:"attempts"` // number of startup attempts since the swap
	StagedAt int64  `json:"staged_at"`
}

func readMarker(path string) (marker, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return marker{}, false
	}
	var m marker
	if json.Unmarshal(b, &m) != nil {
		return marker{}, false
	}
	return m, true
}

func writeMarker(path string, m marker) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// cleanStaged removes the staged files. A successful swap, a failed verification, and a user
// cancellation all go through it, to avoid a leftover artex.new being retried on the next startup.
func cleanStaged(p Paths) {
	_ = os.Remove(p.New)
	_ = os.Remove(p.Sum)
}

// CompareVersions compares two version numbers, returning -1/0/1 (a<b / a==b / a>b).
// ok=false means at least one side is not a comparable version number (for example a local dev
// build's "dev" or a git describe output like "0.3.7-2-gabc1234-dirty"), in which case the caller
// should disable one-click update, or it would "upgrade" an in-development build to a stable
// release and overwrite uncommitted changes.
func CompareVersions(a, b string) (int, bool) {
	av, aok := parseVersion(a)
	bv, bok := parseVersion(b)
	if !aok || !bok {
		return 0, false
	}
	for i := range 3 {
		if av[i] != bv[i] {
			if av[i] < bv[i] {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

// parseVersion parses a version number of the form "v0.3.7" / "0.3.7" into a [3]int.
//
// It only accepts a clean three-part form: on non-tag builds, build.sh uses git describe to
// produce suffixed versions like "0.3.7-2-gabc1234", which must be judged non-comparable rather
// than treated as 0.3.7 -- otherwise a dev build would be wrongly judged "already latest" or
// overwritten by a stable release.
func parseVersion(s string) ([3]int, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return [3]int{}, false
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// InDocker reports whether the process is running in a container. Under Docker, the swap writes to
// the container's writable layer, and `docker compose up -d` recreating the container reverts to
// the version baked into the image. That is expected behavior (the user is pulling a new image
// anyway at that point), but the frontend needs to be able to explain it accordingly.
func InDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	b, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	s := string(b)
	return strings.Contains(s, "docker") || strings.Contains(s, "containerd")
}
