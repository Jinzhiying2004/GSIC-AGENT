// GSIC Windows - Muse's hands on a Windows PC.
//
// A standalone HTTP remote-control agent for Windows 10/11 x64.
// It only executes, it never decides: the cloud Muse brain sends
// steps, this program carries them out (mouse, keyboard, screenshot,
// shell, windows, files, clipboard, skill cards).
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"
)

// Version info. Keep in step with the Android GSIC version line.
const (
	AppName     = "GSIC"
	Version     = "4.43"
	VersionCode = 443
	Platform    = "windows"
)

var startTime = time.Now()

func main() {
	var (
		flagPort      = flag.Int("port", 0, "HTTP listen port (default 28789, env GSIC_PORT)")
		flagToken     = flag.String("token", "", "auth token (env GSIC_TOKEN, else config file, else auto-generated)")
		flagDataDir   = flag.String("data-dir", "", "data directory (default %APPDATA%\\GSIC)")
		flagShowVer   = flag.Bool("version", false, "print version and exit")
		flagGenToken  = flag.Bool("gen-token", false, "generate a new token, save it to config and exit")
		flagNoLogFile = flag.Bool("no-log-file", false, "log to console only, no log file")
		flagNoTsnet   = flag.Bool("no-tsnet", false, "disable built-in Tailscale node (remote access then needs -allow-lan)")
		flagAllowLAN  = flag.Bool("allow-lan", false, "also listen on all interfaces (LAN); default is loopback only, remote access via Tailscale")
		flagHostname  = flag.String("hostname", "gsic-win", "tailnet node hostname (default gsic-win)")
		flagNoAutoUpd = flag.Bool("no-auto-update", false, "disable automatic update checks")
		flagNoGUI     = flag.Bool("no-gui", false, "run headless (no system tray / window, console only)")
	)
	flag.Parse()

	if *flagShowVer {
		fmt.Printf("%s %s (build %d) %s/%s\n", AppName, Version, VersionCode, runtime.GOOS, runtime.GOARCH)
		os.Exit(0)
	}

	cfg, err := LoadConfig(*flagPort, *flagToken, *flagDataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	if *flagGenToken {
		tok, err := RegenerateToken(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gen-token error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("new token saved to %s\ntoken: %s\n", cfg.ConfigPath, tok)
		os.Exit(0)
	}

	if err := InitLogger(cfg.LogPath, *flagNoLogFile); err != nil {
		fmt.Fprintf(os.Stderr, "logger error: %v\n", err)
		os.Exit(1)
	}
	defer CloseLogger()

	cards, err := NewCardStore(cfg.CardsPath)
	if err != nil {
		Logf("WARN: skill card store unavailable (%v), card features disabled", err)
		cards = NewCardStoreOrEmpty(cfg.CardsPath)
	}

	Logf("%s %s (build %d) starting", AppName, Version, VersionCode)
	Logf("data dir: %s", cfg.DataDir)
	if cfg.TokenGenerated {
		Logf("NOTE: no token configured, auto-generated one saved to %s", cfg.ConfigPath)
		Logf("NOTE: your token is %s (keep it secret)", cfg.Token)
	}

	// Self-update: -no-auto-update overrides the config file.
	updater := NewUpdater(cfg)
	if *flagNoAutoUpd {
		cfg.AutoUpdate = false
	}
	// Issue #2: the plain listener is loopback-only by default, like
	// Android GSIC (v2.42+); remote access goes through the built-in
	// Tailscale node. -allow-lan / config allow_lan re-opens LAN.
	if *flagAllowLAN {
		cfg.AllowLAN = true
	}
	srv := NewServer(cfg, cards)
	srv.SetUpdater(updater)

	// GUI (native Walk window + tray icon). Created early so the server
	// can feed it log lines; Run() blocks on the main thread later.
	var gui *GUI
	var walkWin *WalkMainWindow
	if !*flagNoGUI {
		gui = NewGUI(cfg, cards, updater)
		srv.SetGUI(gui)
		SetLogHook(gui.AppendLog)

		// Create the Walk main window now (hidden until Run shows it).
		var err error
		walkWin, err = NewWalkMainWindow(cfg, cards, updater, gui)
		if err != nil {
			Logf("WARN: native window unavailable (%v), tray only", err)
			walkWin = nil
		} else {
			gui.SetWalkWindow(walkWin)
		}
	}

	// Startup update check (background, 24h throttled, log only).
	if updater.ShouldCheck() {
		updater.CheckAsync()
	}

	// Built-in Tailscale node (unless -no-tsnet). Start runs in the
	// background: first run blocks on interactive login, and the GUI
	// must stay responsive to show the login URL meanwhile (there is
	// no console in the installed windowsgui build). The loop restarts
	// the node when a new auth key is applied at runtime (GUI or
	// POST /tailscale/authkey) while it was still waiting for login.
	var tsnode *TsnetNode
	if !*flagNoTsnet {
		tsnode = NewTsnetNode(DefaultTsnetDir(cfg.DataDir), *flagHostname)
		tsnode.SetAuthKey(cfg.TailscaleAuthKey)
		srv.SetTsnet(tsnode)
		if gui != nil {
			gui.SetTsnet(tsnode)
		}
		go func() {
			for {
				err := tsnode.Start()
				if err == errRestartRequested {
					Logf("tsnet: restarting with new auth key")
					continue
				}
				if err != nil {
					Logf("WARN: built-in Tailscale failed (%v), remote control unavailable (localhost only unless allow_lan)", err)
					return
				}
				Logf("tsnet: listening on tailnet %s:%d", tsnode.TailIP(), cfg.Port)
				ln, err := tsnode.Listen(cfg.Port)
				if err != nil {
					Logf("WARN: tailnet listen failed (%v), remote control unavailable (localhost only unless allow_lan)", err)
					// A restart may have been requested mid-start;
					// retry instead of giving up.
					if tsnode.restartRequested() {
						continue
					}
					return
				}
				tsSrv := &http.Server{
					Handler:      srv.Handler(),
					ReadTimeout:  30 * time.Second,
					WriteTimeout: 120 * time.Second,
					IdleTimeout:  120 * time.Second,
				}
				if err := tsSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
					Logf("tailnet server error: %v", err)
				}
				// Serve ended because the node was closed. If a
				// restart was requested, start again with the new key.
				if tsnode.restartRequested() {
					continue
				}
				return
			}
		}()
	}

	// Plain HTTP listener: loopback only by default (Issue #2); the
	// tailnet listener above serves remote access. allow_lan re-opens
	// 0.0.0.0 for LAN use (token auth still required, but the token
	// then travels in cleartext HTTP on the LAN).
	listenAddr := "127.0.0.1"
	if cfg.AllowLAN {
		listenAddr = "0.0.0.0"
		Logf("WARN: allow_lan is on, HTTP service listens on all interfaces (LAN can reach it)")
	}
	httpSrv := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", listenAddr, cfg.Port),
		Handler:      srv.Handler(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown on Ctrl+C / terminate.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		Logf("shutting down...")
		if tsnode != nil {
			tsnode.Close()
		}
		_ = httpSrv.Close()
		os.Exit(0)
	}()

	Logf("listening on http://%s:%d  (token auth: X-Token header)", listenAddr, cfg.Port)

	// The tailnet listener is attached by the tsnet goroutine above
	// once the node is up.

	if *flagNoGUI {
		// Headless: block on the HTTP server.
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			Logf("server error: %v", err)
			os.Exit(1)
		}
		return
	}

	// GUI mode: HTTP server in the background, Walk window on the main thread.
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			Logf("server error: %v", err)
			os.Exit(1)
		}
	}()

	if walkWin == nil {
		// Walk window failed; fall back to headless HTTP server.
		Logf("WARN: no GUI window, running headless")
		select {}
	}
	gui.Run() // blocks on the Walk message loop until quit
	Logf("shutting down...")
	if tsnode != nil {
		tsnode.Close()
	}
	_ = httpSrv.Close()
}
