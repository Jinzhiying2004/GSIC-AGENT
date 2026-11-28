//go:build windows

package main

import (
	"crypto/tls"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// Keep-awake: while GSIC is running tasks, the PC must not sleep;
// when GSIC has been idle for 10 minutes, the PC may sleep again
// (the cloud desktop is pay-per-use, so idle sleep is wanted).
//
// The Wuying cloud platform suspends the VM from the hypervisor after
// ~10 minutes of *platform* idle, and Windows-side signals do not stop
// it. What the platform actually counts is real VM activity (network
// I/O + CPU work), proven two ways:
//   - Muse polling GSIC every few minutes kept the VM awake for over
//     an hour (inbound HTTP + screenshot CPU work).
//   - Tencent WorkBuddy running a background task kept the VM awake
//     overnight (outbound network + sustained CPU).
// Synthetic input does NOT reset it: SetThreadExecutionState and
// SendInput mouse pokes (v4.43/v4.44/v4.45) all failed, the VM still
// slept 10 minutes after wake.
//
// So the watchdog no longer relies on input pokes. While a task
// session is active it fires a real activity pulse every minute:
//   - a screen capture (GDI + CPU work, same as a screenshot request),
//   - a lightweight HTTPS GET (outbound network I/O),
//   - plus the old input poke and execution-state lock as secondary
//     signals (they still help Windows-level idle).
// The pulse is best-effort: any step may fail (screen capture is
// flaky on this VM) and the rest still runs.
//
// A task session is active while a task/shell is actually executing
// OR within 10 minutes after the last task/shell call, so long
// background work triggered by a short task (e.g. an IDE downloading)
// stays covered as long as Muse keeps issuing calls, and the VM
// sleeps ~10 minutes after Muse stops.

// Execution-state flags (kernel32 SetThreadExecutionState).
const (
	esContinuous       = 0x80000000
	esSystemRequired   = 0x00000001
	esAwayModeRequired = 0x00000040
)

const keepAwakeIdleTimeout = 10 * time.Minute
const keepAwakePulseInterval = 60 * time.Second

// Network endpoint for the pulse's outbound activity. microsoft.com is
// reachable from the cloud desktop (verified 2026-11-28); the request
// only fetches headers, it never downloads the page.
const keepAwakePulseURL = "https://www.microsoft.com/"

var (
	keepAwakeMu   sync.Mutex
	keepAwakeOn   bool
	keepAwakeLast time.Time
	// tasksRunning counts /task and /shell handlers currently executing.
	tasksRunning int32
	// pulseCount and pulseLast record activity pulses (for /status).
	pulseCount int64
	pulseLast  time.Time
	pulseMu    sync.Mutex
)

var pulseHTTPClient = &http.Client{
	Timeout: 5 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	},
}

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
// last-input time. On its own it does not reset the cloud platform's
// idle timer (v4.43/v4.44/v4.45), so it is only a secondary signal now;
// the activity pulse below does the real work.
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

// activityPulse generates real VM activity so the cloud platform's idle
// timer resets: a screen capture (GDI + CPU) and a lightweight HTTPS
// GET (outbound network). Best-effort: failures are logged, never fatal.
func activityPulse() {
	// Screen capture at the lowest JPEG quality, bytes discarded. This
	// is the same work as answering a /screenshot request, which is
	// proven to keep the VM awake when polled.
	if _, _, _, err := CaptureScreen(1); err != nil {
		Logf("keep-awake pulse: screen capture failed (%v)", err)
	}
	// Outbound network I/O: fetch headers only, never the body.
	resp, err := pulseHTTPClient.Get(keepAwakePulseURL)
	if err != nil {
		Logf("keep-awake pulse: network GET failed (%v)", err)
	} else {
		resp.Body.Close()
	}
	// Secondary signal: synthetic input + execution state.
	pokeInput()

	pulseMu.Lock()
	pulseCount++
	pulseLast = time.Now()
	pulseMu.Unlock()
}

// NoteTaskActivity records that GSIC just started work (a /task or
// /shell call). It acquires the wake lock if needed and (re)starts the
// idle watchdog. Pair with NoteTaskDone (defer) so a long-running task
// keeps the session active for its whole duration, not just 10 minutes
// after the call arrived.
func NoteTaskActivity() {
	atomic.AddInt32(&tasksRunning, 1)
	keepAwakeMu.Lock()
	keepAwakeLast = time.Now()
	if !keepAwakeOn {
		keepAwakeOn = true
		keepAwakeAcquire()
		go keepAwakeWatchdog()
	}
	keepAwakeMu.Unlock()
}

// NoteTaskDone records that a /task or /shell call finished.
func NoteTaskDone() {
	atomic.AddInt32(&tasksRunning, -1)
}

func keepAwakeWatchdog() {
	t := time.NewTicker(keepAwakePulseInterval)
	defer t.Stop()
	for range t.C {
		keepAwakeMu.Lock()
		running := atomic.LoadInt32(&tasksRunning) > 0
		idle := time.Since(keepAwakeLast)
		if !running && idle >= keepAwakeIdleTimeout {
			// No task running and idle for 10 min: allow sleep again.
			keepAwakeOn = false
			keepAwakeRelease()
			keepAwakeMu.Unlock()
			Logf("keep-awake: idle %v, lock released", idle.Round(time.Second))
			return
		}
		keepAwakeMu.Unlock()
		// Still in an active work session: fire the activity pulse so
		// the platform idle timer resets, and keep the execution
		// state held.
		activityPulse()
	}
}

// KeepAwakeActive reports whether the wake lock is currently held.
func KeepAwakeActive() bool {
	keepAwakeMu.Lock()
	defer keepAwakeMu.Unlock()
	return keepAwakeOn
}

// KeepAwakePulse returns how many activity pulses have fired and when
// the last one fired (zero time if none yet).
func KeepAwakePulse() (count int64, last time.Time) {
	pulseMu.Lock()
	defer pulseMu.Unlock()
	return pulseCount, pulseLast
}
