package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"
)

// ShellResult is the outcome of one command execution.
type ShellResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

// RunCommand runs command through cmd.exe / powershell on Windows
// (or /bin/sh on other platforms, used by the Linux smoke-test build).
// shell: "cmd" (default), "powershell"/"pwsh", "direct" (no shell wrapper,
// command is split on whitespace - no quoting support).
// timeoutSec <= 0 means the default 60s. Output is capped at 1 MiB per stream.
func RunCommand(command, shell string, timeoutSec int) (ShellResult, error) {
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	switch {
	case runtime.GOOS == "windows" && (shell == "" || shell == "cmd"):
		cmd = exec.CommandContext(ctx, "cmd.exe", "/C", command)
	case runtime.GOOS == "windows" && (shell == "powershell" || shell == "pwsh"):
		cmd = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command)
	case runtime.GOOS == "windows" && shell == "direct":
		parts := splitCommand(command)
		if len(parts) == 0 {
			return ShellResult{}, fmt.Errorf("empty command")
		}
		cmd = exec.CommandContext(ctx, parts[0], parts[1:]...)
	case shell == "direct":
		parts := splitCommand(command)
		if len(parts) == 0 {
			return ShellResult{}, fmt.Errorf("empty command")
		}
		cmd = exec.CommandContext(ctx, parts[0], parts[1:]...)
	default:
		// Non-Windows fallback (smoke-test builds only).
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", command)
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &outBuf, limit: 1 << 20}
	cmd.Stderr = &limitedWriter{w: &errBuf, limit: 1 << 20}
	err := cmd.Run()

	res := ShellResult{Stdout: outBuf.String(), Stderr: errBuf.String(), ExitCode: 0}
	if ctx.Err() == context.DeadlineExceeded {
		return res, fmt.Errorf("command timed out after %ds", timeoutSec)
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
			return res, nil // non-zero exit is a result, not a transport error
		}
		return res, fmt.Errorf("run command: %w", err)
	}
	return res, nil
}

// Launch starts a program without waiting for it (e.g. notepad.exe).
func Launch(path string, args []string) (int, error) {
	cmd := exec.Command(path, args...)
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	// Detach: do not Wait; the child keeps running after we return.
	go func() { _ = cmd.Wait() }()
	return cmd.Process.Pid, nil
}

func splitCommand(s string) []string {
	var parts []string
	var cur []rune
	var quote rune
	for _, r := range s {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur = append(cur, r)
		case r == '"' || r == '\'':
			quote = r
		case r == ' ' || r == '\t':
			if len(cur) > 0 {
				parts = append(parts, string(cur))
				cur = nil
			}
		default:
			cur = append(cur, r)
		}
	}
	if len(cur) > 0 {
		parts = append(parts, string(cur))
	}
	return parts
}

type limitedWriter struct {
	w     *bytes.Buffer
	limit int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.w.Len() >= l.limit {
		return len(p), nil // drop the rest, keep the command running
	}
	room := l.limit - l.w.Len()
	if len(p) > room {
		p = p[:room]
	}
	_, _ = l.w.Write(p)
	return len(p), nil
}
