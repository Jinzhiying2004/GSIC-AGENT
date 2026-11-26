package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DefaultUpdateSource is the built-in update source (hard-coded, like
// the Android GSIC self-update foundation). It points at the
// version-windows.json attached to the latest GitHub Release: the
// user's PC cannot reach raw.githubusercontent.com (connection reset)
// nor cdn.jsdelivr.net, but objects.githubusercontent.com (which
// serves Release assets) works fine (verified 2026-11-22 via curl on
// the PC). Every Release must attach an up-to-date version-windows.json.
// The user can override it in config.json (update_source) if needed.
const DefaultUpdateSource = "https://github.com/Jinzhiying2004/GSIC-AGENT/releases/latest/download/version-windows.json"

// UpdateCheckInterval is the throttle between automatic checks
// (24 hours, same as Android GSIC: check, log, don't pester).
const UpdateCheckInterval = 24 * time.Hour

// versionInfo mirrors the JSON served at the update source.
// Example:
// {"version":"4.34","versionCode":434,
//
//	"exeUrl":"https://github.com/Jinzhiying2004/GSIC-AGENT/releases/download/v4.34/gsic-4.34-windows-x64.exe",
//	"sha256":"<hex>","notes":"..."}
//
// sha256 is mandatory (Issue #4, 2026-11-25 feedback): Check, Apply,
// ApplyPushed and /update/push all refuse an update without a valid
// 64-hex digest; build.sh prints the digest for the Release file.
type versionInfo struct {
	Version     string `json:"version"`
	VersionCode int    `json:"versionCode"`
	ExeURL      string `json:"exeUrl"`
	SHA256      string `json:"sha256"`
	Notes       string `json:"notes"`
}

// UpdateStatus is the cached result of the last check.
type UpdateStatus struct {
	Available     bool      `json:"available"`
	LatestVersion string    `json:"latest_version"`
	LatestCode    int       `json:"latest_code"`
	CurrentCode   int       `json:"current_code"`
	Notes         string    `json:"notes"`
	CheckedAt     time.Time `json:"checked_at"`
	Error         string    `json:"error,omitempty"`
}

// pendingUpdate is a pushed update waiting for manual confirmation
// (when auto-install is off).
type pendingUpdate struct {
	Version string
	URL     string
	SHA256  string
}

// Updater owns the check/download/apply cycle.
type Updater struct {
	cfg       *Config
	status    UpdateStatus
	client    *http.Client
	applying  bool
	lastCheck time.Time
	pending   *pendingUpdate
}

// NewUpdater builds an updater bound to the given config.
func NewUpdater(cfg *Config) *Updater {
	return &Updater{
		cfg: cfg,
		client: &http.Client{
			Timeout: 60 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects")
				}
				// Only allow HTTPS redirects (no downgrade to HTTP).
				if req.URL.Scheme != "https" {
					return fmt.Errorf("redirect to non-HTTPS URL blocked: %s", req.URL)
				}
				return nil
			},
		},
		status: UpdateStatus{CurrentCode: VersionCode},
	}
}

// Source returns the effective update source URL.
func (u *Updater) Source() string {
	if u.cfg.UpdateSource != "" {
		return u.cfg.UpdateSource
	}
	return DefaultUpdateSource
}

// Status returns the cached check result.
func (u *Updater) Status() UpdateStatus {
	return u.status
}

// ShouldCheck reports whether an automatic check is due (24h throttle).
func (u *Updater) ShouldCheck() bool {
	if !u.cfg.AutoUpdate {
		return false
	}
	if u.lastCheck.IsZero() {
		// First run after start: check if the persisted timestamp is stale.
		if u.cfg.LastUpdateCheck.IsZero() {
			return true
		}
		return time.Since(u.cfg.LastUpdateCheck) >= UpdateCheckInterval
	}
	return time.Since(u.lastCheck) >= UpdateCheckInterval
}

