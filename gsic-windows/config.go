package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultPort matches the Android GSIC agent port so the same remote
// tooling works for both. Set GSIC_PORT or -port to change it
// (28790 is the suggested alternate when both run on one machine).
const DefaultPort = 28789

// Config is the runtime configuration. The token is never hard-coded:
// flag > GSIC_TOKEN env > config file > auto-generated and saved.
type Config struct {
	Port               int       `json:"port"`
	Token              string    `json:"token"`
	AutoUpdate         bool      `json:"auto_update"`
	AutoInstallUpdates bool      `json:"auto_install_updates"`
	UpdateChannel      string    `json:"update_channel"`
	UpdateSource       string    `json:"update_source"`
	LastUpdateCheck    time.Time `json:"last_update_check"`
	// TailscaleAuthKey is a Tailscale auth key (tskey-auth-...) used to
	// join the tailnet without interactive browser login. Needed on
	// machines that cannot reach the login page (e.g. no foreign
	// network for the Google sign-in): generate the key in the
	// Tailscale admin console (Settings -> Keys) on a machine that can
	// reach it, then paste it here / in the GUI / via
	// POST /tailscale/authkey. It is a secret like the token: stored in
	// config.json (0600), never logged, never sent in any response.
	// Empty means interactive login (the login URL flow). An existing
	// logged-in node ignores it; it only takes effect on first join.
	TailscaleAuthKey string `json:"tailscale_auth_key,omitempty"`
	// AllowLAN re-opens the plain HTTP listener on all interfaces
	// (Issue #2: default false — loopback only, remote access goes
	// through the built-in Tailscale node, like Android GSIC).
	AllowLAN       bool   `json:"allow_lan"`
	DataDir        string `json:"-"`
	ConfigPath     string `json:"-"`
	CardsPath      string `json:"-"`
	LogPath        string `json:"-"`
	TokenGenerated bool   `json:"-"`
}

type configFile struct {
	Port               int       `json:"port"`
	Token              string    `json:"token"`
	AutoUpdate         *bool     `json:"auto_update,omitempty"`
	AutoInstallUpdates *bool     `json:"auto_install_updates,omitempty"`
	UpdateChannel      string    `json:"update_channel,omitempty"`
	UpdateSource       string    `json:"update_source,omitempty"`
	LastUpdateCheck    time.Time `json:"last_update_check,omitempty"`
	AllowLAN           *bool     `json:"allow_lan,omitempty"`
	TailscaleAuthKey   string    `json:"tailscale_auth_key,omitempty"`
}

// DefaultDataDir returns %APPDATA%\GSIC on Windows and
// ~/.config/gsic elsewhere (used by the Linux smoke-test build).
func DefaultDataDir() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "GSIC")
}

// LoadConfig resolves port/token/data-dir from flags, env and file.
func LoadConfig(flagPort int, flagToken, flagDataDir string) (*Config, error) {
	cfg := &Config{Port: DefaultPort, AutoUpdate: true, AutoInstallUpdates: true, UpdateChannel: "stable"}

	if flagDataDir != "" {
		cfg.DataDir = flagDataDir
	} else if v := os.Getenv("GSIC_DATA_DIR"); v != "" {
		cfg.DataDir = v
	} else {
		cfg.DataDir = DefaultDataDir()
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	cfg.ConfigPath = filepath.Join(cfg.DataDir, "config.json")
	cfg.CardsPath = filepath.Join(cfg.DataDir, "skill_cards.json")
	cfg.LogPath = filepath.Join(cfg.DataDir, "gsic.log")

	// 1. config file (lowest priority of the persistent sources)
	var fileCfg configFile
	if data, err := os.ReadFile(cfg.ConfigPath); err == nil {
		_ = json.Unmarshal(data, &fileCfg)
		if fileCfg.Port != 0 {
			cfg.Port = fileCfg.Port
		}
		cfg.Token = fileCfg.Token
		if fileCfg.AutoUpdate != nil {
			cfg.AutoUpdate = *fileCfg.AutoUpdate
		}
		if fileCfg.AutoInstallUpdates != nil {
			cfg.AutoInstallUpdates = *fileCfg.AutoInstallUpdates
		}
		if fileCfg.UpdateChannel != "" {
			cfg.UpdateChannel = fileCfg.UpdateChannel
		}
		if fileCfg.UpdateSource != "" {
			cfg.UpdateSource = fileCfg.UpdateSource
		}
		cfg.LastUpdateCheck = fileCfg.LastUpdateCheck
		if fileCfg.AllowLAN != nil {
			cfg.AllowLAN = *fileCfg.AllowLAN
		}
		cfg.TailscaleAuthKey = fileCfg.TailscaleAuthKey
	}

	// 2. env
	if v := os.Getenv("GSIC_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 && p < 65536 {
			cfg.Port = p
		}
	}
	if v := os.Getenv("GSIC_TOKEN"); v != "" {
		cfg.Token = v
	}
	if v := os.Getenv("GSIC_TAILSCALE_AUTHKEY"); v != "" {
		cfg.TailscaleAuthKey = strings.TrimSpace(v)
	}

	// 3. flags (highest)
	if flagPort != 0 {
		cfg.Port = flagPort
	}
	if flagToken != "" {
		cfg.Token = flagToken
	}

	// 4. generate when nothing configured, and persist it.
	if cfg.Token == "" {
		tok, err := GenerateToken()
		if err != nil {
			return nil, fmt.Errorf("generate token: %w", err)
		}
		cfg.Token = tok
		cfg.TokenGenerated = true
		if err := cfg.Save(); err != nil {
			return nil, fmt.Errorf("save generated token: %w", err)
		}
	}
	return cfg, nil
}

// Save persists port + token + update settings to config.json (0600).
func (c *Config) Save() error {
	auto := c.AutoUpdate
	autoInstall := c.AutoInstallUpdates
	allowLAN := c.AllowLAN
	data, err := json.MarshalIndent(configFile{
		Port:               c.Port,
		Token:              c.Token,
		AutoUpdate:         &auto,
		AutoInstallUpdates: &autoInstall,
		UpdateChannel:      c.UpdateChannel,
		UpdateSource:       c.UpdateSource,
		LastUpdateCheck:    c.LastUpdateCheck,
		AllowLAN:           &allowLAN,
		TailscaleAuthKey:   c.TailscaleAuthKey,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.ConfigPath, data, 0o600)
}

// RegenerateToken creates a fresh token, saves it, and returns it.
func RegenerateToken(cfg *Config) (string, error) {
	tok, err := GenerateToken()
	if err != nil {
		return "", err
	}
	cfg.Token = tok
	if err := cfg.Save(); err != nil {
		return "", err
	}
	return tok, nil
}

// GenerateToken returns a 32-byte random hex token.
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
