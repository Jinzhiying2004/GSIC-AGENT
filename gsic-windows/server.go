package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server wires config + cards + executor into HTTP handlers.
type Server struct {
	cfg      *Config
	cards    *CardStore
	executor *Executor
	tsnet    *TsnetNode
	updater  *Updater
	gui      *GUI
	taskMu   sync.Mutex // one /task at a time: input devices are shared
}

func NewServer(cfg *Config, cards *CardStore) *Server {
	return &Server{cfg: cfg, cards: cards, executor: NewExecutor(cards)}
}

// SetTsnet attaches the built-in Tailscale node (nil = disabled).
func (s *Server) SetTsnet(n *TsnetNode) {
	s.tsnet = n
}

// SetUpdater attaches the self-updater (nil = disabled).
func (s *Server) SetUpdater(u *Updater) {
	s.updater = u
}

// SetGUI attaches the GUI (for the dashboard log feed).
func (s *Server) SetGUI(g *GUI) {
	s.gui = g
}

// Handler returns the full route table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/status", s.auth(s.handleStatus))
	mux.HandleFunc("/task", s.auth(s.handleTask))
	mux.HandleFunc("/screenshot", s.auth(s.handleScreenshot))
	mux.HandleFunc("/windows", s.auth(s.handleWindows))
	mux.HandleFunc("/window/activate", s.auth(s.handleWindowActivate))
	mux.HandleFunc("/clipboard", s.auth(s.handleClipboard))
	mux.HandleFunc("/file", s.auth(s.handleFile))
	mux.HandleFunc("/dir", s.auth(s.handleDir))
	mux.HandleFunc("/shell", s.auth(s.handleShell))
	mux.HandleFunc("/cards", s.auth(s.handleCards))
	mux.HandleFunc("/card", s.auth(s.handleCard))
	mux.HandleFunc("/agent-prompt", s.auth(s.handleAgentPrompt))
	mux.HandleFunc("/update/check", s.auth(s.handleUpdateCheck))
	mux.HandleFunc("/update/apply", s.auth(s.handleUpdateApply))
	mux.HandleFunc("/update/push", s.auth(s.handleUpdatePush))
	mux.HandleFunc("/tailscale/authkey", s.auth(s.handleTailscaleAuthKey))
	return mux
}

// ---- auth + JSON helpers ----

// auth requires the X-Token header to match the configured token
// (constant-time compare). Missing/wrong token -> 401.
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-Token")
		if got == "" || !constantTimeEqual(got, s.cfg.Token) {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"ok": false, "error": "unauthorized: bad or missing X-Token",
			})
			return
		}
		next(w, r)
	}
}

func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, format string, args ...interface{}) {
	writeJSON(w, code, map[string]interface{}{
		"ok": false, "error": fmt.Sprintf(format, args...),
	})
}

// readBodyJSON reads a JSON request body (max 16 MiB).
func readBodyJSON(r *http.Request, v interface{}) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return fmt.Errorf("empty body")
	}
	return json.Unmarshal(body, v)
}

// ---- handlers ----

