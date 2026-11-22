package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ReadFileBytes reads a whole file (size-capped to avoid OOM on huge files).
func ReadFileBytes(path string) ([]byte, error) {
	const maxSize = 32 << 20 // 32 MiB
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.Size() > maxSize {
		return nil, fmt.Errorf("file too large: %d bytes (limit %d)", fi.Size(), maxSize)
	}
	return os.ReadFile(path)
}

// ReadFileBase64 reads a file and returns base64 text.
func ReadFileBase64(path string) (string, error) {
	b, err := ReadFileBytes(path)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// WriteFileBytes writes data, creating parent directories as needed.
func WriteFileBytes(path string, data []byte) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, 0o644)
}

// DirEntry is one row of a directory listing.
type DirEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

// ListDir lists a directory (names sorted, dirs first).
func ListDir(path string) ([]DirEntry, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]DirEntry, 0, len(entries))
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			continue
		}
		mt := ""
		if !fi.ModTime().IsZero() {
			mt = fi.ModTime().Format(time.RFC3339)
		}
		out = append(out, DirEntry{
			Name:    e.Name(),
			Path:    filepath.Join(path, e.Name()),
			IsDir:   e.IsDir(),
			Size:    fi.Size(),
			ModTime: mt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
