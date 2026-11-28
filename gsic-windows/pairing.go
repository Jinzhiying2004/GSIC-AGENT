// Issue #3 (Windows, same as Android): one-time pairing code + per-device
// credentials. The master token never enters the QR code, the agent
// prompt or any copied content.
//
// Pairing code: 8 chars (unambiguous alphabet), crypto/rand; valid 10
// minutes, single use, memory-only (a restart invalidates it); only one
// valid code at a time, generating a new one voids the old; 5 failures
// on the same code void it; failures always return invalid_code.
//
// Device credential: on successful pairing, a random device token valid
// 30 days is issued and persisted in config.json (max 10, expired ones
// purged, oldest dropped when full); revocable per device; listed
// without ever returning the token plaintext. Rotating the master token
// clears all device credentials (see RegenerateToken in config.go).
package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"
)

// pairingNow is the clock (overridable in tests).
var pairingNow = time.Now

const (
	// PairCodeChars excludes ambiguous characters (I/L/O/0/1).
	PairCodeChars = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	// PairCodeLen is the pairing code length.
	PairCodeLen = 8
	// PairCodeTTL is the pairing code validity.
	PairCodeTTL = 10 * time.Minute
	// PairMaxFails voids a code after this many failures.
	PairMaxFails = 5
	// DeviceExpiryDays is the device credential validity.
	DeviceExpiryDays = 30
	// MaxDevices caps stored device credentials.
	MaxDevices = 10
)

// PairedDevice is one issued device credential, persisted in config.json.
type PairedDevice struct {
	ID          string `json:"id"`
	Token       string `json:"token"`
	Device      string `json:"device"`
	CreatedUnix int64  `json:"created"`
	ExpiryDays  int    `json:"expiryDays"`
}

// PairedDeviceView is the safe listing form: never contains the token.
type PairedDeviceView struct {
	ID            string `json:"id"`
	Device        string `json:"device"`
	RemainingDays int    `json:"remainingDays"`
}

// ---- pairing code (process-wide memory state, shared by the HTTP
// server and the GUI, like Android's static PairingManager state) ----

var (
	pairCodeMu      sync.Mutex
	pairCode        string
	pairCodeCreated time.Time
	pairCodeFails   int
)

// newPairingCode draws one uniform index per character from the
// alphabet (crypto/rand, rejection-free via math/big).
func newPairingCode() (string, error) {
	var sb strings.Builder
	sb.Grow(PairCodeLen)
	max := big.NewInt(int64(len(PairCodeChars)))
	for i := 0; i < PairCodeLen; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		sb.WriteByte(PairCodeChars[n.Int64()])
	}
	return sb.String(), nil
}

// GeneratePairingCode creates a new code (the old one is void at once).
func GeneratePairingCode() string {
	code, err := newPairingCode()
	if err != nil {
		Logf("WARN: pairing code generation failed (%v)", err)
		return ""
	}
	pairCodeMu.Lock()
	pairCode = code
	pairCodeCreated = pairingNow()
	pairCodeFails = 0
	pairCodeMu.Unlock()
	return code
}

// GetOrCreatePairingCode returns the current valid code, generating a
// new one when there is none or it expired.
func GetOrCreatePairingCode() string {
	pairCodeMu.Lock()
	defer pairCodeMu.Unlock()
	if pairCode != "" && pairingNow().Sub(pairCodeCreated) <= PairCodeTTL {
		return pairCode
	}
	code, err := newPairingCode()
	if err != nil {
		Logf("WARN: pairing code generation failed (%v)", err)
		return ""
	}
	pairCode = code
	pairCodeCreated = pairingNow()
	pairCodeFails = 0
	return code
}

// PairingCodeRemaining returns how long the current code stays valid
// (<= 0 when there is no usable code).
func PairingCodeRemaining() time.Duration {
	pairCodeMu.Lock()
	defer pairCodeMu.Unlock()
	if pairCode == "" {
		return 0
	}
	return pairCodeCreated.Add(PairCodeTTL).Sub(pairingNow())
}

// ValidatePairingCode checks a code: success consumes it (single use);
// failures count, and 5 failures void the code. Never distinguish
// wrong/expired in the result (callers return invalid_code either way).
func ValidatePairingCode(code string) bool {
	if code == "" {
		return false
	}
	pairCodeMu.Lock()
	defer pairCodeMu.Unlock()
	if pairCode == "" {
		return false
	}
	if pairingNow().Sub(pairCodeCreated) > PairCodeTTL {
		invalidatePairingCodeLocked()
		return false
	}
	if subtle.ConstantTimeCompare([]byte(code), []byte(pairCode)) == 1 {
		invalidatePairingCodeLocked()
		return true
	}
	pairCodeFails++
	if pairCodeFails >= PairMaxFails {
		invalidatePairingCodeLocked()
	}
	return false
}

