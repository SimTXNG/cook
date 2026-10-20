package builder

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/simtxng/cook/pkg/buildcache"
	"github.com/simtxng/cook/pkg/config"
	"github.com/simtxng/cook/pkg/exec"
	"github.com/simtxng/cook/pkg/timing"
)

// BuildOptions controls optional build behavior.
type BuildOptions struct {
	// Jobs limits how many binaries compile concurrently. Zero selects a
	// CPU-based default; one disables binary-level parallelism.
	Jobs int
	// SkipUnchanged skips phases whose inputs match the recorded fingerprint.
	SkipUnchanged bool
	// Force bypasses SkipUnchanged.
	Force bool
	// SkipBindings skips the configured bindings command.
	SkipBindings bool
}

// Builder coordinates frontend builds, hook invocations, and Go compilation.
type Builder struct {
	Config *config.Config
	Hooks  *exec.HookManager
	cache  *buildcache.Store
}

func NewBuilder(cfg *config.Config) *Builder {
	return &Builder{
		Config: cfg,
		Hooks:  exec.NewHookManager(cfg),
	}
}

// Build compiles the frontend (if enabled) and all requested binaries for target OS and ARCH.
func (b *Builder) Build(ctx context.Context, binaryFilter []string, targetOS string, targetArch string) (map[string]string, error) {
	return b.BuildWithOptions(ctx, binaryFilter, targetOS, targetArch, BuildOptions{})
}

// BuildWithOptions compiles the frontend (if enabled) and requested binaries
// using the supplied options.
func (b *Builder) BuildWithOptions(ctx context.Context, binaryFilter []string, targetOS string, targetArch string, opts BuildOptions) (map[string]string, error) {
	targetOS = config.NormalizeOS(targetOS)
	targetArch = config.NormalizeArch(targetArch)

	// 1. Resolve the requested binaries before running any hooks. Preserve the
	// configured order and fail when a filter name matches no configured binary.
	binariesToBuild := b.Config.Binaries
	if len(binaryFilter) > 0 {
		var unknown []string
		for _, name := range binaryFilter {
			known := slices.ContainsFunc(b.Config.Binaries, func(bc config.BinaryConfig) bool {
				return bc.Name == name
			})
			if !known && !slices.Contains(unknown, name) {
				unknown = append(unknown, name)
			}
		}
		if len(unknown) > 0 {
			return nil, fmt.Errorf("unknown binary filter(s): %s", strings.Join(unknown, ", "))
		}

		var filtered []config.BinaryConfig
		for _, bc := range b.Config.Binaries {
			if slices.Contains(binaryFilter, bc.Name) {
				filtered = append(filtered, bc)
			}
		}
		binariesToBuild = filtered
	}

	// 2. Pre-build hooks
	if err := b.Hooks.RunPreBuild(ctx); err != nil {
		return nil, err
	}

	if opts.SkipUnchanged && !opts.Force {
		b.cache = buildcache.Open(buildcache.Path(b.Config.RootDir))
	}

	// 3. Build frontend
	frontendDone := timing.Start("frontend phase")
	if err := buildFrontend(ctx, b.Config, opts, b.cache); err != nil {
		frontendDone()
		return nil, err
	}
	frontendDone()

	// 4. Build each binary, bounded by the requested/automatic worker count.
	built, err := b.buildBinaries(ctx, binariesToBuild, targetOS, targetArch, opts)
	if err != nil {
		return nil, err
	}

	// 5. Post-build hooks
	if err := b.Hooks.RunPostBuild(ctx); err != nil {
		return nil, err
	}

	return built, nil
}

func (b *Builder) buildBinaries(ctx context.Context, binaries []config.BinaryConfig, targetOS, targetArch string, opts BuildOptions) (map[string]string, error) {
	requestedJobs := opts.Jobs
	if requestedJobs == 0 && len(binaries) > 1 && goBuildCacheCold() {
		// On a cold cache, concurrent binaries duplicate the compilation of
		// their shared dependencies (including the standard library), which is
		// slower than letting the first build warm the cache for the rest.
		requestedJobs = 1
	}
	workers := resolveJobs(requestedJobs, len(binaries))
	parallelism := 0
	if workers > 1 {
		parallelism = runtime.GOMAXPROCS(0) / workers
		if parallelism < 1 {
			parallelism = 1
		}
	}

	buildCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
		built    = make(map[string]string, len(binaries))
	)
	sem := make(chan struct{}, workers)

	for i := range binaries {
		bCfg := binaries[i]
		wg.Add(1)
		go func() {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
			case <-buildCtx.Done():
				return
			}
			defer func() { <-sem }()

			if buildCtx.Err() != nil {
				return
			}

			path, err := buildBinaryWithOptions(buildCtx, b.Config, &bCfg, targetOS, targetArch, opts, parallelism, b.cache)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				return
			}
			built[bCfg.Name] = path
		}()
	}

	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return built, nil
}

// goBuildCacheCold reports whether the Go build cache has no compiled entries.
func goBuildCacheCold() bool {
	cacheDir := os.Getenv("GOCACHE")
	if cacheDir == "" {
		out, err := osexec.Command("go", "env", "GOCACHE").Output()
		if err != nil {
			return false
		}
		cacheDir = strings.TrimSpace(string(out))
	}
	if cacheDir == "" {
		return false
	}

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return true
	}
	for _, entry := range entries {
		if !entry.IsDir() || len(entry.Name()) != 2 {
			continue
		}
		children, err := os.ReadDir(filepath.Join(cacheDir, entry.Name()))
		if err == nil && len(children) > 0 {
			return false
		}
	}
	return true
}

// resolveJobs returns the effective worker count for binary builds.
func resolveJobs(requested, binaries int) int {
	if binaries <= 1 {
		return 1
	}
	if requested > 0 {
		if requested > binaries {
			return binaries
		}
		return requested
	}

	workers := runtime.GOMAXPROCS(0) / 2
	if workers < 2 {
		workers = 1
	}
	if workers > 8 {
		workers = 8
	}
	if workers > binaries {
		workers = binaries
	}
	return workers
}
