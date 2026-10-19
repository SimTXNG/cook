package builder

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/simtxng/cook/pkg/config"
	"github.com/simtxng/cook/pkg/exec"
)

// Builder coordinates frontend builds, hook invocations, and Go compilation.
type Builder struct {
	Config *config.Config
	Hooks  *exec.HookManager
}

func NewBuilder(cfg *config.Config) *Builder {
	return &Builder{
		Config: cfg,
		Hooks:  exec.NewHookManager(cfg),
	}
}

// Build compiles the frontend (if enabled) and all requested binaries for target OS and ARCH.
func (b *Builder) Build(ctx context.Context, binaryFilter []string, targetOS string, targetArch string) (map[string]string, error) {
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

	// 3. Build frontend
	if err := BuildFrontend(ctx, b.Config); err != nil {
		return nil, err
	}

	// 4. Build each binary
	built := make(map[string]string)
	for _, bCfg := range binariesToBuild {
		outPath, err := BuildBinary(ctx, b.Config, &bCfg, targetOS, targetArch)
		if err != nil {
			return nil, err
		}
		built[bCfg.Name] = outPath
	}

	// 5. Post-build hooks
	if err := b.Hooks.RunPostBuild(ctx); err != nil {
		return nil, err
	}

	return built, nil
}
