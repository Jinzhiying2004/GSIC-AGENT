//go:build windows

package main

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const swRestore = 9

// WindowInfo describes one top-level window.
type WindowInfo struct {
	HWND    uint64 `json:"hwnd"`
	Title   string `json:"title"`
	PID     uint32 `json:"pid"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Visible bool   `json:"visible"`
}

type winRect struct {
	Left, Top, Right, Bottom int32
}

func windowTitle(hwnd uintptr) string {
	buf := make([]uint16, 512)
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// ListWindows enumerates visible top-level windows with titles.
func ListWindows() ([]WindowInfo, error) {
	var out []WindowInfo
	cb := windows.NewCallback(func(hwnd, lParam uintptr) uintptr {
		vis, _, _ := procIsWindowVisible.Call(hwnd)
		if vis == 0 {
			return 1 // continue
		}
		title := windowTitle(hwnd)
		if strings.TrimSpace(title) == "" {
			return 1
		}
		var pid uint32
		_, _, _ = procGetWindowThreadProcess.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		var rc winRect
		_, _, _ = procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
		out = append(out, WindowInfo{
			HWND:    uint64(hwnd),
			Title:   title,
			PID:     pid,
			X:       int(rc.Left),
			Y:       int(rc.Top),
			Width:   int(rc.Right - rc.Left),
			Height:  int(rc.Bottom - rc.Top),
			Visible: true,
		})
		return 1 // continue
	})
	_, _, _ = procEnumWindows.Call(cb, 0)
	return out, nil
}

// ForegroundWindow returns the currently focused window, if any.
func ForegroundWindow() *WindowInfo {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return nil
	}
	var pid uint32
	_, _, _ = procGetWindowThreadProcess.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	var rc winRect
	_, _, _ = procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
	return &WindowInfo{
		HWND:    uint64(hwnd),
		Title:   windowTitle(hwnd),
		PID:     pid,
		X:       int(rc.Left),
		Y:       int(rc.Top),
		Width:   int(rc.Right - rc.Left),
		Height:  int(rc.Bottom - rc.Top),
		Visible: true,
	}
}

// ActivateWindow brings a window to the front (restores if minimized).
func ActivateWindow(hwnd uint64) error {
	h := uintptr(hwnd)
	iconic, _, _ := procIsIconic.Call(h)
	if iconic != 0 {
		_, _, _ = procShowWindow.Call(h, swRestore)
	}
	r, _, _ := procSetForegroundWindow.Call(h)
	if r == 0 {
		return fmt.Errorf("SetForegroundWindow(%d) failed (window may refuse focus)", hwnd)
	}
	return nil
}

// ActivateWindowByTitle activates the first visible window whose title
// contains substr (case-insensitive).
func ActivateWindowByTitle(substr string) (*WindowInfo, error) {
	wins, err := ListWindows()
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(substr)
	for _, w := range wins {
		if strings.Contains(strings.ToLower(w.Title), needle) {
			if err := ActivateWindow(w.HWND); err != nil {
				return nil, err
			}
			return &w, nil
		}
	}
	return nil, fmt.Errorf("no visible window title contains %q", substr)
}
