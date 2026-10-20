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
	"sync"
	"syscall"
	"time"

	"github.com/simtxng/cook/pkg/buildcache"
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

	// Start the watcher before the initial compile so edits made during startup
	// are not lost.
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

	// Kick off bindings, the frontend dev server, and the backend compile
	// concurrently. They are independent and dominate cold startup.
	setupCtx, cancelSetup := context.WithCancel(ctx)
	defer cancelSetup()

	var (
		wg             sync.WaitGroup
		frontendServer *FrontendServer
		frontendErr    error
		supervisor     *BackendSupervisor
		compileErr     error
		bindingsErr    error
	)

	if cfg.Frontend.Enabled && !opts.SkipBindings {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bindingsErr = GenerateBindingsWithOptions(setupCtx, cfg, opts.SkipUnchanged, opts.Force)
		}()
	}

	if cfg.Frontend.Enabled && !opts.SkipFrontend {
		frontendServer = NewFrontendServer(cfg, port, opts.Secure)
		LogCook("Starting frontend dev server on %s...", frontendURL)
		wg.Add(1)
		go func() {
			defer wg.Done()
			frontendErr = frontendServer.Start(setupCtx)
		}()
	}

	if !opts.SkipBackend {
		supervisor = NewBackendSupervisor(cfg)
		LogCook("Compiling backend binary...")
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, compileErr = supervisor.CompileWithOptions(setupCtx, CompileOptions{
				ExtraTags:     opts.ExtraTags,
				GCFlags:       opts.GCFlags,
				SkipUnchanged: opts.SkipUnchanged,
				Force:         opts.Force,
			})
		}()
	}

	wg.Wait()

	if frontendErr != nil {
		cancelSetup()
		if supervisor != nil {
			supervisor.Stop()
		}
		return fmt.Errorf("failed to start frontend server: %w", frontendErr)
	}
	if compileErr != nil {
		cancelSetup()
		if frontendServer != nil {
			frontendServer.Stop()
		}
		return fmt.Errorf("initial backend compilation failed: %w", compileErr)
	}
	if bindingsErr != nil {
		LogError("bindings", "Warning: initial binding generation failed: %v", bindingsErr)
	}
	if frontendServer != nil {
		defer frontendServer.Stop()
	}

	// Ensure the frontend server is ready before launching the app.
	if frontendServer != nil {
		LogCook("Waiting for frontend dev server readiness...")
		if err := frontendServer.WaitReady(ctx, 15*time.Second); err != nil {
			LogCook("Warning: frontend readiness probe timed out; launching backend anyway: %v", err)
		} else {
			LogSuccess("Frontend dev server is live at %s", frontendURL)
		}
	}

	// Start the backend application.
	if supervisor != nil {
		if err := supervisor.Start(ctx, frontendURL, port, opts.Env); err != nil {
			return fmt.Errorf("failed to start backend application: %w", err)
		}
		defer supervisor.Stop()
	}

	// Setup interactive keyboard shortcuts and signal traps.
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

			// Regenerate bindings and recompile concurrently; both only read the
			// Go sources and share the Go build cache safely.
			var (
				reloadWG      sync.WaitGroup
				reloadBindErr error
				reloadCompErr error
			)
			if needsBindings && !opts.SkipBindings {
				reloadWG.Add(1)
				go func() {
					defer reloadWG.Done()
					reloadBindErr = GenerateBindingsWithOptions(ctx, cfg, opts.SkipUnchanged, opts.Force)
				}()
			}
			if supervisor != nil {
				reloadWG.Add(1)
				go func() {
					defer reloadWG.Done()
					_, reloadCompErr = supervisor.CompileWithOptions(ctx, CompileOptions{
						ExtraTags:     opts.ExtraTags,
						GCFlags:       opts.GCFlags,
						SkipUnchanged: opts.SkipUnchanged,
						Force:         opts.Force,
					})
				}()
			}
			reloadWG.Wait()

			if reloadBindErr != nil {
				LogError("bindings", "Binding regeneration failed: %v", reloadBindErr)
			}
			if supervisor != nil {
				if reloadCompErr != nil {
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
					if _, err := supervisor.CompileWithOptions(ctx, CompileOptions{
						ExtraTags:     opts.ExtraTags,
						GCFlags:       opts.GCFlags,
						SkipUnchanged: opts.SkipUnchanged,
						Force:         opts.Force,
					}); err == nil {
						_ = supervisor.Restart(ctx, frontendURL, port, opts.Env)
					}
				}
			case ActionRegenBindings:
				LogCook("Regenerating Wails bindings...")
				_ = GenerateBindingsWithOptions(ctx, cfg, opts.SkipUnchanged, opts.Force)
				if supervisor != nil {
					if _, err := supervisor.CompileWithOptions(ctx, CompileOptions{
						ExtraTags:     opts.ExtraTags,
						GCFlags:       opts.GCFlags,
						SkipUnchanged: opts.SkipUnchanged,
						Force:         opts.Force,
					}); err == nil {
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
	return GenerateBindingsWithOptions(ctx, cfg, false, false)
}

// GenerateBindingsWithOptions runs bindings generation, optionally skipping it
// when the cached fingerprint matches.
func GenerateBindingsWithOptions(ctx context.Context, cfg *config.Config, skipUnchanged, force bool) error {
	cmdStr := strings.TrimSpace(cfg.Frontend.BindingsCmd)
	if cmdStr == "" {
		cmdStr = "wails3 generate bindings -ts -i"
	}

	var store *buildcache.Store
	var fingerprint string
	outputs := buildcache.BindingsOutputs(cfg)
	if skipUnchanged && !force {
		store = buildcache.Open(buildcache.Path(cfg.RootDir))
		if inputs, err := buildcache.BindingsInputs(cfg); err == nil {
			if fp, err := buildcache.Fingerprint(inputs, "bindings", cmdStr, "go:"+runtime.Version()); err == nil {
				fingerprint = fp
				if len(outputs) > 0 && store.Fresh("bindings", fp, outputs...) {
					LogCook("Bindings unchanged; skipping generation")
					return nil
				}
			}
		}
	}

	startTime := time.Now()
	cmd := shellCommandContext(ctx, cmdStr)
	cmd.Dir = cfg.RootDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		outStr := strings.TrimSpace(string(output))
		return fmt.Errorf("bindings generation failed: %s (%w)", outStr, err)
	}

	if fingerprint != "" && store != nil {
		_ = store.Mark("bindings", fingerprint)
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
