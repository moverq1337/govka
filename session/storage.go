package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Memory keeps the session in process memory. Useful for tests and for
// programs that persist the session themselves.
type Memory struct {
	mu   sync.Mutex
	data []byte
}

// LoadSession implements Storage.
func (m *Memory) LoadSession(context.Context) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		return nil, ErrNotFound
	}
	return append([]byte(nil), m.data...), nil
}

// StoreSession implements Storage.
func (m *Memory) StoreSession(_ context.Context, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = append([]byte(nil), data...)
	return nil
}

// File keeps the session in a JSON file with 0600 permissions. Writes are
// atomic: the data goes to a temporary file that is renamed over Path.
type File struct {
	Path string
	mu   sync.Mutex
}

// NewFile returns a File storage for path.
func NewFile(path string) *File { return &File{Path: path} }

// LoadSession implements Storage.
func (f *File) LoadSession(context.Context) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("session: read %s: %w", f.Path, err)
	}
	return data, nil
}

// StoreSession implements Storage.
func (f *File) StoreSession(_ context.Context, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	dir := filepath.Dir(f.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("session: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".session-*.tmp")
	if err != nil {
		return fmt.Errorf("session: temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("session: chmod: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("session: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("session: close: %w", err)
	}
	if err := os.Rename(tmpName, f.Path); err != nil {
		cleanup()
		return fmt.Errorf("session: rename: %w", err)
	}
	return nil
}