func invalidatePairingCodeLocked() {
	pairCode = ""
	pairCodeCreated = time.Time{}
	pairCodeFails = 0
}

// qrConnectText builds the QR payload: address + one-time pairing code.
// It never contains any credential: the agent exchanges the code for a
// device credential via POST /pair first.
func qrConnectText(tailIP string, port int, pairingCode string) string {
	return fmt.Sprintf("GSIC 远控连接\n地址：http://%s:%d\n配对码：%s\n先 POST /pair 用配对码换设备凭据，再用 X-Token 请求头调 GET /agent-prompt 取完整操作说明，再开始工作。",
		tailIP, port, pairingCode)
}

// ---- device credentials (persisted in config.json via Config) ----

var pairedMu sync.Mutex

func newRandomHex(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// purgeExpiredPairedDevices drops expired entries, reporting whether
// any were dropped.
func purgeExpiredPairedDevices(devs []PairedDevice, nowUnix int64) ([]PairedDevice, bool) {
	kept := make([]PairedDevice, 0, len(devs))
	dropped := false
	for _, d := range devs {
		if d.CreatedUnix <= 0 || nowUnix-d.CreatedUnix > int64(d.ExpiryDays)*86400 {
			dropped = true
			continue
		}
		kept = append(kept, d)
	}
	return kept, dropped
}

// ListPairedDevices returns the paired devices without token plaintext.
func (c *Config) ListPairedDevices() []PairedDeviceView {
	pairedMu.Lock()
	defer pairedMu.Unlock()
	devs, dropped := purgeExpiredPairedDevices(c.PairedDevices, pairingNow().Unix())
	if dropped {
		c.PairedDevices = devs
		_ = c.Save()
	}
	out := make([]PairedDeviceView, 0, len(devs))
	now := pairingNow().Unix()
	for _, d := range devs {
		remainMs := d.CreatedUnix + int64(d.ExpiryDays)*86400 - now
		remainDays := 0
		if remainMs > 0 {
			remainDays = int(remainMs / 86400)
		}
		out = append(out, PairedDeviceView{ID: d.ID, Device: d.Device, RemainingDays: remainDays})
	}
	return out
}

// IssueDeviceToken signs a new device credential and persists it. When
// the save fails, no credential is issued (never hand out one that is
// not stored).
func (c *Config) IssueDeviceToken(device string) (PairedDevice, error) {
	pairedMu.Lock()
	defer pairedMu.Unlock()
	devs, _ := purgeExpiredPairedDevices(c.PairedDevices, pairingNow().Unix())
	for len(devs) >= MaxDevices {
		devs = devs[1:]
	}
	id, err := newRandomHex(16)
	if err != nil {
		return PairedDevice{}, err
	}
	tok, err := newRandomHex(16)
	if err != nil {
		return PairedDevice{}, err
	}
	d := PairedDevice{
		ID:          id,
		Token:       tok,
		Device:      device,
		CreatedUnix: pairingNow().Unix(),
		ExpiryDays:  DeviceExpiryDays,
	}
	old := c.PairedDevices
	c.PairedDevices = append(devs, d)
	if err := c.Save(); err != nil {
		c.PairedDevices = old
		return PairedDevice{}, err
	}
	return d, nil
}

// IsValidDeviceToken checks a device token, including its expiry.
func (c *Config) IsValidDeviceToken(tok string) bool {
	if tok == "" {
		return false
	}
	pairedMu.Lock()
	defer pairedMu.Unlock()
	devs, dropped := purgeExpiredPairedDevices(c.PairedDevices, pairingNow().Unix())
	if dropped {
		c.PairedDevices = devs
		_ = c.Save()
	}
	now := pairingNow().Unix()
	for _, d := range devs {
		if subtle.ConstantTimeCompare([]byte(tok), []byte(d.Token)) == 1 {
			return d.CreatedUnix > 0 && now-d.CreatedUnix <= int64(d.ExpiryDays)*86400
		}
	}
	return false
}

// RevokePairedDevice deletes one device credential by id.
func (c *Config) RevokePairedDevice(id string) bool {
	if id == "" {
		return false
	}
	pairedMu.Lock()
	defer pairedMu.Unlock()
	for i, d := range c.PairedDevices {
		if d.ID == id {
			c.PairedDevices = append(c.PairedDevices[:i], c.PairedDevices[i+1:]...)
			_ = c.Save()
			return true
		}
	}
	return false
}

// ClearPairedDevices voids all device credentials (master token
// rotation/revocation calls this).
func (c *Config) ClearPairedDevices() {
	pairedMu.Lock()
	defer pairedMu.Unlock()
	c.PairedDevices = nil
	_ = c.Save()
}
