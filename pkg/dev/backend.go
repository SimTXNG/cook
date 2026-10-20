package dev

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/simtxng/cook/pkg/buildcache"
	"github.com/simtxng/cook/pkg/builder"
	"github.com/simtxng/cook/pkg/config"
	hookexec "github.com/simtxng/cook/pkg/exec"
)

// BackendSupervisor manages the building, running, and restarting of the backend app.
type BackendSupervisor struct {
	cfg         *config.Config
	cmd         *exec.Cmd
	mu          sync.Mutex
	binaryName  string
	binaryPath  string
	cancelFunc  context.CancelFunc
	appExitedCh chan struct{}
	done        chan struct{}
}

// NewBackendSupervisor creates a new BackendSupervisor.
func NewBackendSupervisor(cfg *config.Config) *BackendSupervisor {
	name := cfg.Package.Name
	if name == "" || binaryByName(cfg.Binaries, name) == nil {
		for _, b := range cfg.Binaries {
			if b.GUI {
				name = b.Name
				break
			}
		}
	}
	if name == "" && len(cfg.Binaries) > 0 {
		name = cfg.Binaries[0].Name
	}
	return &BackendSupervisor{
		cfg:         cfg,
		binaryName:  name,
		appExitedCh: make(chan struct{}, 1),
	}
}

func binaryByName(binaries []config.BinaryConfig, name string) *config.BinaryConfig {
	for i := range binaries {
		if binaries[i].Name == name {
			return &binaries[i]
		}
	}
	return nil
}

// AppExited returns a channel that signals when the backend application has exited on its own.
func (b *BackendSupervisor) AppExited() <-chan struct{} {
	return b.appExitedCh
}

// CompileOptions controls a development compilation.
type CompileOptions struct {
	ExtraTags     []string
	GCFlags       string
	SkipUnchanged bool
	Force         bool
}

// Compile performs an incremental development compilation of the backend binary.
func (b *BackendSupervisor) Compile(ctx context.Context, extraTags []string) (string, error) {
	return b.CompileWithOptions(ctx, CompileOptions{ExtraTags: extraTags})
}

// CompileWithOptions performs a development compilation with optional cache
// reuse and debug flags.
func (b *BackendSupervisor) CompileWithOptions(ctx context.Context, opts CompileOptions) (string, error) {
	outDir := filepath.Join(b.cfg.RootDir, b.cfg.Package.OutputDir)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create output dir: %w", err)
	}

	targetBinary := binaryByName(b.cfg.Binaries, b.binaryName)
	if targetBinary == nil {
		return "", fmt.Errorf("binary %q is not configured", b.binaryName)
	}

	binPath := builder.GoOutputPath(b.cfg, targetBinary, runtime.GOOS)
	stagingPath := devStagingPath(binPath)
	tags := builder.GoTags(targetBinary, builder.GoBuildOptions{
		ExcludeTags: []string{"production"},
		ExtraTags:   opts.ExtraTags,
	})

	// Strip the production tag in dev builds so Wails devmode / webview dev
	// server is enabled, and skip the configured release ldflags. -trimpath
	// keeps the Go build cache shared with release builds.
	args := builder.GoBuildArgs(b.cfg, targetBinary, runtime.GOOS, builder.GoBuildOptions{
		ExcludeTags: []string{"production"},
		ExtraTags:   opts.ExtraTags,
		GCFlags:     opts.GCFlags,
		Trimpath:    true,
		OutFile:     stagingPath,
	})
	env := builder.GoBuildEnv(b.cfg, targetBinary, runtime.GOOS, runtime.GOARCH)

	var store *buildcache.Store
	var fingerprint string
	cacheKey := "go-dev:" + b.binaryName
	if opts.SkipUnchanged && !opts.Force {
		store = buildcache.Open(buildcache.Path(b.cfg.RootDir))
		if inputs, err := builder.GoInputFiles(ctx, b.cfg, targetBinary, runtime.GOOS, runtime.GOARCH, tags); err == nil {
			extras := append([]string{"go:" + runtime.Version()}, builder.GoBuildCacheArgs(args)...)
			keys := make([]string, 0, len(env))
			for k := range env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				extras = append(extras, k+"="+env[k])
			}
			if fp, err := buildcache.Fingerprint(inputs, extras...); err == nil {
				fingerprint = fp
				b.mu.Lock()
				existing := b.binaryPath
				b.mu.Unlock()
				if existing != "" && store.Fresh(cacheKey, fp, existing) {
					LogCook("Backend unchanged; reusing %s", existing)
					return existing, nil
				}
			}
		}
	}

	startTime := time.Now()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = b.cfg.RootDir
	cmd.Env = hookexec.BuildEnv(env)

	output, err := cmd.CombinedOutput()
	if err != nil {
		outStr := strings.TrimSpace(string(output))
		LogError("compiler", "Compilation failed:\n%s", outStr)
		return "", fmt.Errorf("build failed: %w", err)
	}

	duration := time.Since(startTime).Round(time.Millisecond)
	LogSuccess("Compiled %s in %v", b.binaryName, duration)
	if fingerprint != "" && store != nil {
		_ = store.Mark(cacheKey, fingerprint)
	}
	b.mu.Lock()
	b.binaryPath = stagingPath
	b.mu.Unlock()
	cleanupDevStaging(binPath, stagingPath)
	return stagingPath, nil
}

