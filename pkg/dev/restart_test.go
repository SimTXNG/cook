package dev

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/simtxng/cook/pkg/config"
)

func requireUnix(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("restart/stop tests require a unix shell and POSIX signals")
	}
}

func writeTestScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0755); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}
	return path
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func waitFor(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", desc)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func countLines(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return len(strings.Split(strings.TrimSpace(string(data)), "\n"))
}

func backendPID(b *BackendSupervisor) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cmd == nil || b.cmd.Process == nil {
		return 0
	}
	return b.cmd.Process.Pid
}

func frontendPID(f *FrontendServer) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cmd == nil || f.cmd.Process == nil {
		return 0
	}
	return f.cmd.Process.Pid
}

func TestBackendSupervisorRestartAndStop(t *testing.T) {
	requireUnix(t)

	dir := t.TempDir()
	script := writeTestScript(t, dir, "app.sh", `#!/bin/sh
printf '%s\n' "$$" >> "$0.starts"
exec sleep 300
`)
	starts := script + ".starts"

	cfg := &config.Config{
		RootDir: dir,
		Package: config.PackageConfig{Name: "app", OutputDir: "bin"},
		Binaries: []config.BinaryConfig{
			{Name: "app", GUI: true},
		},
	}

	s := NewBackendSupervisor(cfg)
	s.binaryPath = script

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx, "http://127.0.0.1:9245", 9245, nil); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	firstPID := backendPID(s)
	if !processAlive(firstPID) {
		t.Fatalf("first backend process %d is not running", firstPID)
	}
	waitFor(t, time.Second, "first process to record its start", func() bool {
		return countLines(starts) == 1
	})

	if err := s.Restart(ctx, "http://127.0.0.1:9245", 9245, nil); err != nil {
		t.Fatalf("Restart failed: %v", err)
	}

	secondPID := backendPID(s)
	if secondPID == firstPID {
		t.Fatalf("Restart kept the same PID %d", firstPID)
	}
	if !processAlive(secondPID) {
		t.Fatalf("replacement backend process %d is not running", secondPID)
	}
	waitFor(t, 2*time.Second, "old backend process to terminate", func() bool {
		return !processAlive(firstPID)
	})
	waitFor(t, time.Second, "exactly two backend starts", func() bool {
		return countLines(starts) == 2
	})

	s.Stop()
	waitFor(t, 2*time.Second, "backend process to terminate", func() bool {
		return !processAlive(secondPID)
	})
	if backendPID(s) != 0 {
		t.Fatal("Stop did not clear the backend command")
	}

	s.Stop()
	if processAlive(secondPID) {
		t.Fatalf("second Stop left process %d running", secondPID)
	}
}

func TestFrontendServerStopIsIdempotent(t *testing.T) {
	requireUnix(t)

	dir := t.TempDir()
	frontendDir := filepath.Join(dir, "frontend")
	if err := os.MkdirAll(filepath.Join(frontendDir, "node_modules"), 0755); err != nil {
		t.Fatalf("failed to create fake frontend dir: %v", err)
	}

	cfg := &config.Config{
		RootDir: dir,
		Frontend: config.FrontendConfig{
			Dir:    frontendDir,
			DevCmd: "exec sleep 300",
		},
	}

	f := NewFrontendServer(cfg, 9245)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := f.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	pid := frontendPID(f)
	if !processAlive(pid) {
		t.Fatalf("frontend process %d is not running", pid)
	}

	f.Stop()
	waitFor(t, 2*time.Second, "frontend process to terminate", func() bool {
		return !processAlive(pid)
	})
	if frontendPID(f) != 0 {
		t.Fatal("Stop did not clear the frontend command")
	}

	f.Stop()
	if processAlive(pid) {
		t.Fatalf("second Stop left process %d running", pid)
	}
}