// Check fetches version-windows.json and compares versionCode.
// It updates the cached status and persists the check timestamp.
func (u *Updater) Check() UpdateStatus {
	src := u.Source()
	if !strings.HasPrefix(src, "https://") {
		u.status = UpdateStatus{
			CurrentCode: VersionCode,
			CheckedAt:   time.Now(),
			Error:       "update source must be HTTPS",
		}
		return u.status
	}

	req, err := http.NewRequest("GET", src, nil)
	if err != nil {
		u.status = UpdateStatus{CurrentCode: VersionCode, CheckedAt: time.Now(), Error: err.Error()}
		return u.status
	}
	req.Header.Set("User-Agent", "gsic-windows/"+Version)

	resp, err := u.client.Do(req)
	if err != nil {
		u.status = UpdateStatus{CurrentCode: VersionCode, CheckedAt: time.Now(), Error: err.Error()}
		return u.status
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		u.status = UpdateStatus{
			CurrentCode: VersionCode, CheckedAt: time.Now(),
			Error: fmt.Sprintf("update source HTTP %d", resp.StatusCode),
		}
		return u.status
	}

	var info versionInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&info); err != nil {
		u.status = UpdateStatus{CurrentCode: VersionCode, CheckedAt: time.Now(), Error: "bad version JSON: " + err.Error()}
		return u.status
	}
	if info.VersionCode <= 0 || info.ExeURL == "" {
		u.status = UpdateStatus{CurrentCode: VersionCode, CheckedAt: time.Now(), Error: "version JSON missing versionCode or exeUrl"}
		return u.status
	}
	if !strings.HasPrefix(info.ExeURL, "https://") {
		u.status = UpdateStatus{CurrentCode: VersionCode, CheckedAt: time.Now(), Error: "exeUrl must be HTTPS"}
		return u.status
	}
	// Issue #4: a version without a usable hash is not safely
	// installable, so the check fails loudly instead of reporting
	// an update that Apply would then refuse.
	if !validSHA256(info.SHA256) {
		u.status = UpdateStatus{CurrentCode: VersionCode, CheckedAt: time.Now(), Error: "version JSON missing valid sha256, update refused"}
		return u.status
	}

	u.lastCheck = time.Now()
	u.cfg.LastUpdateCheck = u.lastCheck
	_ = u.cfg.Save() // best effort; a failed save must not block the check

	available := info.VersionCode > VersionCode
	u.status = UpdateStatus{
		Available:     available,
		LatestVersion: info.Version,
		LatestCode:    info.VersionCode,
		CurrentCode:   VersionCode,
		Notes:         info.Notes,
		CheckedAt:     u.lastCheck,
	}
	if available {
		Logf("update: %s (build %d) available, current is %s (build %d)", info.Version, info.VersionCode, Version, VersionCode)
	}
	return u.status
}

// CheckAsync runs Check in the background (startup check: log, don't block).
func (u *Updater) CheckAsync() {
	go func() {
		st := u.Check()
		if st.Error != "" {
			Logf("update check failed: %s", st.Error)
		}
	}()
}

// SetPending records a pushed update for later manual application.
func (u *Updater) SetPending(version, url, sha256 string) {
	u.pending = &pendingUpdate{Version: version, URL: url, SHA256: sha256}
	Logf("update: pending update recorded (version %s, auto-install off)", version)
}

// Pending returns the pending pushed update, if any.
func (u *Updater) Pending() *pendingUpdate {
	return u.pending
}

// ApplyPushed downloads and applies a pushed update (version/URL/hash
// supplied by Muse via /update/push). It shares the download/verify/
// swap pipeline with Apply but skips the version.json check.
func (u *Updater) ApplyPushed(version, url, sha256 string) error {
	if u.applying {
		return fmt.Errorf("update already in progress")
	}
	if !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("pushed URL must be HTTPS")
	}
	// Issue #4: a pushed update without a valid hash is refused
	// before any download, so unverified content can never reach
	// the swap step.
	if !validSHA256(sha256) {
		return fmt.Errorf("pushed update requires a valid sha256 (missing or malformed)")
	}
	u.applying = true
	defer func() { u.applying = false }()

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate current exe: %w", err)
	}
	exePath, err = filepath.Abs(exePath)
	if err != nil {
		return fmt.Errorf("resolve exe path: %w", err)
	}

	tmpDir := os.TempDir()
	newExe := filepath.Join(tmpDir, fmt.Sprintf("gsic-update-%s.exe", sanitizeVersion(version)))
	backupExe := exePath + ".bak"
	batPath := filepath.Join(tmpDir, "gsic-update.bat")

	Logf("update: downloading pushed %s from %s", version, url)
	if err := u.download(url, newExe); err != nil {
		return fmt.Errorf("download: %w", err)
	}
	// Hash is mandatory (checked above); verify before backup/swap.
	if err := verifySHA256(newExe, sha256); err != nil {
		_ = os.Remove(newExe)
		return fmt.Errorf("hash verification failed: %w", err)
	}
	Logf("update: sha256 verified (pushed)")
	if err := copyFile(exePath, backupExe); err != nil {
		_ = os.Remove(newExe)
		return fmt.Errorf("backup current exe: %w", err)
	}
	if err := writeUpdateBatch(batPath, exePath, newExe, backupExe); err != nil {
		_ = os.Remove(newExe)
		return fmt.Errorf("write updater script: %w", err)
	}
	Logf("update: launching updater for pushed %s, exiting", version)
	// The batch waits on %1 (old PID) before swapping; without it the
	// replace races the exit and fails on the locked exe (2026-11-22:
	// push to DESKTOP-6VTO4GK left nothing running).
	cmd := exec.Command("cmd", "/C", "start", "/min", "", batPath, fmt.Sprintf("%d", os.Getpid()))
	cmd.Dir = tmpDir
	if err := cmd.Start(); err != nil {
		_ = os.Remove(newExe)
		_ = os.Remove(batPath)
		return fmt.Errorf("launch updater: %w", err)
	}
	u.pending = nil
	time.Sleep(500 * time.Millisecond)
	return nil
}