func devStagingPath(binPath string) string {
	ext := filepath.Ext(binPath)
	return fmt.Sprintf("%s.cook-dev-%d%s", strings.TrimSuffix(binPath, ext), time.Now().UnixNano(), ext)
}

func cleanupDevStaging(binPath, current string) {
	ext := filepath.Ext(binPath)
	pattern := strings.TrimSuffix(binPath, ext) + ".cook-dev-*" + ext
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return
	}
	for _, match := range matches {
		if match == current {
			continue
		}
		_ = os.Remove(match)
	}
}

// Start spawns the compiled backend binary connected to the frontend dev server.
func (b *BackendSupervisor) Start(ctx context.Context, frontendURL string, port int, envOverrides map[string]string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.binaryPath == "" {
		return fmt.Errorf("no binary path available; compile first")
	}

	appCtx, cancel := context.WithCancel(ctx)

	cmd := exec.CommandContext(appCtx, b.binaryPath)
	cmd.Dir = b.cfg.RootDir

	setSysProcAttr(cmd)

	// Environment configuration
	cmdEnv := append(append([]string(nil), os.Environ()...),
		fmt.Sprintf("FRONTEND_DEVSERVER_URL=%s", frontendURL),
		fmt.Sprintf("WAILS_VITE_PORT=%d", port),
		"DEV=true",
	)
	for k, v := range envOverrides {
		cmdEnv = append(cmdEnv, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = cmdEnv

	err := startProcess(cmd, cancel, LogBackend, "backend", filterBackendLog, func(done chan struct{}) {
		b.cmd = cmd
		b.cancelFunc = cancel
		b.done = done
	}, func() {
		b.mu.Lock()
		current := b.cmd
		b.mu.Unlock()

		if current == cmd {
			select {
			case b.appExitedCh <- struct{}{}:
			default:
			}
		}
	})
	if err != nil {
		return err
	}

	LogCook("Launched %s (PID %d)", b.binaryName, cmd.Process.Pid)
	return nil
}

func filterBackendLog(text string) (string, bool) {
	// Filter out obsolete/misleading Wails v3 watcher messages
	if strings.Contains(text, "The application has terminated, but the watcher is still running") ||
		strings.Contains(text, "To terminate the watcher, press CTRL+C") {
		return "", false
	}
	return text, true
}

// Restart stops the running backend process and launches the newly compiled binary.
func (b *BackendSupervisor) Restart(ctx context.Context, frontendURL string, port int, envOverrides map[string]string) error {
	b.Stop()
	return b.Start(ctx, frontendURL, port, envOverrides)
}

// Stop terminates the backend process and any child processes.
func (b *BackendSupervisor) Stop() {
	b.mu.Lock()
	cmd := b.cmd
	done := b.done
	cancel := b.cancelFunc
	b.cmd = nil
	b.done = nil
	b.cancelFunc = nil
	b.mu.Unlock()

	stopProcess(cmd, cancel, done)
}
