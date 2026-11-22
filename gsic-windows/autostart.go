//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

// autostartRegKey is the Run key for current-user autostart.
const autostartRegKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`

// isAutostartEnabled reports whether GSIC is registered for autostart.
func isAutostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, autostartRegKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(AppName)
	return err == nil
}

// setAutostart enables or disables autostart via the registry Run key.
func setAutostart(enable bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, autostartRegKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !enable {
		// DeleteValue returns ErrNotExist if absent; treat as success.
		_ = k.DeleteValue(AppName)
		return nil
	}
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue(AppName, `"`+exePath+`"`)
}