// sanitizeVersion strips path-unsafe chars from a version string for use
// in a temp filename.
func sanitizeVersion(v string) string {
	var b strings.Builder
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "pushed"
	}
	return b.String()
}

// Apply downloads the latest exe, verifies its hash, and hands off to
// a batch script that replaces the running exe and restarts it.
// It returns an error if no update is available or any step fails.
// On success the caller should exit promptly (the batch file waits for
// this process to exit before replacing the exe).
func (u *Updater) Apply() error {
	if u.applying {
		return fmt.Errorf("update already in progress")
	}
	st := u.Check()
	if st.Error != "" {
		return fmt.Errorf("check failed: %s", st.Error)
	}
	if !st.Available {
		return fmt.Errorf("no update available (current %s build %d is latest)", Version, VersionCode)
	}

	// Re-fetch the full info (URL + hash) for the download.
	info, err := u.fetchInfo()
	if err != nil {
		return err
	}
	if info.VersionCode != st.LatestCode {
		return fmt.Errorf("version changed during update (check said %d, source now %d)", st.LatestCode, info.VersionCode)
	}
	// Issue #4: re-fetched info must carry a valid hash too; Check
	// already validated it, but the source may have changed since.
	if !validSHA256(info.SHA256) {
		return fmt.Errorf("version info missing valid sha256, refusing update")
	}

	u.applying = true
	defer func() { u.applying = false }()

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate current exe: %w", err)
	}
	exePath, err = filepath.Abs(exePath)
	if err != nil {
		return fmt.Errorf("resolve exe path: %w", err)
	}

	tmpDir := os.TempDir()
	newExe := filepath.Join(tmpDir, fmt.Sprintf("gsic-update-%d.exe", info.VersionCode))
	backupExe := exePath + ".bak"
	batPath := filepath.Join(tmpDir, "gsic-update.bat")

	// 1. Download.
	Logf("update: downloading %s", info.ExeURL)
	if err := u.download(info.ExeURL, newExe); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	// 2. Verify hash (mandatory, Issue #4: no hash, no install).
	if err := verifySHA256(newExe, info.SHA256); err != nil {
		_ = os.Remove(newExe)
		return fmt.Errorf("hash verification failed: %w", err)
	}
	Logf("update: sha256 verified")

	// 3. Back up the current exe.
	if err := copyFile(exePath, backupExe); err != nil {
		_ = os.Remove(newExe)
		return fmt.Errorf("backup current exe: %w", err)
	}

	// 4. Write the updater batch script.
	if err := writeUpdateBatch(batPath, exePath, newExe, backupExe); err != nil {
		_ = os.Remove(newExe)
		return fmt.Errorf("write updater script: %w", err)
	}

	// 5. Launch it detached, then exit. The script waits for this
	// process to die, swaps the exe, restarts, and deletes itself.
	Logf("update: launching updater, exiting for %s (build %d)", info.Version, info.VersionCode)
	// Pass the old PID: the batch waits on %1 before swapping (see ApplyPushed).
	cmd := exec.Command("cmd", "/C", "start", "/min", "", batPath, fmt.Sprintf("%d", os.Getpid()))
	cmd.Dir = tmpDir
	if err := cmd.Start(); err != nil {
		_ = os.Remove(newExe)
		_ = os.Remove(batPath)
		return fmt.Errorf("launch updater: %w", err)
	}
	// Give the batch file a moment to start before we exit.
	time.Sleep(500 * time.Millisecond)
	return nil
}

