//go:build windows

package main

import (
	"fmt"
	"strings"
	"time"
	"unsafe"
)

// ---- SendInput plumbing (user32) ----

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseMove       = 0x0001
	mouseLeftDown   = 0x0002
	mouseLeftUp     = 0x0004
	mouseRightDown  = 0x0008
	mouseRightUp    = 0x0010
	mouseMiddleDown = 0x0020
	mouseMiddleUp   = 0x0040
	mouseWheel      = 0x0800

	keyEventKeyUp   = 0x0002
	keyEventUnicode = 0x0004
)

// input mirrors the Win32 INPUT struct (40 bytes on amd64):
// DWORD type + padding + 32-byte union.
type winInput struct {
	Type uint32
	_    uint32
	U    [32]byte
}

type winMouseInput struct {
	Dx          int32
	Dy          int32
	MouseData   uint32
	DwFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
}

type winKeybdInput struct {
	WVK         uint16
	WScan       uint16
	DwFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
	_           [8]byte // pad union to 32 bytes
}

func newMouseInput(flags uint32, data uint32) winInput {
	var in winInput
	in.Type = inputMouse
	mi := (*winMouseInput)(unsafe.Pointer(&in.U[0]))
	mi.DwFlags = flags
	mi.MouseData = data
	return in
}

func newKeyInput(vk, scan uint16, flags uint32) winInput {
	var in winInput
	in.Type = inputKeyboard
	ki := (*winKeybdInput)(unsafe.Pointer(&in.U[0]))
	ki.WVK = vk
	ki.WScan = scan
	ki.DwFlags = flags
	return in
}

func sendInputs(inputs []winInput) error {
	if len(inputs) == 0 {
		return nil
	}
	n, _, err := procSendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs[0])),
		unsafe.Sizeof(winInput{}),
	)
	if n != uintptr(len(inputs)) {
		return fmt.Errorf("SendInput sent %d/%d (err=%v)", n, len(inputs), err)
	}
	return nil
}

// ---- Mouse ----

// MoveMouse moves the cursor to absolute screen pixels.
func MoveMouse(x, y int) error {
	r, _, _ := procSetCursorPos.Call(uintptr(x), uintptr(y))
	if r == 0 {
		return fmt.Errorf("SetCursorPos(%d,%d) failed", x, y)
	}
	return nil
}

// GetMousePos returns the current cursor position.
func GetMousePos() (int, int) {
	var pt struct{ X, Y int32 }
	_, _, _ = procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	return int(pt.X), int(pt.Y)
}

func mouseButtonFlags(button string) (down, up uint32, err error) {
	switch strings.ToLower(button) {
	case "", "left":
		return mouseLeftDown, mouseLeftUp, nil
	case "right":
		return mouseRightDown, mouseRightUp, nil
	case "middle":
		return mouseMiddleDown, mouseMiddleUp, nil
	default:
		return 0, 0, fmt.Errorf("unknown mouse button %q (left/right/middle)", button)
	}
}

// ClickMouse moves to (x,y) and clicks button ("left"/"right"/"middle").
// clicks=2 produces a double click.
func ClickMouse(x, y int, button string, clicks int) error {
	if clicks < 1 {
		clicks = 1
	}
	down, up, err := mouseButtonFlags(button)
	if err != nil {
		return err
	}
	if err := MoveMouse(x, y); err != nil {
		return err
	}
	time.Sleep(15 * time.Millisecond)
	for i := 0; i < clicks; i++ {
		if err := sendInputs([]winInput{newMouseInput(down, 0), newMouseInput(up, 0)}); err != nil {
			return err
		}
		if i < clicks-1 {
			time.Sleep(60 * time.Millisecond)
		}
	}
	return nil
}

// DragMouse presses left at (x1,y1), moves in steps to (x2,y2), releases.
func DragMouse(x1, y1, x2, y2 int) error {
	if err := MoveMouse(x1, y1); err != nil {
		return err
	}
	time.Sleep(20 * time.Millisecond)
	if err := sendInputs([]winInput{newMouseInput(mouseLeftDown, 0)}); err != nil {
		return err
	}
	steps := 12
	for i := 1; i <= steps; i++ {
		x := x1 + (x2-x1)*i/steps
		y := y1 + (y2-y1)*i/steps
		_ = MoveMouse(x, y)
		time.Sleep(10 * time.Millisecond)
	}
	return sendInputs([]winInput{newMouseInput(mouseLeftUp, 0)})
}

