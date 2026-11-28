package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setPairingClock fixes the pairing clock for a test, restoring it after.
func setPairingClock(t *testing.T, now time.Time) {
	t.Helper()
	old := pairingNow
	pairingNow = func() time.Time { return now }
	t.Cleanup(func() { pairingNow = old })
}

// resetPairingCode clears the process-wide pairing code between tests.
func resetPairingCode() {
	pairingNow = time.Now
	pairCodeMu.Lock()
	pairCode = ""
	pairCodeCreated = time.Time{}
	pairCodeFails = 0
	pairCodeMu.Unlock()
}

func newTestConfig(t *testing.T) *Config {
	t.Helper()
	dir := t.TempDir()
	return &Config{
		Port:       28789,
		Token:      "test-master-token-abcdef",
		DataDir:    dir,
		ConfigPath: filepath.Join(dir, "config.json"),
	}
}

func TestGeneratePairingCodeFormat(t *testing.T) {
	resetPairingCode()
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		code := GeneratePairingCode()
		if len(code) != PairCodeLen {
			t.Fatalf("code length %d, want %d (%q)", len(code), PairCodeLen, code)
		}
		for _, ch := range code {
			if !strings.ContainsRune(PairCodeChars, ch) {
				t.Fatalf("code %q contains char %q outside the alphabet", code, ch)
			}
		}
		if seen[code] {
			t.Fatalf("duplicate code generated: %q", code)
		}
		seen[code] = true
	}
}

func TestPairingCodeSingleUse(t *testing.T) {
	resetPairingCode()
	code := GeneratePairingCode()
	if !ValidatePairingCode(code) {
		t.Fatal("fresh code rejected")
	}
	if ValidatePairingCode(code) {
		t.Fatal("consumed code accepted a second time (replay)")
	}
}

func TestPairingCodeExpiry(t *testing.T) {
	resetPairingCode()
	base := time.Now()
	setPairingClock(t, base)
	code := GeneratePairingCode()
	setPairingClock(t, base.Add(PairCodeTTL+time.Minute))
	if ValidatePairingCode(code) {
		t.Fatal("expired code accepted")
	}
}

func TestPairingCodeWrongCode(t *testing.T) {
	resetPairingCode()
	GeneratePairingCode()
	if ValidatePairingCode("ABCDEFGH") {
		t.Fatal("wrong code accepted")
	}
	// A wrong guess must not void the real code.
	if !ValidatePairingCode(GetOrCreatePairingCode()) {
		t.Fatal("real code rejected after one wrong guess")
	}
}

func TestPairingCodeFiveFailsVoid(t *testing.T) {
	resetPairingCode()
	code := GeneratePairingCode()
	for i := 0; i < PairMaxFails; i++ {
		if ValidatePairingCode("ABCDEFGH") {
			t.Fatal("wrong code accepted")
		}
	}
	if ValidatePairingCode(code) {
		t.Fatal("code still valid after 5 failures")
	}
}

func TestPairingCodeNewInvalidatesOld(t *testing.T) {
	resetPairingCode()
	old := GeneratePairingCode()
	GeneratePairingCode()
	if ValidatePairingCode(old) {
		t.Fatal("old code still valid after a new one was generated")
	}
}

func TestIssueAndValidateDeviceToken(t *testing.T) {
	resetPairingCode()
	cfg := newTestConfig(t)
	cred, err := cfg.IssueDeviceToken("test-agent")
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}
	if cred.Token == "" || cred.Token == cfg.Token {
		t.Fatal("issued credential is empty or equals the master token")
	}
	if !cfg.IsValidDeviceToken(cred.Token) {
		t.Fatal("freshly issued device token rejected")
	}
	if cfg.IsValidDeviceToken("not-a-real-token") {
		t.Fatal("bogus device token accepted")
	}
	// The master token still authenticates (existing remote scripts
	// keep working).
	if !constantTimeEqual(cfg.Token, "test-master-token-abcdef") {
		t.Fatal("master token changed unexpectedly")
	}
}

func TestDeviceTokenExpiry(t *testing.T) {
	resetPairingCode()
	base := time.Now()
	setPairingClock(t, base)
	cfg := newTestConfig(t)
	cred, err := cfg.IssueDeviceToken("test-agent")
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}
	setPairingClock(t, base.Add(time.Duration(DeviceExpiryDays+1)*24*time.Hour))
	if cfg.IsValidDeviceToken(cred.Token) {
		t.Fatal("expired device token accepted")
	}
}