// handleRoot is the only unauthenticated endpoint: a liveness ping
// that reveals nothing but the program name and version.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeErr(w, http.StatusNotFound, "not found: %s", r.URL.Path)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true, "name": AppName, "version": Version,
		"version_code": VersionCode, "platform": Platform,
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	hostname, _ := os.Hostname()
	sw, sh := ScreenSize()
	mx, my := GetMousePos()
	resp := map[string]interface{}{
		"ok":             true,
		"version":        Version,
		"version_code":   VersionCode,
		"platform":       Platform,
		"os":             runtime.GOOS,
		"arch":           runtime.GOARCH,
		"go_version":     runtime.Version(),
		"hostname":       hostname,
		"pid":            os.Getpid(),
		"port":           s.cfg.Port,
		"uptime_seconds": int(time.Since(startTime).Seconds()),
		"time":           time.Now().Format(time.RFC3339),
		"screen":         map[string]int{"width": sw, "height": sh},
		"mouse":          map[string]int{"x": mx, "y": my},
		"cards":          len(s.cards.List()),
		"token_auth":     true,
		"keep_awake":     KeepAwakeActive(),
	}
	// Activity-pulse state (how the keep-awake actually resets the
	// cloud platform's idle timer).
	if pc, pl := KeepAwakePulse(); pc > 0 {
		resp["keep_awake_pulses"] = pc
		resp["keep_awake_last_pulse"] = pl.Format(time.RFC3339)
	}
	// Built-in Tailscale status (no login URL: it's a bearer secret,
	// it only goes to the log file/console, never to HTTP).
	if s.tsnet != nil {
		resp["tsnet_running"] = s.tsnet.IsRunning()
		resp["tsnet_ip"] = s.tsnet.TailIP()
		resp["tsnet_state"] = s.tsnet.State()
		// Whether an auth key is saved (never the key itself).
		resp["tsnet_authkey_set"] = s.tsnet.AuthKeySet()
	} else {
		resp["tsnet_running"] = false
		resp["tsnet_ip"] = ""
		resp["tsnet_state"] = "disabled"
		resp["tsnet_authkey_set"] = false
	}
	if fg := ForegroundWindow(); fg != nil {
		resp["foreground_window"] = fg
	}
	// Self-update status (cached from the last check).
	if s.updater != nil {
		st := s.updater.Status()
		resp["update_available"] = st.Available
		resp["latest_version"] = st.LatestVersion
		resp["latest_code"] = st.LatestCode
		resp["auto_update"] = s.cfg.AutoUpdate
		if !st.CheckedAt.IsZero() {
			resp["update_checked_at"] = st.CheckedAt.Format(time.RFC3339)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// taskRequest accepts either {"steps": [...]} or a bare [...] array.
type taskRequest struct {
	Steps       []json.RawMessage `json:"steps"`
	StopOnError *bool             `json:"stop_on_error"`
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body: %v", err)
		return
	}
	var req taskRequest
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal(body, &req.Steps); err != nil {
			writeErr(w, http.StatusBadRequest, "bad steps array: %v", err)
			return
		}
	} else {
		if err := json.Unmarshal(body, &req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad task JSON: %v", err)
			return
		}
	}
	if len(req.Steps) == 0 {
		writeErr(w, http.StatusBadRequest, "task has no steps")
		return
	}
	stopOnError := true
	if req.StopOnError != nil {
		stopOnError = *req.StopOnError
	}

	taskID := newTaskID()
	Logf("task %s: %d steps (stop_on_error=%v)", taskID, len(req.Steps), stopOnError)

	// While GSIC runs tasks the PC must not sleep; it may sleep again
	// after 10 minutes idle (pay-per-use cloud desktop).
	NoteTaskActivity()
	defer NoteTaskDone()

	// Serialize tasks: two tasks driving one mouse/keyboard would garble
	// each other. A second task waits for the first to finish.
	s.taskMu.Lock()
	results := s.executor.ExecuteSteps(req.Steps, stopOnError)
	s.taskMu.Unlock()

	allOK := true
	for _, res := range results {
		if !res.OK {
			allOK = false
			break
		}
	}
	Logf("task %s: done ok=%v (%d results)", taskID, allOK, len(results))
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": allOK, "task_id": taskID, "results": results,
	})
}

// handleAgentPrompt returns the filled agent prompt (connection info +
// instructions) for an agent that already has the token — e.g. one that
// scanned the connect QR code and needs the full instructions.
func (s *Server) handleAgentPrompt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	ip := ""
	if s.tsnet != nil {
		ip = s.tsnet.TailIP()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true, "prompt": FillAgentPrompt(s.cfg, ip),
	})
}

func (s *Server) handleScreenshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	quality := 80
	if q := r.URL.Query().Get("quality"); q != "" {
		if v, err := strconv.Atoi(q); err == nil {
			quality = v
		}
	}
	img, width, height, err := CaptureScreen(quality)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "screenshot: %v", err)
		return
	}
	if r.URL.Query().Get("raw") == "1" {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("X-Width", strconv.Itoa(width))
		w.Header().Set("X-Height", strconv.Itoa(height))
		_, _ = w.Write(img)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true, "width": width, "height": height, "format": "jpeg",
		"image": base64.StdEncoding.EncodeToString(img),
	})
}

func (s *Server) handleWindows(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	wins, err := ListWindows()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list windows: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true, "windows": wins, "count": len(wins),
	})
}

func (s *Server) handleWindowActivate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var p struct {
		HWND  uint64 `json:"hwnd"`
		Title string `json:"title"`
	}
	if err := readBodyJSON(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, "bad JSON: %v", err)
		return
	}
	if p.HWND != 0 {
		if err := ActivateWindow(p.HWND); err != nil {
			writeErr(w, http.StatusInternalServerError, "%v", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "hwnd": p.HWND})
		return
	}
	if p.Title != "" {
		win, err := ActivateWindowByTitle(p.Title)
		if err != nil {
			writeErr(w, http.StatusNotFound, "%v", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "hwnd": win.HWND, "title": win.Title})
		return
	}
	writeErr(w, http.StatusBadRequest, "need hwnd or title")
}

