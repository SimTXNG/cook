package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/simtxng/cook/pkg/config"
	"github.com/simtxng/cook/pkg/exec"
)

// BuildFrontend executes binding generation and frontend asset compilation.
func BuildFrontend(ctx context.Context, cfg *config.Config) error {
	if !cfg.Frontend.Enabled {
		return nil
	}

	frontendDir := filepath.Join(cfg.RootDir, cfg.Frontend.Dir)
	if _, err := os.Stat(frontendDir); err != nil {
		return fmt.Errorf("frontend directory %s does not exist: %w", frontendDir, err)
	}

	runner := exec.NewRunner("frontend")

	// 1. Generate bindings if configured
	if cfg.Frontend.BindingsCmd != "" {
		fmt.Printf("==> Generating bindings with: %s\n", cfg.Frontend.BindingsCmd)
		if err := runner.Run(ctx, cfg.Frontend.BindingsCmd, cfg.RootDir, nil); err != nil {
			return fmt.Errorf("binding generation failed: %w", err)
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
		if err := runner.Run(ctx, installCmd, frontendDir, nil); err != nil {
			return fmt.Errorf("frontend dependencies installation failed: %w", err)
		}
	}

	// 3. Build frontend assets. A custom BuildCmd wins. The legacy npm default
	// written by config defaults is treated as unset so the configured package
	// manager still decides how to run the build.
	buildCmd := cfg.Frontend.BuildCmd
	if buildCmd == "" || (buildCmd == defaultNPMBuildCmd && cfg.Frontend.PackageManager != "" && cfg.Frontend.PackageManager != "npm") {
		buildCmd = config.DefaultBuildCmd(cfg.Frontend.PackageManager)
	}
	fmt.Printf("==> Compiling frontend assets: %s\n", buildCmd)
	if err := runner.Run(ctx, buildCmd, frontendDir, map[string]string{
		"NODE_ENV": "production",
	}); err != nil {
		return fmt.Errorf("frontend asset compilation failed: %w", err)
	}

	// 4. Verify output directory. Absolute paths are used as-is; relative paths
	// resolve against RootDir, falling back to the frontend directory.
	distDir := cfg.Frontend.DistDir
	if distDir == "" {
		frontendName := cfg.Frontend.Dir
		if frontendName == "" {
			frontendName = "frontend"
		}
		distDir = filepath.Join(frontendName, "dist")
	}
	if !filepath.IsAbs(distDir) {
		rootRelative := filepath.Join(cfg.RootDir, distDir)
		if _, err := os.Stat(rootRelative); err == nil {
			distDir = rootRelative
		} else {
			distDir = filepath.Join(frontendDir, distDir)
		}
	}
	if _, err := os.Stat(distDir); err != nil {
		return fmt.Errorf("frontend build finished but output directory %s is missing: %w", distDir, err)
	}

	return nil
}

const defaultNPMBuildCmd = "npm run build"
