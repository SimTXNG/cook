package linux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/simtxng/cook/pkg/config"
)

func TestGenerateDesktopContent(t *testing.T) {
	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:        "simtx",
			ProductName: "SimTX Desktop",
			Description: "SimTX desktop application",
			Icon:        "build/appicon.png",
		},
		Linux: config.LinuxConfig{
			Categories: "Development;Utility",
		},
	}

	content := GenerateDesktopContent(cfg, "", "")

	expectedSnippets := []string{
		"[Desktop Entry]",
		"Type=Application",
		"Name=SimTX Desktop",
		"Comment=SimTX desktop application",
		"Exec=simtx",
		"Icon=simtx",
		"Categories=Development;Utility;",
		"Terminal=false",
		"StartupNotify=false",
		"StartupWMClass=simtx",
	}

	for _, s := range expectedSnippets {
		if !strings.Contains(content, s) {
			t.Errorf("expected desktop content to contain %q, but got:\n%s", s, content)
		}
	}
}

func TestEnsureDesktopFile(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:       "simtx",
			OutputDir:  "bin",
			Maintainer: "SimTX <packages@simtx.net>",
		},
		Linux: config.LinuxConfig{
			Categories: "Utility;",
		},
		RootDir: tmpDir,
	}

	destPath := filepath.Join(tmpDir, "simtx.desktop")
	outPath, err := EnsureDesktopFile(cfg, destPath)
	if err != nil {
		t.Fatalf("EnsureDesktopFile failed: %v", err)
	}

	if outPath != destPath {
		t.Fatalf("expected outPath=%s, got %s", destPath, outPath)
	}

	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("failed to read created desktop file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "Name=simtx") {
		t.Errorf("desktop file missing Name=simtx:\n%s", content)
	}
	if !strings.Contains(content, "Categories=Utility;") {
		t.Errorf("desktop file missing Categories=Utility;:\n%s", content)
	}
}