func (s *Server) handleClipboard(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		text, err := GetClipboardText()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "%v", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "text": text})
	case http.MethodPost:
		var p struct {
			Text string `json:"text"`
		}
		if err := readBodyJSON(r, &p); err != nil {
			writeErr(w, http.StatusBadRequest, "bad JSON: %v", err)
			return
		}
		if err := SetClipboardText(p.Text); err != nil {
			writeErr(w, http.StatusInternalServerError, "%v", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "chars": len([]rune(p.Text))})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		path := r.URL.Query().Get("path")
		if path == "" {
			writeErr(w, http.StatusBadRequest, "missing ?path=")
			return
		}
		b, err := ReadFileBytes(path)
		if err != nil {
			writeErr(w, http.StatusNotFound, "%v", err)
			return
		}
		if r.URL.Query().Get("base64") == "1" {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"ok": true, "path": path, "size": len(b),
				"base64": base64.StdEncoding.EncodeToString(b),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok": true, "path": path, "size": len(b), "content": string(b),
		})
	case http.MethodPost:
		var p struct {
			Path    string `json:"path"`
			Content string `json:"content"`
			Base64  string `json:"base64"`
		}
		if err := readBodyJSON(r, &p); err != nil {
			writeErr(w, http.StatusBadRequest, "bad JSON: %v", err)
			return
		}
		if p.Path == "" {
			writeErr(w, http.StatusBadRequest, "missing path")
			return
		}
		var data []byte
		if p.Base64 != "" {
			b, err := base64.StdEncoding.DecodeString(p.Base64)
			if err != nil {
				writeErr(w, http.StatusBadRequest, "bad base64: %v", err)
				return
			}
			data = b
		} else {
			data = []byte(p.Content)
		}
		if err := WriteFileBytes(p.Path, data); err != nil {
			writeErr(w, http.StatusInternalServerError, "%v", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "path": p.Path, "size": len(data)})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}

func (s *Server) handleDir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "."
	}
	entries, err := ListDir(path)
	if err != nil {
		writeErr(w, http.StatusNotFound, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true, "path": path, "entries": entries, "count": len(entries),
	})
}

func (s *Server) handleShell(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var p struct {
		Command string `json:"command"`
		Shell   string `json:"shell"`
		Timeout int    `json:"timeout"`
	}
	if err := readBodyJSON(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, "bad JSON: %v", err)
		return
	}
	if strings.TrimSpace(p.Command) == "" {
		writeErr(w, http.StatusBadRequest, "empty command")
		return
	}
	NoteTaskActivity()
	defer NoteTaskDone()
	res, err := RunCommand(p.Command, p.Shell, p.Timeout)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true, "stdout": res.Stdout, "stderr": res.Stderr, "exit_code": res.ExitCode,
	})
}

func (s *Server) handleCards(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cards := s.cards.List()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok": true, "cards": cards, "count": len(cards),
		})
	case http.MethodPost:
		var c Card
		if err := readBodyJSON(r, &c); err != nil {
			writeErr(w, http.StatusBadRequest, "bad JSON: %v", err)
			return
		}
		if err := s.cards.Put(c); err != nil {
			writeErr(w, http.StatusBadRequest, "%v", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "id": c.ID})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}

func (s *Server) handleCard(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "missing ?id=")
		return
	}
	switch r.Method {
	case http.MethodGet:
		c := s.cards.Get(id)
		if c == nil {
			writeErr(w, http.StatusNotFound, "unknown card %q", id)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "card": c})
	case http.MethodDelete:
		if !s.cards.Delete(id) {
			writeErr(w, http.StatusNotFound, "unknown card %q", id)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "deleted": id})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "GET or DELETE only")
	}
}

// handleUpdateCheck triggers a manual update check (GET or POST).
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeErr(w, http.StatusServiceUnavailable, "updater not configured")
		return
	}
	st := s.updater.Check()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":             true,
		"available":      st.Available,
		"latest_version": st.LatestVersion,
		"latest_code":    st.LatestCode,
		"current_code":   st.CurrentCode,
		"notes":          st.Notes,
		"checked_at":     st.CheckedAt.Format(time.RFC3339),
		"error":          st.Error,
	})
}