// fetchInfo re-downloads the version JSON (for Apply's URL + hash).
func (u *Updater) fetchInfo() (*versionInfo, error) {
	req, err := http.NewRequest("GET", u.Source(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "gsic-windows/"+Version)
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update source HTTP %d", resp.StatusCode)
	}
	var info versionInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&info); err != nil {
		return nil, fmt.Errorf("bad version JSON: %w", err)
	}
	return &info, nil
}

// download fetches url to dest (max 100 MiB), HTTPS only.
func (u *Updater) download(url, dest string) error {
	if !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("refusing non-HTTPS download URL")
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "gsic-windows/"+Version)
	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(resp.Body, 100<<20))
	if err != nil {
		_ = os.Remove(dest)
		return err
	}
	if n == 0 {
		_ = os.Remove(dest)
		return fmt.Errorf("downloaded empty file")
	}
	return nil
}

// validSHA256 reports whether s is a 64-char hex digest.
// Issue #4 (2026-11-25 feedback): a missing or malformed hash must
// refuse the update on every path, never silently skip verification.
func validSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// verifySHA256 checks a file against a hex SHA256 digest.
func verifySHA256(path, wantHex string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, strings.TrimSpace(wantHex)) {
		return fmt.Errorf("sha256 mismatch: got %s, want %s", got, wantHex)
	}
	return nil
}

// copyFile copies src to dst (used for the pre-update backup).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return out.Sync()
}

// writeUpdateBatch writes the self-replace script. It:
//  1. waits for the old GSIC process to exit (up to 30s),
//  2. moves the new exe over the old one (restores the backup on failure),
//  3. starts the new exe, and
//  4. deletes itself and the temp download.
func writeUpdateBatch(batPath, exePath, newExe, backupExe string) error {
	// %~dp0 is the batch file's own dir; quote every path (spaces in
	// %APPDATA% / Program Files are the norm on Windows).
	content := "@echo off\r\n" +
		"setlocal\r\n" +
		"set OLD_EXE=" + quoteBatch(exePath) + "\r\n" +
		"set NEW_EXE=" + quoteBatch(newExe) + "\r\n" +
		"set BAK_EXE=" + quoteBatch(backupExe) + "\r\n" +
		"set PID=%1\r\n" +
		"if \"%PID%\"==\"\" (\r\n" +
		"  rem No PID passed: wait 3s for the old process to exit, then swap.\r\n" +
		"  ping -n 4 127.0.0.1 >nul\r\n" +
		"  goto :swapped\r\n" +
		")\r\n" +
		"\r\n" +
		"rem Wait for the old process to exit (max ~30s).\r\n" +
		"for /L %%i in (1,1,60) do (\r\n" +
		"  tasklist /FI \"PID eq %PID%\" 2>nul | findstr /I \"%PID%\" >nul\r\n" +
		"  if errorlevel 1 goto :swapped\r\n" +
		"  ping -n 2 127.0.0.1 >nul\r\n" +
		")\r\n" +
		"rem Still running: force-kill it, then proceed.\r\n" +
		"taskkill /PID %PID% /F >nul 2>&1\r\n" +
		"ping -n 2 127.0.0.1 >nul\r\n" +
		"\r\n" +
		":swapped\r\n" +
		"move /Y %NEW_EXE% %OLD_EXE% >nul\r\n" +
		"if errorlevel 1 (\r\n" +
		"  echo [gsic-update] replace failed, restoring backup\r\n" +
		"  if exist %BAK_EXE% copy /Y %BAK_EXE% %OLD_EXE% >nul\r\n" +
		"  goto :done\r\n" +
		")\r\n" +
		"echo [gsic-update] updated, restarting\r\n" +
		"start \"\" %OLD_EXE%\r\n" +
		"\r\n" +
		":done\r\n" +
		"del /F /Q %NEW_EXE% >nul 2>&1\r\n" +
		"del /F /Q %BAK_EXE% >nul 2>&1\r\n" +
		"(goto) 2>nul & del \"%~f0\"\r\n"
	return os.WriteFile(batPath, []byte(content), 0o700)
}

// quoteBatch wraps a path in double quotes for cmd.exe.
func quoteBatch(p string) string {
	return `"` + p + `"`
}
