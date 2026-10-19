package dev

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcher_BasicDebounce(t *testing.T) {
	tempDir := t.TempDir()

	watcher, err := NewWatcher(tempDir, 50*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("NewWatcher failed: %v", err)
	}
	defer watcher.Close()

	testFile := filepath.Join(tempDir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	select {
	case changes := <-watcher.Changes():
		if len(changes) == 0 {
			t.Fatalf("expected at least 1 changed file")
		}
		if changes[0] != testFile {
			t.Errorf("expected %s, got %s", testFile, changes[0])
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timeout waiting for watcher change event")
	}
}

func TestWatcher_IgnoreNonGoFiles(t *testing.T) {
	tempDir := t.TempDir()

	watcher, err := NewWatcher(tempDir, 50*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("NewWatcher failed: %v", err)
	}
	defer watcher.Close()

	// Write non-watchable files
	_ = os.WriteFile(filepath.Join(tempDir, "test.txt"), []byte("text"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "main_test.go"), []byte("package main"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, ".hidden.go"), []byte("package main"), 0644)

	select {
	case changes := <-watcher.Changes():
		t.Fatalf("unexpected change event for ignored files: %v", changes)
	case <-time.After(150 * time.Millisecond):
		// Success: no event fired
	}
}

func TestWatcher_CloseIdempotent(t *testing.T) {
	tempDir := t.TempDir()

	watcher, err := NewWatcher(tempDir, 50*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("NewWatcher failed: %v", err)
	}

	if err := watcher.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}
	if err := watcher.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
}

func TestWatcher_NonexistentRootSurfacesError(t *testing.T) {
	if _, err := NewWatcher(filepath.Join(t.TempDir(), "does-not-exist"), 50*time.Millisecond, nil); err == nil {
		t.Fatal("expected NewWatcher to surface walk error for missing root")
	}
}

func TestWatcher_IgnoreDirectory(t *testing.T) {
	tempDir := t.TempDir()

	ignoredSubDir := filepath.Join(tempDir, "node_modules")
	if err := os.MkdirAll(ignoredSubDir, 0755); err != nil {
		t.Fatalf("failed to create ignored dir: %v", err)
	}

	watcher, err := NewWatcher(tempDir, 50*time.Millisecond, []string{"node_modules"})
	if err != nil {
		t.Fatalf("NewWatcher failed: %v", err)
	}
	defer watcher.Close()

	_ = os.WriteFile(filepath.Join(ignoredSubDir, "foo.go"), []byte("package foo"), 0644)

	select {
	case changes := <-watcher.Changes():
		t.Fatalf("unexpected change event for file in ignored directory: %v", changes)
	case <-time.After(150 * time.Millisecond):
		// Success
	}
}
