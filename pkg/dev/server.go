package dev

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/simtxng/cook/pkg/config"
)

// Run starts the cook development environment.
func Run(ctx context.Context, cfg *config.Config, opts DevOptions) error {
	LogCook("Starting Cook Dev Environment v1.0...")

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Single reader: first signal shuts down gracefully, any later signal
	// (including one arriving during cleanup) forces immediate exit.
	shutdownCh := make(chan struct{})
	go func() {
		sig, ok := <-sigCh
		if !ok {
			return
		}
		LogCook("Caught signal %v, shutting down dev environment...", sig)
		close(shutdownCh)
		for range sigCh {
			os.Exit(130)
		}
	}()

	defer func() {
		signal.Stop(sigCh)
		close(sigCh)
	}()

	host := opts.Host
	if host == "" {
		host = "127.0.0.1"
	}
	port := opts.Port
	if port <= 0 {
		port = 9245
	}

	frontendProto := "http"
	if opts.Secure {
		frontendProto = "https"
	}
	frontendURL := fmt.Sprintf("%s://%s:%d", frontendProto, host, port)

	// Step 1: Bindings Generation (if frontend enabled)
	if cfg.Frontend.Enabled && !opts.SkipBindings {
		if err := GenerateBindings(ctx, cfg); err != nil {
			LogError("bindings", "Warning: initial binding generation failed: %v", err)
		}
	}

	// Step 2: Start Frontend Dev Server (in background)
	var frontendServer *FrontendServer
	if cfg.Frontend.Enabled && !opts.SkipFrontend {
		frontendServer = NewFrontendServer(cfg, port, opts.Secure)
		LogCook("Starting frontend dev server on %s...", frontendURL)
		if err := frontendServer.Start(ctx); err != nil {
			return fmt.Errorf("failed to start frontend server: %w", err)
		}
		defer frontendServer.Stop()
	}

	// Step 3: Concurrently Compile Backend (if not skipped)
	var supervisor *BackendSupervisor
	if !opts.SkipBackend {
		supervisor = NewBackendSupervisor(cfg)
		LogCook("Compiling backend binary...")
		if _, err := supervisor.Compile(ctx, opts.ExtraTags); err != nil {
			if frontendServer != nil {
				frontendServer.Stop()
			}
			return fmt.Errorf("initial backend compilation failed: %w", err)
		}
	}

	// Step 4: Ensure Frontend Server is Ready
	if frontendServer != nil {
		LogCook("Waiting for frontend dev server readiness...")
		if err := frontendServer.WaitReady(ctx, 15*time.Second); err != nil {
			LogCook("Warning: frontend readiness probe timed out; launching backend anyway: %v", err)
		} else {
			LogSuccess("Frontend dev server is live at %s", frontendURL)
		}
	}

	// Step 5: Start Backend Application (if not skipped)
	if supervisor != nil {
		if err := supervisor.Start(ctx, frontendURL, port, opts.Env); err != nil {
			return fmt.Errorf("failed to start backend application: %w", err)
		}
		defer supervisor.Stop()
	}

	// Step 6: Start File Watcher
	extraIgnored := []string{}
	if cfg.Frontend.Dir != "" {
		extraIgnored = append(extraIgnored, cfg.Frontend.Dir)
	}
	if cfg.Package.OutputDir != "" {
		extraIgnored = append(extraIgnored, cfg.Package.OutputDir)
	}

	watcher, err := NewWatcher(cfg.RootDir, opts.Debounce, extraIgnored)
	if err != nil {
		return fmt.Errorf("failed to initialize file watcher: %w", err)
	}
	defer watcher.Close()
	LogCook("Watching Go source files in %s (debounce: %v)", cfg.RootDir, opts.Debounce)

	// Step 7: Setup Interactive Keyboard Shortcuts & Signal Traps
	actionCh := make(chan KeyboardAction, 5)
	stopKeyboardCh := make(chan struct{})
	defer close(stopKeyboardCh)
	go StartKeyboardListener(actionCh, stopKeyboardCh)

	var appExitCh <-chan struct{}
	if supervisor != nil {
		appExitCh = supervisor.AppExited()
	}

	PrintHelp()

	// Main Dev Event Loop
	for {
		select {
		case <-ctx.Done():
			LogCook("Shutdown requested by context.")
			return nil

		case <-shutdownCh:
			return nil

		case <-appExitCh:
			LogCook("Application closed, shutting down dev environment...")
			return nil

		case changedFiles := <-watcher.Changes():
			LogCook("File change detected (%d file(s)):", len(changedFiles))
			needsBindings := false
			for _, f := range changedFiles {
				rel, err := filepath.Rel(cfg.RootDir, f)
				if err != nil {
					LogError("cook", "Could not relativize %s: %v", f, err)
					rel = f
				}
				LogCook("  -> %s", rel)
				if isServiceOrEventFile(f) {
					needsBindings = true
				}
			}

			if needsBindings && !opts.SkipBindings {
				_ = GenerateBindings(ctx, cfg)
			}

			if supervisor != nil {
				// Incremental recompilation
				LogCook("Recompiling backend...")
				if _, err := supervisor.Compile(ctx, opts.ExtraTags); err != nil {
					LogError("cook", "Compilation failed; keeping previous instance running.")
					continue
				}

				LogCook("Restarting backend...")
				if err := supervisor.Restart(ctx, frontendURL, port, opts.Env); err != nil {
					LogError("cook", "Failed to restart backend: %v", err)
				}
			}

		case action := <-actionCh:
			switch action {
			case ActionRestartBackend:
				if supervisor != nil {
					LogCook("Manual restart requested.")
					if _, err := supervisor.Compile(ctx, opts.ExtraTags); err == nil {
						_ = supervisor.Restart(ctx, frontendURL, port, opts.Env)
					}
				}
			case ActionRegenBindings:
				LogCook("Regenerating Wails bindings...")
				_ = GenerateBindings(ctx, cfg)
				if supervisor != nil {
					if _, err := supervisor.Compile(ctx, opts.ExtraTags); err == nil {
						_ = supervisor.Restart(ctx, frontendURL, port, opts.Env)
					}
				}
			case ActionRestartFrontend:
				if frontendServer != nil {
					LogCook("Restarting frontend dev server...")
					frontendServer.Stop()
					if err := frontendServer.Start(ctx); err != nil {
						LogError("cook", "Failed to restart frontend server: %v", err)
						break
					}
					if err := frontendServer.WaitReady(ctx, 15*time.Second); err != nil {
						LogError("cook", "Frontend dev server did not become ready: %v", err)
					} else {
						LogSuccess("Frontend dev server is live at %s", frontendURL)
					}
				}
			case ActionClear:
				fmt.Print("\033[H\033[2J")
				PrintHelp()
			case ActionQuit:
				LogCook("Quit requested, shutting down...")
				return nil
			}

		case err := <-watcher.Errors():
			LogError("watcher", "File watcher error: %v", err)
		}
	}
}