func TestRevokeDevice(t *testing.T) {
	resetPairingCode()
	cfg := newTestConfig(t)
	a, err := cfg.IssueDeviceToken("agent-a")
	if err != nil {
		t.Fatalf("issue a failed: %v", err)
	}
	b, err := cfg.IssueDeviceToken("agent-b")
	if err != nil {
		t.Fatalf("issue b failed: %v", err)
	}
	if !cfg.RevokePairedDevice(a.ID) {
		t.Fatal("revoke returned false for a known device")
	}
	if cfg.IsValidDeviceToken(a.Token) {
		t.Fatal("revoked device token still accepted")
	}
	if !cfg.IsValidDeviceToken(b.Token) {
		t.Fatal("unrelated device token broke after revoking another")
	}
	// Listing never exposes token plaintext.
	for _, v := range cfg.ListPairedDevices() {
		if v.Device != "agent-b" {
			t.Fatalf("unexpected listed device %q", v.Device)
		}
		if v.RemainingDays <= 0 || v.RemainingDays > DeviceExpiryDays {
			t.Fatalf("remaining days %d out of range", v.RemainingDays)
		}
	}
	if cfg.RevokePairedDevice("no-such-id") {
		t.Fatal("revoke returned true for an unknown id")
	}
}

func TestDeviceListCap(t *testing.T) {
	resetPairingCode()
	cfg := newTestConfig(t)
	for i := 0; i < MaxDevices+3; i++ {
		if _, err := cfg.IssueDeviceToken("agent"); err != nil {
			t.Fatalf("issue %d failed: %v", i, err)
		}
	}
	if n := len(cfg.ListPairedDevices()); n != MaxDevices {
		t.Fatalf("stored devices = %d, want cap %d", n, MaxDevices)
	}
}

func TestRotateClearsDevices(t *testing.T) {
	resetPairingCode()
	cfg := newTestConfig(t)
	cred, err := cfg.IssueDeviceToken("test-agent")
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}
	if _, err := RegenerateToken(cfg); err != nil {
		t.Fatalf("rotate failed: %v", err)
	}
	if cfg.IsValidDeviceToken(cred.Token) {
		t.Fatal("device token survived master token rotation")
	}
	if len(cfg.ListPairedDevices()) != 0 {
		t.Fatal("device list not empty after rotation")
	}
}

func TestQRConnectTextNoMasterToken(t *testing.T) {
	fakeMaster := "FAKEMASTERTOKEN1234567890abcdef"
	text := qrConnectText("100.99.88.77", 28789, "ABCDEFGH")
	if strings.Contains(text, fakeMaster) {
		t.Fatal("QR text contains the master token")
	}
	if strings.Contains(text, "Token：") {
		t.Fatal("QR text still carries a token field")
	}
	for _, want := range []string{"http://100.99.88.77:28789", "ABCDEFGH", "POST /pair"} {
		if !strings.Contains(text, want) {
			t.Errorf("QR text missing %q", want)
		}
	}
}

// TestHandlePairEndToEnd drives POST /pair through the real mux: no
// auth needed to pair, the issued token then authenticates /status.
func TestHandlePairEndToEnd(t *testing.T) {
	resetPairingCode()
	cfg := newTestConfig(t)
	srv := NewServer(cfg, NewCardStoreOrEmpty(filepath.Join(t.TempDir(), "cards.json")))
	h := srv.Handler()

	code := GeneratePairingCode()
	body, _ := json.Marshal(map[string]string{"code": code, "device": "e2e-agent"})
	req := httptest.NewRequest(http.MethodPost, "/pair", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pair status = %d, want 200", rec.Code)
	}
	var pr struct {
		OK            bool   `json:"ok"`
		Token         string `json:"token"`
		ExpiresInDays int    `json:"expires_in_days"`
		Device        string `json:"device"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pr); err != nil {
		t.Fatalf("pair response not JSON: %v", err)
	}
	if !pr.OK || pr.Token == "" || pr.ExpiresInDays != DeviceExpiryDays || pr.Device != "e2e-agent" {
		t.Fatalf("unexpected pair response: %+v", pr)
	}
	// The same code must not pair twice.
	req2 := httptest.NewRequest(http.MethodPost, "/pair", bytes.NewReader(body))
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	var pr2 struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rec2.Body.Bytes(), &pr2)
	if pr2.OK || pr2.Error != "invalid_code" {
		t.Fatalf("reused code response = %+v, want invalid_code", pr2)
	}
	// The issued device token authenticates /status; a bogus one does not.
	req3 := httptest.NewRequest(http.MethodGet, "/status", nil)
	req3.Header.Set("X-Token", pr.Token)
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("status with device token = %d, want 200", rec3.Code)
	}
	req4 := httptest.NewRequest(http.MethodGet, "/status", nil)
	req4.Header.Set("X-Token", "bogus-token")
	rec4 := httptest.NewRecorder()
	h.ServeHTTP(rec4, req4)
	if rec4.Code != http.StatusUnauthorized {
		t.Fatalf("status with bogus token = %d, want 401", rec4.Code)
	}
	// The master token still authenticates /status too.
	req5 := httptest.NewRequest(http.MethodGet, "/status", nil)
	req5.Header.Set("X-Token", cfg.Token)
	rec5 := httptest.NewRecorder()
	h.ServeHTTP(rec5, req5)
	if rec5.Code != http.StatusOK {
		t.Fatalf("status with master token = %d, want 200", rec5.Code)
	}
}
