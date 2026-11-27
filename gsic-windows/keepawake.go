//go:build windows

package main

import (
	"sync"
	"time"
	"unsafe"
)

// Keep-awake: while GSIC is running tasks, the PC must not sleep;
// when GSIC has been idle for 10 minutes, the PC may sleep again
// (the cloud desktop is pay-per-use, so idle sleep is wanted).
//
// Two mechanisms, because the cloud platform and Windows watch different
// signals:
//   - SetThreadExecutionState(ES_SYSTEM_REQUIRED | ES_AWAYMODE_REQUIRED)
//     stops Windows itself from sleeping.
//   - A zero-delta mouse move every minute updates the system's last-input
//     time, so the cloud platform's input-based idle timer also resets.
//     It does not move the cursor, so it cannot disturb a task's clicks.
//
// The lock is held from the first task/shell call and released 10 minutes
// after the last one, matching the user's rule exactly.

// Execution-state flags (kernel32 SetThreadExecutionState).
const (
	esContinuous       = 0x80000000
	esSystemRequired   = 0x00000001
	esAwayModeRequired = 0x00000040
)

const keepAwakeIdleTimeout = 10 * time.Minute

var (
	keepAwakeMu   sync.Mutex
	keepAwakeOn   bool
	keepAwakeLast time.Time
)

func keepAwakeAcquire() {
	_, _, _ = procSetThreadExecutionState.Call(
		uintptr(esContinuous | esSystemRequired | esAwayModeRequired))
}

func keepAwakeRelease() {
	_, _, _ = procSetThreadExecutionState.Call(uintptr(esContinuous))
}

// pokeInput moves the cursor 1 pixel and back via SendInput, so it is a
// real input event. It must be SendInput, not SetCursorPos:
// SetCursorPos only moves the cursor and does NOT update the system's
// last-input time, so the cloud platform's idle timer never resets
// (v4.44 used SetCursorPos and the VM still slept at ~08:03).
// The cursor returns to its original spot, so a task's clicks are not
// disturbed.
func pokeInput() {
	_ = sendInputs([]winInput{
		newMouseInputRel(mouseMove, 1, 0),
		newMouseInputRel(mouseMove, -1, 0),
	})
}

// newMouseInputRel builds a relative mouse-move input (dx, dy in pixels).
func newMouseInputRel(flags uint32, dx, dy int32) winInput {
	in := newMouseInput(flags, 0)
	mi := (*winMouseInput)(unsafe.Pointer(&in.U[0]))
	mi.Dx = dx
	mi.Dy = dy
	return in
}

// NoteTaskActivity records that GSIC just did work (a /task or /shell call).
// It acquires the wake lock if needed and (re)starts the idle watchdog.
func NoteTaskActivity() {
	keepAwakeMu.Lock()
	keepAwakeLast = time.Now()
	if !keepAwakeOn {
		keepAwakeOn = true
		keepAwakeAcquire()
		go keepAwakeWatchdog()
	}
	keepAwakeMu.Unlock()
}

func keepAwakeWatchdog() {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for range t.C {
		keepAwakeMu.Lock()
		idle := time.Since(keepAwakeLast)
		if idle >= keepAwakeIdleTimeout {
			// Idle for 10 min: allow sleep again.
			keepAwakeOn = false
			keepAwakeRelease()
			keepAwakeMu.Unlock()
			return
		}
		keepAwakeMu.Unlock()
		// Still in an active work session: poke input so the platform
		// idle timer resets, and keep the execution state held.
		pokeInput()
	}
}

// KeepAwakeActive reports whether the wake lock is currently held.
func KeepAwakeActive() bool {
	keepAwakeMu.Lock()
	defer keepAwakeMu.Unlock()
	return keepAwakeOn
}