// GenerateBindings runs the bindings generator tool.
func GenerateBindings(ctx context.Context, cfg *config.Config) error {
	cmdStr := strings.TrimSpace(cfg.Frontend.BindingsCmd)
	if cmdStr == "" {
		cmdStr = "wails3 generate bindings -ts -i"
	}

	startTime := time.Now()
	cmd := shellCommandContext(ctx, cmdStr)
	cmd.Dir = cfg.RootDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		outStr := strings.TrimSpace(string(output))
		return fmt.Errorf("bindings generation failed: %s (%w)", outStr, err)
	}

	duration := time.Since(startTime).Round(time.Millisecond)
	LogSuccess("Generated bindings in %v", duration)
	return nil
}

func shellCommandContext(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd.exe", "/c", command)
	}
	return exec.CommandContext(ctx, "/bin/sh", "-c", command)
}

func isServiceOrEventFile(path string) bool {
	clean := strings.ToLower(filepath.ToSlash(path))
	slash := strings.LastIndex(clean, "/")
	base := clean[slash+1:]
	if !strings.HasSuffix(base, ".go") || strings.HasSuffix(base, "_test.go") {
		return false
	}
	name := strings.TrimSuffix(base, ".go")
	if strings.Contains(name, "service") ||
		strings.Contains(name, "event") ||
		strings.Contains(name, "model") {
		return true
	}
	for _, seg := range strings.Split(clean[:slash+1], "/") {
		switch seg {
		case "service", "services", "event", "events", "model", "models":
			return true
		}
	}
	return false
}
