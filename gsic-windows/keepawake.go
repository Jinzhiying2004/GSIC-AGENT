//go:build windows

package main

import (
	"sync"
	"time"
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

// pokeInput sends a zero-delta mouse move. It counts as user input
// (resets the idle timer) without moving the cursor.
func pokeInput() {
	_ = sendInputs([]winInput{newMouseInput(mouseMove, 0)})
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
