// GSIC Windows built-in Tailscale node (tsnet).
//
// The Windows GSIC embeds a tsnet node so the PC joins the user's
// tailnet without needing the separate Tailscale app. The node runs
// in-process (unlike Android, which uses a separate Go process).
//
// First run: the node has no state, so it prints a login URL to the
// log. The user opens it in a browser, authorizes the device, and
// the node joins the tailnet. The state is persisted to
// %APPDATA%\GSIC\tailscale\ so restarts don't need re-login.
//
// The HTTP server listens on 0.0.0.0:28789 (LAN) and the tailnet
// listener serves the same handler on the node's tailnet IP.
//
// Login URLs are sensitive (bearer auth): they go to the log file
// and console only, never to /status or any HTTP response.
package main

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/tsnet"
)

// TsnetNode wraps a tsnet.Server with GSIC's login/URL handling.
type TsnetNode struct {
	srv       *tsnet.Server
	stateDir  string
	hostname  string
	mu        sync.Mutex
	tailIP    string
	loginURL  string
	urlSince  time.Time
	running   bool
	lastState string
	// authKey is a Tailscale auth key (tskey-auth-...) used to join the
	// tailnet without interactive login. Set before Start (from config)
	// or at runtime via RestartWithAuthKey. A node that is already
	// logged in ignores it; it only takes effect on first join.
	authKey string
	// restartCh is signalled (buffered, size 1) when a new auth key is
	// applied while Start is waiting for login, so the node restarts
	// with the key instead of waiting for the browser login forever.
	restartCh chan struct{}
	// keyWarned tracks whether we already warned that a configured auth
	// key was not accepted (avoid spamming the log every poll round).
	keyWarned bool
}

// errRestartRequested is returned by Start when a new auth key was
// applied mid-startup; the caller should Start again with the new key.
var errRestartRequested = fmt.Errorf("restart requested (new auth key)")

// ValidAuthKey reports whether key looks like a Tailscale auth key:
// "tskey-auth-" followed by a long random part. API keys
// ("tskey-api-...") are rejected: they cannot join a tailnet.
func ValidAuthKey(key string) bool {
	key = strings.TrimSpace(key)
	if !strings.HasPrefix(key, "tskey-auth-") {
		return false
	}
	// Real auth keys are long (50+ chars); accept >= 30 to leave room
	// for future formats, but reject obvious fragments.
	return len(key) >= 30
}

// SetAuthKey stores the auth key for the next Start. Call before Start
// (main passes the configured key); for a running node use
// RestartWithAuthKey instead.
func (n *TsnetNode) SetAuthKey(key string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.authKey = strings.TrimSpace(key)
}

// AuthKeySet reports whether an auth key is configured (never returns
// the key itself).
func (n *TsnetNode) AuthKeySet() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.authKey != ""
}

// RestartWithAuthKey applies a new auth key and restarts the node with
// it. If the node is already Running, the key is only stored (it takes
// effect on the next start); returns alreadyRunning=true. Otherwise it
// interrupts the pending login wait and the caller should Start again
// (main's tsnet loop does that); returns alreadyRunning=false.
func (n *TsnetNode) RestartWithAuthKey(key string) (alreadyRunning bool, err error) {
	key = strings.TrimSpace(key)
	if !ValidAuthKey(key) {
		return false, fmt.Errorf("invalid auth key: expected tskey-auth-... (an auth key, not an API key)")
	}
	n.mu.Lock()
	running := n.running
	n.authKey = key
	n.keyWarned = false
	n.mu.Unlock()
	if running {
		return true, nil
	}
	// Interrupt the pending login wait: Close the server (its status
	// calls start failing) and signal the restart; waitForLogin turns
	// either into errRestartRequested.
	n.Close()
	select {
	case n.restartCh <- struct{}{}:
	default:
	}
	return false, nil
}

// NewTsnetNode creates a node. stateDir is the tsnet state directory
// (e.g. %APPDATA%\GSIC\tailscale). hostname is the tailnet node name.
func NewTsnetNode(stateDir, hostname string) *TsnetNode {
	return &TsnetNode{
		stateDir:  stateDir,
		hostname:  hostname,
		restartCh: make(chan struct{}, 1),
	}
}

