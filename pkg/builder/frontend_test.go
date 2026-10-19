package builder

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/simtxng/cook/pkg/config"
)

// fakeCommand installs a shell script that logs its invocation to $FAKE_LOG and
// creates a dist directory when asked to run a build.
func fakeCommand(t *testing.T, binDir, name string) {
	t.Helper()

	script := `#!/bin/sh
printf '%s %s\n' "$(basename "$0")" "$*" >> "$FAKE_LOG"
case " $* " in
  *" build "*) mkdir -p dist ;;
esac
exit 0
`
	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
}

func setupFrontend(t *testing.T, packageManager string) (*config.Config, string, string) {
	t.Helper()

	root := t.TempDir()
	frontendDir := filepath.Join(root, "frontend")
	if err := os.MkdirAll(frontendDir, 0o755); err != nil {
		t.Fatalf("create frontend dir: %v", err)
	}

	binDir := t.TempDir()
	for _, name := range []string{"npm", "bun", "pnpm", "yarn"} {
		fakeCommand(t, binDir, name)
	}
	logPath := filepath.Join(t.TempDir(), "commands.log")
	t.Setenv("FAKE_LOG", logPath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := &config.Config{
		RootDir: root,
		Frontend: config.FrontendConfig{
			Enabled:        true,
			Dir:            "frontend",
			PackageManager: packageManager,
			DistDir:        filepath.Join("frontend", "dist"),
		},
	}
	return cfg, frontendDir, logPath
}

func readLog(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read command log: %v", err)
	}
	return string(data)
}

func TestBuildFrontendSkipsInstallWhenNodeModulesExists(t *testing.T) {
	cfg, frontendDir, logPath := setupFrontend(t, "npm")
	if err := os.MkdirAll(filepath.Join(frontendDir, "node_modules"), 0o755); err != nil {
		t.Fatalf("create node_modules: %v", err)
	}
	cfg.Frontend.InstallCmd = "touch custom-install-ran"

	if err := BuildFrontend(context.Background(), cfg); err != nil {
		t.Fatalf("BuildFrontend: %v", err)
	}

	log := readLog(t, logPath)
	if strings.Contains(log, "install") {
		t.Errorf("install ran despite node_modules existing:\n%s", log)
	}
	if !strings.Contains(log, "npm run build") {
		t.Errorf("build did not run with npm:\n%s", log)
	}
	if _, err := os.Stat(filepath.Join(frontendDir, "custom-install-ran")); !os.IsNotExist(err) {
		t.Errorf("custom install command ran despite node_modules existing")
	}
}

func TestBuildFrontendInstallsAndBuildsWithPackageManager(t *testing.T) {
	tests := []struct {
		packageManager string
		wantInstall    string
		wantBuild      string
	}{
		{"npm", "npm install", "npm run build"},
		{"bun", "bun install", "bun run build"},
		{"pnpm", "pnpm install", "pnpm run build"},
		{"yarn", "yarn install", "yarn build"},
	}

	for _, tt := range tests {
		t.Run(tt.packageManager, func(t *testing.T) {
			cfg, _, logPath := setupFrontend(t, tt.packageManager)
			// config.applyDefaults writes this legacy value even when another
			// package manager is configured; the builder must override it.
			cfg.Frontend.BuildCmd = defaultNPMBuildCmd

			if err := BuildFrontend(context.Background(), cfg); err != nil {
				t.Fatalf("BuildFrontend: %v", err)
			}

			log := readLog(t, logPath)
			if !strings.Contains(log, tt.wantInstall) {
				t.Errorf("missing install command %q:\n%s", tt.wantInstall, log)
			}
			if !strings.Contains(log, tt.wantBuild) {
				t.Errorf("missing build command %q:\n%s", tt.wantBuild, log)
			}
			if strings.Index(log, tt.wantInstall) > strings.Index(log, tt.wantBuild) {
				t.Errorf("install ran after build:\n%s", log)
			}
		})
	}
}

func TestBuildFrontendCustomInstallOverridesDefault(t *testing.T) {
	cfg, _, logPath := setupFrontend(t, "npm")
	cfg.Frontend.InstallCmd = "npm custom-install"

	if err := BuildFrontend(context.Background(), cfg); err != nil {
		t.Fatalf("BuildFrontend: %v", err)
	}

	log := readLog(t, logPath)
	if !strings.Contains(log, "custom-install") {
		t.Errorf("custom install command did not run:\n%s", log)
	}
	if strings.Contains(log, "npm install") {
		t.Errorf("default install command ran alongside custom install:\n%s", log)
	}
}

func TestBuildFrontendDistDirResolution(t *testing.T) {
	tests := []struct {
		name        string
		distDir     string
		create      func(t *testing.T, root, frontendDir string) string
		expectError bool
	}{
		{
			name:    "default root relative",
			distDir: filepath.Join("frontend", "dist"),
			create: func(t *testing.T, root, frontendDir string) string {
				path := filepath.Join(root, "frontend", "dist")
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatalf("create dist: %v", err)
				}
				return path
			},
		},
		{
			name:    "absolute",
			distDir: "absolute",
			create: func(t *testing.T, root, frontendDir string) string {
				path := filepath.Join(t.TempDir(), "dist")
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatalf("create dist: %v", err)
				}
				return path
			},
		},
		{
			name:    "root relative non default",
			distDir: filepath.Join("frontend", "out"),
			create: func(t *testing.T, root, frontendDir string) string {
				path := filepath.Join(root, "frontend", "out")
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatalf("create dist: %v", err)
				}
				return path
			},
		},
		{
			name:    "frontend relative fallback",
			distDir: "out",
			create: func(t *testing.T, root, frontendDir string) string {
				path := filepath.Join(frontendDir, "out")
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatalf("create dist: %v", err)
				}
				return path
			},
		},
		{
			name:    "missing",
			distDir: "missing",
			create: func(t *testing.T, root, frontendDir string) string {
				return ""
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, frontendDir, _ := setupFrontend(t, "npm")
			cfg.Frontend.DistDir = tt.distDir
			if tt.distDir == "absolute" {
				cfg.Frontend.DistDir = tt.create(t, cfg.RootDir, frontendDir)
			} else {
				tt.create(t, cfg.RootDir, frontendDir)
			}

			err := BuildFrontend(context.Background(), cfg)
			if tt.expectError {
				if err == nil {
					t.Fatal("expected missing output directory error")
				}
				return
			}
			if err != nil {
				t.Fatalf("BuildFrontend: %v", err)
			}
		})
	}
}