// ScrollMouse scrolls the wheel at (x,y); delta>0 scrolls up.
// One notch is 120 (WHEEL_DELTA); callers may pass notch counts
// via delta and we multiply when |delta| is small.
func ScrollMouse(x, y, delta int) error {
	if err := MoveMouse(x, y); err != nil {
		return err
	}
	data := delta
	if data > -10 && data < 10 && data != 0 {
		data = delta * 120 // treat small values as notch counts
	}
	return sendInputs([]winInput{newMouseInput(mouseWheel, uint32(int32(data)))})
}

// ---- Keyboard ----

var vkNames = map[string]uint16{
	"ctrl": 0x11, "control": 0x11, "alt": 0x12, "shift": 0x10,
	"win": 0x5B, "super": 0x5B, "menu": 0x5D, "apps": 0x5D,
	"enter": 0x0D, "return": 0x0D, "tab": 0x09, "esc": 0x1B, "escape": 0x1B,
	"space": 0x20, "backspace": 0x08, "back": 0x08, "delete": 0x2E, "del": 0x2E,
	"insert": 0x2D, "ins": 0x2D, "home": 0x24, "end": 0x23,
	"pageup": 0x21, "pgup": 0x21, "pagedown": 0x22, "pgdn": 0x22,
	"up": 0x26, "down": 0x28, "left": 0x25, "right": 0x27,
	"capslock": 0x14, "numlock": 0x90, "scrolllock": 0x91,
	"printscreen": 0x2C, "prtsc": 0x2C, "pause": 0x13, "break": 0x03,
	"f1": 0x70, "f2": 0x71, "f3": 0x72, "f4": 0x73, "f5": 0x74, "f6": 0x75,
	"f7": 0x76, "f8": 0x77, "f9": 0x78, "f10": 0x79, "f11": 0x7A, "f12": 0x7B,
}

// keyNameToVK maps a key name to a virtual-key code.
// Single letters/digits map to their VK directly (A-Z, 0-9).
func keyNameToVK(name string) (uint16, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	if vk, ok := vkNames[n]; ok {
		return vk, true
	}
	if len(n) == 1 {
		c := n[0]
		if c >= 'a' && c <= 'z' {
			return uint16(c - 'a' + 0x41), true
		}
		if c >= '0' && c <= '9' {
			return uint16(c), true
		}
	}
	return 0, false
}

// PressKeys presses keys in order and releases in reverse,
// so ["ctrl", "c"] behaves like Ctrl+C.
func PressKeys(keys []string) error {
	if len(keys) == 0 {
		return fmt.Errorf("no keys given")
	}
	vks := make([]uint16, 0, len(keys))
	for _, k := range keys {
		vk, ok := keyNameToVK(k)
		if !ok {
			return fmt.Errorf("unknown key %q", k)
		}
		vks = append(vks, vk)
	}
	var inputs []winInput
	for _, vk := range vks {
		inputs = append(inputs, newKeyInput(vk, 0, 0))
	}
	for i := len(vks) - 1; i >= 0; i-- {
		inputs = append(inputs, newKeyInput(vks[i], 0, keyEventKeyUp))
	}
	return sendInputs(inputs)
}

// TypeText types Unicode text (Chinese supported) via KEYEVENTF_UNICODE.
// Newlines are sent as Enter.
func TypeText(text string) error {
	if text == "" {
		return nil
	}
	const chunk = 64 // keep each SendInput batch bounded
	runes := []rune(text)
	for start := 0; start < len(runes); start += chunk {
		end := start + chunk
		if end > len(runes) {
			end = len(runes)
		}
		var inputs []winInput
		for _, r := range runes[start:end] {
			if r == '\n' {
				inputs = append(inputs, newKeyInput(0x0D, 0, 0), newKeyInput(0x0D, 0, keyEventKeyUp))
				continue
			}
			if r == '\r' {
				continue
			}
			inputs = append(inputs,
				newKeyInput(0, uint16(r), keyEventUnicode),
				newKeyInput(0, uint16(r), keyEventUnicode|keyEventKeyUp))
		}
		if err := sendInputs(inputs); err != nil {
			return err
		}
		time.Sleep(5 * time.Millisecond)
	}
	return nil
}
