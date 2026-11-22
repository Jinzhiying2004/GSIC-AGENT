package main

import (
	"fmt"
	"log"
	"os"
	"sync"
)

var (
	logger   *log.Logger
	logFile  *os.File
	logMutex sync.Mutex
	logHook  func(string) // optional GUI log feed (set by main)
)

// SetLogHook registers a callback invoked with each log line
// (used by the GUI dashboard to show live logs).
func SetLogHook(fn func(string)) {
	logMutex.Lock()
	defer logMutex.Unlock()
	logHook = fn
}

// InitLogger logs to stdout and, unless disabled, appends to logPath.
func InitLogger(logPath string, noFile bool) error {
	if noFile {
		logger = log.New(os.Stdout, "", log.LstdFlags)
		return nil
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	logFile = f
	logger = log.New(&teeWriter{file: f}, "", log.LstdFlags)
	return nil
}

// CloseLogger closes the log file, if any.
func CloseLogger() {
	logMutex.Lock()
	defer logMutex.Unlock()
	if logFile != nil {
		_ = logFile.Close()
		logFile = nil
	}
}

// Logf writes one log line (console + file + GUI hook).
func Logf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	logMutex.Lock()
	hook := logHook
	if logger == nil {
		logger = log.New(os.Stdout, "", log.LstdFlags)
	}
	logger.Print(msg)
	logMutex.Unlock()
	if hook != nil {
		hook(msg)
	}
}

type teeWriter struct{ file *os.File }

func (w *teeWriter) Write(p []byte) (int, error) {
	n, _ := os.Stdout.Write(p)
	_, _ = w.file.Write(p)
	return n, nil
}

var _ = fmt.Sprintf // keep fmt imported for future log helpers
