//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/lxn/walk"
)

// GUI owns the native Windows interface: the Walk main window and
// the system tray icon (Walk's NotifyIcon). Double-clicking the tray
// icon shows the main window; closing the window minimizes to tray.
// This replaces the v4.35 web dashboard (/ui) with a real Win32 UI.
type GUI struct {
	cfg     *Config
	cards   *CardStore
	updater *Updater
	tsnet   *TsnetNode
	walkWin *WalkMainWindow

	notifyIcon *walk.NotifyIcon

	logLines []string
	logMu    sync.Mutex

	quitCh chan struct{}
	once   sync.Once
}

// NewGUI builds the GUI (does not show it yet).
func NewGUI(cfg *Config, cards *CardStore, updater *Updater) *GUI {
	return &GUI{
		cfg: cfg, cards: cards, updater: updater,
		quitCh: make(chan struct{}),
	}
}

// SetTsnet attaches the Tailscale node for the status display.
func (g *GUI) SetTsnet(n *TsnetNode) {
	g.tsnet = n
	if g.walkWin != nil {
		g.walkWin.SetTsnet(n)
	}
}

// SetWalkWindow attaches the native main window (created by main).
func (g *GUI) SetWalkWindow(w *WalkMainWindow) {
	g.walkWin = w
	if g.tsnet != nil {
		w.SetTsnet(g.tsnet)
	}
}

// Run creates the tray icon and blocks on the Walk message loop.
// Must be called on the main goroutine. The main window is created
// by the caller before Run; Run shows the tray and starts the loop.
func (g *GUI) Run() {
	// Create the tray icon attached to the main window.
	ni, err := walk.NewNotifyIcon(g.walkWin.MainWindow)
	if err != nil {
		Logf("WARN: tray icon unavailable (%v), window only", err)
	} else {
		g.notifyIcon = ni
		g.setupTray()
	}

	// Show the main window on first run (user can close to tray).
	g.walkWin.Show()

	// Block on the Walk message loop until the window is disposed
	// or the tray "Exit" action calls Quit.
	g.walkWin.Run()
}

// setupTray builds the tray icon menu.
func (g *GUI) setupTray() {
	ni := g.notifyIcon
	// Tray icon: same embedded app icon (rsrc icon group, ID 2 —
	// the manifest takes ID 1).
	if icon, err := walk.NewIconFromResourceId(2); err == nil {
		if err := ni.SetIcon(icon); err != nil {
			Logf("WARN: tray icon unavailable (%v)", err)
		}
	} else {
		Logf("WARN: tray icon unavailable (%v)", err)
	}
	ni.SetToolTip(fmt.Sprintf("GSIC %s - Muse's hands", Version))
	ni.SetVisible(true)

	// Left-click shows the window; double-click also shows it.
	ni.MouseDown().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			g.ShowMainWindow()
		}
	})

	// Right-click menu.
	showAction := walk.NewAction()
	showAction.SetText("显示主界面")
	showAction.Triggered().Attach(func() { g.ShowMainWindow() })

	checkAction := walk.NewAction()
	checkAction.SetText("检查更新")
	checkAction.Triggered().Attach(func() { g.CheckUpdateAsync() })

	restartAction := walk.NewAction()
	restartAction.SetText("重启服务")
	restartAction.Triggered().Attach(func() { g.Restart() })

	quitAction := walk.NewAction()
	quitAction.SetText("退出")
	quitAction.Triggered().Attach(func() { g.Quit() })

	ni.ContextMenu().Actions().Add(showAction)
	ni.ContextMenu().Actions().Add(checkAction)
	ni.ContextMenu().Actions().Add(restartAction)
	ni.ContextMenu().Actions().Add(walk.NewSeparatorAction())
	ni.ContextMenu().Actions().Add(quitAction)
}

// ShowMainWindow displays the native main window.
func (g *GUI) ShowMainWindow() {
	if g.walkWin != nil {
		g.walkWin.Show()
	}
}

// CheckUpdateAsync checks for updates in the background and logs the result.
func (g *GUI) CheckUpdateAsync() {
	if g.updater == nil {
		Logf("update check: updater not configured")
		return
	}
	go func() {
		st := g.updater.Check()
		if st.Error != "" {
			Logf("update check failed: %s", st.Error)
			return
		}
		if st.Available {
			Logf("update available: %s (build %d)", st.LatestVersion, st.LatestCode)
		} else {
			Logf("update check: already on latest (%s build %d)", Version, VersionCode)
		}
	}()
}

// Restart re-execs the current binary (used after config changes).
func (g *GUI) Restart() {
	exePath, err := os.Executable()
	if err != nil {
		Logf("restart failed: %v", err)
		return
	}
	Logf("restarting GSIC...")
	cmd := exec.Command(exePath, os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		Logf("restart failed: %v", err)
		return
	}
	time.Sleep(500 * time.Millisecond)
	os.Exit(0)
}

// Quit stops the GUI (disposes the tray icon and closes the window).
func (g *GUI) Quit() {
	g.once.Do(func() {
		close(g.quitCh)
		if g.notifyIcon != nil {
			g.notifyIcon.Dispose()
		}
		if g.walkWin != nil {
			g.walkWin.Dispose()
		}
	})
}

// AppendLog adds a line to the in-memory log buffer (shown on the log tab).
func (g *GUI) AppendLog(line string) {
	g.logMu.Lock()
	defer g.logMu.Unlock()
	g.logLines = append(g.logLines, line)
	if len(g.logLines) > 500 {
		g.logLines = g.logLines[len(g.logLines)-500:]
	}
}

// LogLines returns a copy of the buffered log lines.
func (g *GUI) LogLines() []string {
	g.logMu.Lock()
	defer g.logMu.Unlock()
	out := make([]string, len(g.logLines))
	copy(out, g.logLines)
	return out
}
