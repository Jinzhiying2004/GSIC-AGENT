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
}

// NewTsnetNode creates a node. stateDir is the tsnet state directory
// (e.g. %APPDATA%\GSIC\tailscale). hostname is the tailnet node name.
func NewTsnetNode(stateDir, hostname string) *TsnetNode {
	return &TsnetNode{
		stateDir: stateDir,
		hostname: hostname,
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

	// Wait for Running + IP. First run needs interactive login.
	ip, err := n.waitForLogin(lc)
	if err != nil {
		srv.Close()
		return err
	}

	n.mu.Lock()
	n.tailIP = ip
	n.running = true
	n.lastState = "Running"
	n.mu.Unlock()

	Logf("tsnet: node %q online, tailnet IP %s", n.hostname, ip)
	return nil
}

// waitForLogin polls LocalClient Status until BackendState is Running
// with a tailnet IP. It logs the auth URL when NeedsLogin.
func (n *TsnetNode) waitForLogin(lc *local.Client) (string, error) {
	for {
		st, err := lc.Status(context.Background())
		if err != nil {
			return "", fmt.Errorf("tsnet status: %w", err)
		}
		if st == nil {
			time.Sleep(2 * time.Second)
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
		}
		time.Sleep(2 * time.Second)
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
