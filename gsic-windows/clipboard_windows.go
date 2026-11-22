//go:build windows

package main

import (
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

// openClipboardRetry retries because another app often holds the
// clipboard briefly. Caller must CloseClipboard on success.
func openClipboardRetry() error {
	var lastErr error
	for i := 0; i < 10; i++ {
		r, _, _ := procOpenClipboard.Call(0)
		if r != 0 {
			return nil
		}
		lastErr = fmt.Errorf("OpenClipboard failed (attempt %d)", i+1)
		time.Sleep(50 * time.Millisecond)
	}
	return lastErr
}

// GetClipboardText reads Unicode text from the clipboard.
func GetClipboardText() (string, error) {
	if err := openClipboardRetry(); err != nil {
		return "", err
	}
	defer procCloseClipboard.Call()

	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", fmt.Errorf("clipboard has no Unicode text")
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return "", fmt.Errorf("GlobalLock failed")
	}
	defer procGlobalUnlock.Call(h)
	return windows.UTF16PtrToString((*uint16)(unsafe.Pointer(ptr))), nil
}

// SetClipboardText replaces the clipboard contents with text.
func SetClipboardText(text string) error {
	if err := openClipboardRetry(); err != nil {
		return err
	}
	defer procCloseClipboard.Call()

	utf16, err := windows.UTF16FromString(text)
	if err != nil {
		return err
	}
	size := uintptr(len(utf16) * 2)
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, size)
	if h == 0 {
		return fmt.Errorf("GlobalAlloc failed")
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		_, _, _ = procGlobalFree.Call(h)
		return fmt.Errorf("GlobalLock failed")
	}
	dst := (*[1 << 30]uint16)(unsafe.Pointer(ptr))
	copy(dst[:len(utf16)], utf16)
	_, _, _ = procGlobalUnlock.Call(h)

	_, _, _ = procEmptyClipboard.Call()
	r, _, _ := procSetClipboardData.Call(cfUnicodeText, h)
	if r == 0 {
		_, _, _ = procGlobalFree.Call(h)
		return fmt.Errorf("SetClipboardData failed")
	}
	// Ownership of h passes to the clipboard; do not free it.
	return nil
}
