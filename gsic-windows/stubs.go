//go:build !windows

// Stubs for non-Windows builds. They let the HTTP server, shell runner,
// file ops and skill cards compile and smoke-test on Linux; the real
// implementations live in the *_windows.go files.
package main

import "fmt"

var errNotWindows = fmt.Errorf("not supported on this platform (Windows-only feature)")

func MoveMouse(x, y int) error                             { return errNotWindows }
func GetMousePos() (int, int)                              { return 0, 0 }
func ClickMouse(x, y int, button string, clicks int) error { return errNotWindows }
func DragMouse(x1, y1, x2, y2 int) error                   { return errNotWindows }
func ScrollMouse(x, y, delta int) error                    { return errNotWindows }
func PressKeys(keys []string) error                        { return errNotWindows }
func TypeText(text string) error                           { return errNotWindows }

func ScreenSize() (int, int) { return 0, 0 }

func CaptureScreen(quality int) ([]byte, int, int, error) {
	return nil, 0, 0, errNotWindows
}

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

func ListWindows() ([]WindowInfo, error) { return nil, errNotWindows }
func ForegroundWindow() *WindowInfo      { return nil }
func ActivateWindow(hwnd uint64) error   { return errNotWindows }
func ActivateWindowByTitle(substr string) (*WindowInfo, error) {
	return nil, errNotWindows
}

func GetClipboardText() (string, error)  { return "", errNotWindows }
func SetClipboardText(text string) error { return errNotWindows }
