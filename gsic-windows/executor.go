package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// StepResult is the outcome of one executed step.
type StepResult struct {
	Index  int                    `json:"index"`
	Action string                 `json:"action"`
	OK     bool                   `json:"ok"`
	Error  string                 `json:"error,omitempty"`
	Data   map[string]interface{} `json:"data,omitempty"`
}

// Executor runs /task step lists. It only executes; the caller decides.
type Executor struct {
	cards *CardStore
}

func NewExecutor(cards *CardStore) *Executor {
	return &Executor{cards: cards}
}

// maxCardDepth limits use_card recursion (cards calling cards).
const maxCardDepth = 3

// ExecuteSteps runs steps in order. With stopOnError (default) the run
// stops at the first failed step and returns the partial results.
func (e *Executor) ExecuteSteps(steps []json.RawMessage, stopOnError bool) []StepResult {
	return e.executeSteps(steps, stopOnError, 0, map[string]bool{})
}

func (e *Executor) executeSteps(steps []json.RawMessage, stopOnError bool, depth int, visiting map[string]bool) []StepResult {
	results := make([]StepResult, 0, len(steps))
	for i, raw := range steps {
		res := e.executeOne(i, raw, stopOnError, depth, visiting)
		results = append(results, res...)
		// executeOne may expand a card into several results; stop if
		// the last one failed and stopOnError is set.
		if stopOnError && len(res) > 0 && !res[len(res)-1].OK {
			break
		}
	}
	return results
}

