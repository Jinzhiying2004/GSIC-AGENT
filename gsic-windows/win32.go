//go:build windows

package main

import (
	"golang.org/x/sys/windows"
)

var (
	modUser32   = windows.NewLazySystemDLL("user32.dll")
	modGdi32    = windows.NewLazySystemDLL("gdi32.dll")
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procSetCursorPos           = modUser32.NewProc("SetCursorPos")
	procGetCursorPos           = modUser32.NewProc("GetCursorPos")
	procSendInput              = modUser32.NewProc("SendInput")
	procGetSystemMetrics       = modUser32.NewProc("GetSystemMetrics")
	procGetDC                  = modUser32.NewProc("GetDC")
	procReleaseDC              = modUser32.NewProc("ReleaseDC")
	procEnumWindows            = modUser32.NewProc("EnumWindows")
	procIsWindowVisible        = modUser32.NewProc("IsWindowVisible")
	procIsIconic               = modUser32.NewProc("IsIconic")
	procGetWindowTextW         = modUser32.NewProc("GetWindowTextW")
	procGetWindowRect          = modUser32.NewProc("GetWindowRect")
	procGetWindowThreadProcess = modUser32.NewProc("GetWindowThreadProcessId")
	procSetForegroundWindow    = modUser32.NewProc("SetForegroundWindow")
	procShowWindow             = modUser32.NewProc("ShowWindow")
	procGetForegroundWindow    = modUser32.NewProc("GetForegroundWindow")
	procOpenClipboard          = modUser32.NewProc("OpenClipboard")
	procCloseClipboard         = modUser32.NewProc("CloseClipboard")
	procEmptyClipboard         = modUser32.NewProc("EmptyClipboard")
	procGetClipboardData       = modUser32.NewProc("GetClipboardData")
	procSetClipboardData       = modUser32.NewProc("SetClipboardData")

	procCreateCompatibleDC     = modGdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = modGdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = modGdi32.NewProc("SelectObject")
	procBitBlt                 = modGdi32.NewProc("BitBlt")
	procGetDIBits              = modGdi32.NewProc("GetDIBits")
	procDeleteObject           = modGdi32.NewProc("DeleteObject")
	procDeleteDC               = modGdi32.NewProc("DeleteDC")

	procGlobalAlloc  = modKernel32.NewProc("GlobalAlloc")
	procGlobalLock   = modKernel32.NewProc("GlobalLock")
	procGlobalUnlock = modKernel32.NewProc("GlobalUnlock")
	procGlobalFree   = modKernel32.NewProc("GlobalFree")
	procSleep        = modKernel32.NewProc("Sleep")
)

func winSleep(ms uint32) {
	_, _, _ = procSleep.Call(uintptr(ms))
}