// handleUpdateApply downloads and applies the latest update, then exits
// so the updater batch script can replace the exe and restart it.
func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.updater == nil {
		writeErr(w, http.StatusServiceUnavailable, "updater not configured")
		return
	}
	if err := s.updater.Apply(); err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	// Apply launched the updater script; reply first, then exit so the
	// script can swap the exe.
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true, "message": "update applied, restarting",
	})
	go func() {
		time.Sleep(1 * time.Second)
		Logf("update: exiting for restart")
		os.Exit(0)
	}()
}

// handleUpdatePush receives a pushed update notification from Muse
// (POST {"version","url","sha256"}). If auto-install is on, it downloads
// and applies immediately; otherwise it records the pending update for
// manual confirmation.
func (s *Server) handleUpdatePush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.updater == nil {
		writeErr(w, http.StatusServiceUnavailable, "updater not configured")
		return
	}
	var p struct {
		Version string `json:"version"`
		URL     string `json:"url"`
		SHA256  string `json:"sha256"`
	}
	if err := readBodyJSON(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, "bad JSON: %v", err)
		return
	}
	if p.URL == "" {
		writeErr(w, http.StatusBadRequest, "missing url")
		return
	}
	// Issue #4: a push without a valid hash is rejected here, before
	// it can be recorded as pending or installed; a pending update
	// without a hash could never be applied anyway.
	if !validSHA256(p.SHA256) {
		writeErr(w, http.StatusBadRequest, "missing or invalid sha256 (64 hex chars required)")
		return
	}
	Logf("update push received: version=%s url=%s", p.Version, p.URL)
	if !s.cfg.AutoInstallUpdates {
		// Record only; the user (or Muse) applies it later via /update/apply.
		s.updater.SetPending(p.Version, p.URL, p.SHA256)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok": true, "installed": false,
			"message": "auto-install disabled, update recorded as pending",
		})
		return
	}
	if err := s.updater.ApplyPushed(p.Version, p.URL, p.SHA256); err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true, "installed": true, "message": "pushed update applied, restarting",
	})
	go func() {
		time.Sleep(1 * time.Second)
		Logf("update: exiting for restart (pushed update)")
		os.Exit(0)
	}()
}

// handleTailscaleAuthKey saves a Tailscale auth key and joins the
// tailnet with it (POST {"auth_key": "tskey-auth-..."}). This is the
// way to join on a machine that cannot open the browser login (e.g.
// its Google sign-in needs foreign network): generate the key in the
// Tailscale admin console on a machine that can reach it, then send
// it here. The key is a secret: it is stored in config.json (0600)
// and never returned in any response.
func (s *Server) handleTailscaleAuthKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if s.tsnet == nil {
		writeErr(w, http.StatusServiceUnavailable, "built-in Tailscale is disabled (-no-tsnet)")
		return
	}
	var p struct {
		AuthKey string `json:"auth_key"`
	}
	if err := readBodyJSON(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, "bad JSON: %v", err)
		return
	}
	key := strings.TrimSpace(p.AuthKey)
	if !ValidAuthKey(key) {
		writeErr(w, http.StatusBadRequest, "invalid auth key: expected tskey-auth-... (an auth key, not an API key)")
		return
	}
	// Persist first: even an already-running node keeps the key for
	// the next start.
	s.cfg.TailscaleAuthKey = key
	if err := s.cfg.Save(); err != nil {
		writeErr(w, http.StatusInternalServerError, "save config: %v", err)
		return
	}
	if s.tsnet.IsRunning() {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok": true, "already_running": true,
			"tsnet_state": s.tsnet.State(), "tsnet_ip": s.tsnet.TailIP(),
			"message": "already on the tailnet; auth key saved for the next start",
		})
		return
	}
	alreadyRunning, err := s.tsnet.RestartWithAuthKey(key)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "%v", err)
		return
	}
	if alreadyRunning {
		// Raced with the node coming up; same outcome as above.
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok": true, "already_running": true,
			"tsnet_state": s.tsnet.State(), "tsnet_ip": s.tsnet.TailIP(),
			"message": "already on the tailnet; auth key saved for the next start",
		})
		return
	}
	Logf("tailscale: auth key applied, rejoining tailnet")
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true, "already_running": false,
		"tsnet_state": s.tsnet.State(), "tsnet_ip": s.tsnet.TailIP(),
		"message": "auth key saved, rejoining tailnet (poll /status for tsnet_ip)",
	})
}

func newTaskID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("task-%d", time.Now().UnixNano())
	}
	return "task-" + hex.EncodeToString(b)
}