// executeOne runs a single step. A use_card step expands to the card's
// own results (with the card id noted on each).
func (e *Executor) executeOne(index int, raw json.RawMessage, stopOnError bool, depth int, visiting map[string]bool) []StepResult {
	fail := func(action, format string, args ...interface{}) []StepResult {
		return []StepResult{{Index: index, Action: action, OK: false, Error: fmt.Sprintf(format, args...)}}
	}
	ok := func(action string, data map[string]interface{}) []StepResult {
		return []StepResult{{Index: index, Action: action, OK: true, Data: data}}
	}

	var head struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return fail("", "invalid step JSON: %v", err)
	}
	action := strings.ToLower(strings.TrimSpace(head.Action))
	if action == "" {
		return fail("", "step has no action")
	}

	switch action {

	case "click", "tap":
		var p struct {
			X      int    `json:"x"`
			Y      int    `json:"y"`
			Button string `json:"button"`
			Clicks int    `json:"clicks"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return fail(action, "bad params: %v", err)
		}
		if p.Clicks < 1 {
			p.Clicks = 1
		}
		if err := ClickMouse(p.X, p.Y, p.Button, p.Clicks); err != nil {
			return fail(action, "%v", err)
		}
		return ok(action, map[string]interface{}{"x": p.X, "y": p.Y, "button": p.Button, "clicks": p.Clicks})

	case "move", "mousemove":
		var p struct {
			X int `json:"x"`
			Y int `json:"y"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return fail(action, "bad params: %v", err)
		}
		if err := MoveMouse(p.X, p.Y); err != nil {
			return fail(action, "%v", err)
		}
		return ok(action, map[string]interface{}{"x": p.X, "y": p.Y})

	case "drag":
		var p struct {
			X1   int `json:"x1"`
			Y1   int `json:"y1"`
			X2   int `json:"x2"`
			Y2   int `json:"y2"`
			From *struct {
				X int `json:"x"`
				Y int `json:"y"`
			} `json:"from"`
			To *struct {
				X int `json:"x"`
				Y int `json:"y"`
			} `json:"to"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return fail(action, "bad params: %v", err)
		}
		if p.From != nil {
			p.X1, p.Y1 = p.From.X, p.From.Y
		}
		if p.To != nil {
			p.X2, p.Y2 = p.To.X, p.To.Y
		}
		if err := DragMouse(p.X1, p.Y1, p.X2, p.Y2); err != nil {
			return fail(action, "%v", err)
		}
		return ok(action, map[string]interface{}{"from": [2]int{p.X1, p.Y1}, "to": [2]int{p.X2, p.Y2}})

	case "scroll", "wheel":
		var p struct {
			X     int `json:"x"`
			Y     int `json:"y"`
			Delta int `json:"delta"`
			DY    int `json:"dy"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return fail(action, "bad params: %v", err)
		}
		if p.Delta == 0 {
			p.Delta = p.DY
		}
		if p.Delta == 0 {
			p.Delta = -3 // default: scroll down 3 notches
		}
		if err := ScrollMouse(p.X, p.Y, p.Delta); err != nil {
			return fail(action, "%v", err)
		}
		return ok(action, map[string]interface{}{"x": p.X, "y": p.Y, "delta": p.Delta})

	case "type":
		var p struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return fail(action, "bad params: %v", err)
		}
		if err := TypeText(p.Text); err != nil {
			return fail(action, "%v", err)
		}
		return ok(action, map[string]interface{}{"chars": len([]rune(p.Text))})

	case "key", "keys", "hotkey":
		var p struct {
			Keys interface{} `json:"keys"`
			Key  string      `json:"key"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return fail(action, "bad params: %v", err)
		}
		keys, err := normalizeKeys(p.Keys, p.Key)
		if err != nil {
			return fail(action, "%v", err)
		}
		if err := PressKeys(keys); err != nil {
			return fail(action, "%v", err)
		}
		return ok(action, map[string]interface{}{"keys": keys})

	case "shell":
		var p struct {
			Command string `json:"command"`
			Shell   string `json:"shell"`
			Timeout int    `json:"timeout"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return fail(action, "bad params: %v", err)
		}
		if strings.TrimSpace(p.Command) == "" {
			return fail(action, "empty command")
		}
		res, err := RunCommand(p.Command, p.Shell, p.Timeout)
		if err != nil {
			return fail(action, "%v", err)
		}
		data := map[string]interface{}{
			"stdout": res.Stdout, "stderr": res.Stderr, "exit_code": res.ExitCode,
		}
		if res.ExitCode != 0 {
			return []StepResult{{Index: index, Action: action, OK: false,
				Error: fmt.Sprintf("exit code %d", res.ExitCode), Data: data}}
		}
		return ok(action, data)

	case "screenshot":
		var p struct {
			Quality int `json:"quality"`
		}
		_ = json.Unmarshal(raw, &p)
		if p.Quality == 0 {
			p.Quality = 80
		}
		img, w, h, err := CaptureScreen(p.Quality)
		if err != nil {
			return fail(action, "%v", err)
		}
		return ok(action, map[string]interface{}{
			"width": w, "height": h, "format": "jpeg",
			"image": base64.StdEncoding.EncodeToString(img),
		})

	case "sleep", "wait":
		var p struct {
			MS      int     `json:"ms"`
			Seconds float64 `json:"seconds"`
		}
		_ = json.Unmarshal(raw, &p)
		d := time.Duration(p.MS) * time.Millisecond
		if d == 0 && p.Seconds > 0 {
			d = time.Duration(p.Seconds * float64(time.Second))
		}
		if d < 0 {
			d = 0
		}
		if d > 5*time.Minute {
			d = 5 * time.Minute
		}
		time.Sleep(d)
		return ok(action, map[string]interface{}{"slept_ms": d.Milliseconds()})

	case "window_list", "windows":
		wins, err := ListWindows()
		if err != nil {
			return fail(action, "%v", err)
		}
		return ok(action, map[string]interface{}{"windows": wins, "count": len(wins)})

	case "window_activate", "activate":
		var p struct {
			HWND  uint64 `json:"hwnd"`
			Title string `json:"title"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return fail(action, "bad params: %v", err)
		}
		if p.HWND != 0 {
			if err := ActivateWindow(p.HWND); err != nil {
				return fail(action, "%v", err)
			}
			return ok(action, map[string]interface{}{"hwnd": p.HWND})
		}
		if p.Title != "" {
			w, err := ActivateWindowByTitle(p.Title)
			if err != nil {
				return fail(action, "%v", err)
			}
			return ok(action, map[string]interface{}{"hwnd": w.HWND, "title": w.Title})
		}
		return fail(action, "need hwnd or title")

	case "clipboard_get":
		text, err := GetClipboardText()
		if err != nil {
			return fail(action, "%v", err)
		}
		return ok(action, map[string]interface{}{"text": text})

	case "clipboard_set":
		var p struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return fail(action, "bad params: %v", err)
		}
		if err := SetClipboardText(p.Text); err != nil {
			return fail(action, "%v", err)
		}
		return ok(action, map[string]interface{}{"chars": len([]rune(p.Text))})

	case "file_read":
		var p struct {
			Path   string `json:"path"`
			Base64 bool   `json:"base64"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return fail(action, "bad params: %v", err)
		}
		b, err := ReadFileBytes(p.Path)
		if err != nil {
			return fail(action, "%v", err)
		}
		if p.Base64 {
			return ok(action, map[string]interface{}{"path": p.Path, "base64": base64.StdEncoding.EncodeToString(b), "size": len(b)})
		}
		return ok(action, map[string]interface{}{"path": p.Path, "content": string(b), "size": len(b)})

	case "file_write":
		var p struct {
			Path    string `json:"path"`
			Content string `json:"content"`
			Base64  string `json:"base64"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return fail(action, "bad params: %v", err)
		}
		var data []byte
		if p.Base64 != "" {
			b, err := base64.StdEncoding.DecodeString(p.Base64)
			if err != nil {
				return fail(action, "bad base64: %v", err)
			}
			data = b
		} else {
			data = []byte(p.Content)
		}
		if err := WriteFileBytes(p.Path, data); err != nil {
			return fail(action, "%v", err)
		}
		return ok(action, map[string]interface{}{"path": p.Path, "size": len(data)})

	case "dir_list", "list_dir":
		var p struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return fail(action, "bad params: %v", err)
		}
		if p.Path == "" {
			p.Path = "."
		}
		entries, err := ListDir(p.Path)
		if err != nil {
			return fail(action, "%v", err)
		}
		return ok(action, map[string]interface{}{"path": p.Path, "entries": entries, "count": len(entries)})

	case "launch", "open", "start":
		var p struct {
			Path string   `json:"path"`
			Args []string `json:"args"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return fail(action, "bad params: %v", err)
		}
		if p.Path == "" {
			return fail(action, "empty path")
		}
		pid, err := Launch(p.Path, p.Args)
		if err != nil {
			return fail(action, "%v", err)
		}
		return ok(action, map[string]interface{}{"path": p.Path, "pid": pid})

	case "use_card":
		var p struct {
			CardID string `json:"card_id"`
			ID     string `json:"id"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return fail(action, "bad params: %v", err)
		}
		cardID := p.CardID
		if cardID == "" {
			cardID = p.ID
		}
		if cardID == "" {
			return fail(action, "use_card needs card_id")
		}
		if depth >= maxCardDepth {
			return fail(action, "card recursion too deep (limit %d)", maxCardDepth)
		}
		if visiting[cardID] {
			return fail(action, "card cycle detected: %q already running", cardID)
		}
		card := e.cards.Get(cardID)
		if card == nil {
			return fail(action, "unknown card %q", cardID)
		}
		visiting[cardID] = true
		sub := e.executeSteps(card.Steps, stopOnError, depth+1, visiting)
		delete(visiting, cardID)
		// Tag expanded results with the card they came from.
		for i := range sub {
			if sub[i].Data == nil {
				sub[i].Data = map[string]interface{}{}
			}
			sub[i].Data["card_id"] = cardID
		}
		if len(sub) == 0 {
			return fail(action, "card %q has no steps", cardID)
		}
		return sub

	default:
		return fail(action, "unknown action %q", action)
	}
}

// normalizeKeys accepts keys as ["ctrl","c"], "ctrl+c", or {"key": "enter"}.
func normalizeKeys(keysRaw interface{}, single string) ([]string, error) {
	if single != "" {
		return []string{single}, nil
	}
	switch v := keysRaw.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("empty keys")
		}
		parts := strings.Split(v, "+")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts, nil
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return nil, fmt.Errorf("keys must be strings")
			}
			out = append(out, s)
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("empty keys")
		}
		return out, nil
	default:
		return nil, fmt.Errorf("keys must be a string or string array")
	}
}
