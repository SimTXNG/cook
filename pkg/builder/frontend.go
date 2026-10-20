package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/simtxng/cook/pkg/buildcache"
	"github.com/simtxng/cook/pkg/config"
	"github.com/simtxng/cook/pkg/exec"
	"github.com/simtxng/cook/pkg/timing"
)

// BuildFrontend executes binding generation and frontend asset compilation.
func BuildFrontend(ctx context.Context, cfg *config.Config) error {
	return BuildFrontendWithOptions(ctx, cfg, BuildOptions{})
}

// BuildFrontendWithOptions executes binding generation and frontend asset
// compilation, optionally skipping unchanged phases.
func BuildFrontendWithOptions(ctx context.Context, cfg *config.Config, opts BuildOptions) error {
	var store *buildcache.Store
	if opts.SkipUnchanged && !opts.Force {
		store = buildcache.Open(buildcache.Path(cfg.RootDir))
	}
	return buildFrontend(ctx, cfg, opts, store)
}

func buildFrontend(ctx context.Context, cfg *config.Config, opts BuildOptions, store *buildcache.Store) error {
	if !cfg.Frontend.Enabled {
		return nil
	}

	frontendDir := filepath.Join(cfg.RootDir, cfg.Frontend.Dir)
	if _, err := os.Stat(frontendDir); err != nil {
		return fmt.Errorf("frontend directory %s does not exist: %w", frontendDir, err)
	}

	runner := exec.NewRunner("frontend")

	// 1. Generate bindings if configured
	if cfg.Frontend.BindingsCmd != "" && !opts.SkipBindings {
		if err := runBindings(ctx, cfg, runner, opts, store); err != nil {
			return err
		}
	}

	distDirs := resolveFrontendDistDirs(cfg, frontendDir)

	buildCmd := cfg.Frontend.BuildCmd
	if buildCmd == "" || (buildCmd == defaultNPMBuildCmd && cfg.Frontend.PackageManager != "" && cfg.Frontend.PackageManager != "npm") {
		buildCmd = config.DefaultBuildCmd(cfg.Frontend.PackageManager)
	}

	var buildFingerprint string
	distDir := firstExistingDir(distDirs)
	if opts.SkipUnchanged && !opts.Force && store != nil {
		if inputs, err := buildcache.FrontendInputs(cfg, distDirs...); err == nil {
			extras := []string{
				"frontend-build",
				buildCmd,
				cfg.Frontend.PackageManager,
				cfg.Frontend.DistDir,
				"NODE_ENV=production",
				"node:" + runtime.Version(),
			}
			if fp, err := buildcache.Fingerprint(inputs, extras...); err == nil {
				buildFingerprint = fp
				if distDir != "" && store.Fresh("frontend:build", fp, distDir) {
					fmt.Printf("==> Skipping unchanged frontend build (%s)\n", distDir)
					return nil
				}
			}
		}
	}

	// 2. Install dependencies only when node_modules is missing. A custom install
	// command overrides which command runs, not whether installation is needed.
	nodeModulesDir := filepath.Join(frontendDir, "node_modules")
	if _, err := os.Stat(nodeModulesDir); os.IsNotExist(err) {
		installCmd := cfg.Frontend.InstallCmd
		if installCmd == "" {
			installCmd = config.DefaultInstallCmd(cfg.Frontend.PackageManager)
		}
		fmt.Printf("==> Installing frontend dependencies: %s\n", installCmd)
		done := timing.Start("frontend install")
		err := runner.Run(ctx, installCmd, frontendDir, nil)
		done()
		if err != nil {
			return fmt.Errorf("frontend dependencies installation failed: %w", err)
		}
	}

	// 3. Build frontend assets. A custom BuildCmd wins. The legacy npm default
	// written by config defaults is treated as unset so the configured package
	// manager still decides how to run the build.
	fmt.Printf("==> Compiling frontend assets: %s\n", buildCmd)
	done := timing.Start("frontend build")
	err := runner.Run(ctx, buildCmd, frontendDir, map[string]string{
		"NODE_ENV": "production",
	})
	done()
	if err != nil {
		return fmt.Errorf("frontend asset compilation failed: %w", err)
	}

	// 4. Verify output directory. Absolute paths are used as-is; relative paths
	// resolve against RootDir, falling back to the frontend directory.
	distDir = firstExistingDir(distDirs)
	if distDir == "" {
		return fmt.Errorf("frontend build finished but output directory is missing (checked: %s)", strings.Join(distDirs, ", "))
	}

	if buildFingerprint != "" && store != nil {
		_ = store.Mark("frontend:build", buildFingerprint)
	}

	return nil
}

func runBindings(ctx context.Context, cfg *config.Config, runner *exec.Runner, opts BuildOptions, store *buildcache.Store) error {
	cmd := cfg.Frontend.BindingsCmd
	outputs := buildcache.BindingsOutputs(cfg)

	var fingerprint string
	if opts.SkipUnchanged && !opts.Force && store != nil {
		if inputs, err := buildcache.BindingsInputs(cfg); err == nil {
			if fp, err := buildcache.Fingerprint(inputs, "bindings", cmd, "go:"+runtime.Version()); err == nil {
				fingerprint = fp
				if len(outputs) > 0 && store.Fresh("bindings", fp, outputs...) {
					fmt.Printf("==> Skipping unchanged bindings generation\n")
					return nil
				}
			}
		}
	}

	fmt.Printf("==> Generating bindings with: %s\n", cmd)
	done := timing.Start("bindings")
	err := runner.Run(ctx, cmd, cfg.RootDir, nil)
	done()
	if err != nil {
		return fmt.Errorf("binding generation failed: %w", err)
	}
	if fingerprint != "" && store != nil {
		_ = store.Mark("bindings", fingerprint)
	}
	return nil
}

func resolveFrontendDistDirs(cfg *config.Config, frontendDir string) []string {
	distDir := cfg.Frontend.DistDir
	if distDir == "" {
		frontendName := cfg.Frontend.Dir
		if frontendName == "" {
			frontendName = "frontend"
		}
		distDir = filepath.Join(frontendName, "dist")
	}
	if filepath.IsAbs(distDir) {
		return []string{distDir}
	}
	rootRelative := filepath.Join(cfg.RootDir, distDir)
	frontendRelative := filepath.Join(frontendDir, distDir)
	if rootRelative == frontendRelative {
		return []string{rootRelative}
	}
	return []string{rootRelative, frontendRelative}
}

func firstExistingDir(dirs []string) string {
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}

const defaultNPMBuildCmd = "npm run build"