// Start brings the node up. It blocks until the node is Running with
// a tailnet IP, or returns an error. During first-time login it logs
// the auth URL (once per new URL) and polls until authorized.
func (n *TsnetNode) Start() error {
	n.mu.Lock()
	if n.running {
		n.mu.Unlock()
		return nil
	}
	n.mu.Unlock()

	srv := &tsnet.Server{
		Hostname: n.hostname,
		Dir:      n.stateDir,
		AuthKey:  n.authKey,
		Logf: func(format string, args ...any) {
			msg := fmt.Sprintf(format, args...)
			// tsnet prints "go to: <url>" when login is needed.
			// Capture it for the log, don't pass the raw line through.
			if idx := strings.Index(msg, "go to: "); idx >= 0 {
				if u := strings.TrimSpace(msg[idx+len("go to: "):]); strings.HasPrefix(u, "https://") {
					n.reportURL(u)
				}
				return
			}
			// Other tsnet internals: debug only, don't spam the log.
		},
	}
	n.srv = srv

	if err := srv.Start(); err != nil {
		return fmt.Errorf("tsnet start: %w", err)
	}

	lc, err := srv.LocalClient()
	if err != nil {
		srv.Close()
		return fmt.Errorf("tsnet local client: %w", err)
	}

	// Wait for Running + IP. First run needs interactive login, unless
	// an auth key was configured (then the node joins on its own).
	ip, err := n.waitForLogin(lc)
	if err != nil {
		srv.Close()
		if err == errRestartRequested {
			return errRestartRequested
		}
		return err
	}

	n.mu.Lock()
	n.tailIP = ip
	n.running = true
	n.lastState = "Running"
	n.loginURL = ""
	n.mu.Unlock()

	Logf("tsnet: node %q online, tailnet IP %s", n.hostname, ip)
	return nil
}

// waitForLogin polls LocalClient Status until BackendState is Running
// with a tailnet IP. It logs the auth URL when NeedsLogin. It returns
// errRestartRequested promptly when a new auth key is applied mid-wait
// (RestartWithAuthKey), so the caller can restart with the key instead
// of waiting for the browser login forever.
func (n *TsnetNode) waitForLogin(lc *local.Client) (string, error) {
	for {
		st, err := lc.Status(context.Background())
		if err != nil {
			if n.restartRequested() {
				return "", errRestartRequested
			}
			return "", fmt.Errorf("tsnet status: %w", err)
		}
		if st == nil {
			if !n.sleepOrRestart(2 * time.Second) {
				return "", errRestartRequested
			}
			continue
		}
		n.mu.Lock()
		n.lastState = st.BackendState
		n.mu.Unlock()

		if st.BackendState == ipn.Running.String() {
			for _, ip := range st.TailscaleIPs {
				if ip.IsValid() {
					return ip.String(), nil
				}
			}
			// Running but no IP yet: wait one more round.
		} else if st.AuthURL != "" {
			n.reportURL(st.AuthURL)
			// An auth key was configured but the node still needs a
			// browser login: the key was rejected (or is for another
			// tailnet). Say so once, so the user stops waiting on it.
			n.mu.Lock()
			keySet := n.authKey != ""
			warned := n.keyWarned
			if keySet && !warned {
				n.keyWarned = true
			}
			n.mu.Unlock()
			if keySet && !warned {
				Logf("tsnet: auth key was not accepted (still needs browser login); check the key and the tailnet it belongs to")
			}
		}
		if !n.sleepOrRestart(2 * time.Second) {
			return "", errRestartRequested
		}
	}
}

// restartRequested reports whether a restart was signalled (and
// consumes the signal).
func (n *TsnetNode) restartRequested() bool {
	select {
	case <-n.restartCh:
		return true
	default:
		return false
	}
}

// sleepOrRestart waits d unless a restart is signalled first.
// It returns false when a restart was signalled.
func (n *TsnetNode) sleepOrRestart(d time.Duration) bool {
	select {
	case <-n.restartCh:
		return false
	case <-time.After(d):
		return true
	}
}

// reportURL logs a new login URL (deduped). The URL is a bearer
// credential: it goes to the log only, never to HTTP responses.
func (n *TsnetNode) reportURL(url string) {
	if url == "" {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if url == n.loginURL {
		return
	}
	n.loginURL = url
	n.urlSince = time.Now()
	Logf("tsnet: login required, open this URL in your browser:")
	Logf("tsnet: %s", url)
}

// Listen returns a listener on the tailnet IP for the given port.
// Call after Start() succeeds.
func (n *TsnetNode) Listen(port int) (net.Listener, error) {
	if n.srv == nil {
		return nil, fmt.Errorf("tsnet node not started")
	}
	return n.srv.Listen("tcp", fmt.Sprintf(":%d", port))
}

// TailIP returns the node's tailnet IP, or "" if not online.
func (n *TsnetNode) TailIP() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.tailIP
}

// IsRunning reports whether the node is up with an IP.
func (n *TsnetNode) IsRunning() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.running
}

// State returns the last known BackendState string.
func (n *TsnetNode) State() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.lastState
}

// LoginURL returns the current pending login URL, or "" if none.
// Used only for the log; never exposed via HTTP.
func (n *TsnetNode) LoginURL() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.loginURL
}

// Close shuts the node down.
func (n *TsnetNode) Close() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.srv != nil {
		n.srv.Close()
		n.srv = nil
	}
	n.running = false
	n.tailIP = ""
}

// DefaultTsnetDir returns the tsnet state directory under the data dir.
func DefaultTsnetDir(dataDir string) string {
	return filepath.Join(dataDir, "tailscale")
}
