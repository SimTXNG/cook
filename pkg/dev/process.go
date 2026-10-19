package dev

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// startProcess starts cmd and streams both output pipes until the process exits.
// onStarted runs after a successful start and before the streamers begin, so
// callers can publish the process before a fast exit is observed. onExit runs
// after Wait completes.
func startProcess(cmd *exec.Cmd, cancel context.CancelFunc, logf func(string, ...any), errPrefix string, filter func(string) (string, bool), onStarted func(chan struct{}), onExit func()) error {
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("failed to start process: %w", err)
	}

	done := make(chan struct{})
	if onStarted != nil {
		onStarted(done)
	}

	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		streamLog(stdoutPipe, logf, errPrefix, filter)
	}()
	go func() {
		defer readers.Done()
		streamLog(stderrPipe, logf, errPrefix, filter)
	}()

	// Single Wait per process: let the pipe readers finish first, then reap.
	go func() {
		readers.Wait()
		_ = cmd.Wait()
		if onExit != nil {
			onExit()
		}
		close(done)
	}()

	return nil
}

func streamLog(r io.Reader, logf func(string, ...any), errPrefix string, filter func(string) (string, bool)) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		text := scanner.Text()
		if strings.TrimSpace(text) == "" {
			continue
		}
		if filter != nil {
			var ok bool
			text, ok = filter(text)
			if !ok {
				continue
			}
		}
		logf("%s", text)
	}
	if err := scanner.Err(); err != nil {
		LogError(errPrefix, "Log stream read error: %v", err)
	}
}

func stopProcess(cmd *exec.Cmd, cancel context.CancelFunc, done <-chan struct{}) {
	if cmd == nil || cmd.Process == nil {
		if cancel != nil {
			cancel()
		}
		return
	}

	if cancel != nil {
		cancel()
	}

	killProcessTree(cmd)

	if done == nil {
		return
	}

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
		}
	}
}
