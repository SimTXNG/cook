package windows

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/simtxng/cook/pkg/config"
)

func TestWindowsPackagerInterface(t *testing.T) {
	p := New()
	if p.Name() != "windows" {
		t.Errorf("expected Name() = windows, got %s", p.Name())
	}

	targets := p.SupportedTargets()
	targetMap := make(map[string]bool)
	for _, tg := range targets {
		targetMap[tg] = true
	}

	for _, expected := range []string{"syso", "nsis", "msix"} {
		if !targetMap[expected] {
			t.Errorf("expected supported target %s", expected)
		}
	}
}

func TestWindowsPackagerPackageSyso(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cook-winpack-syso-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name: "simtx",
		},
		Version: &config.VersionInfo{
			Raw:     "0.1.0",
			Windows: "0.1.0.0",
		},
		RootDir: tmpDir,
	}

	p := New()
	artifacts, err := p.Package(context.Background(), cfg, "syso", "amd64", nil)
	if err != nil {
		t.Fatalf("Package(syso) failed: %v", err)
	}
	if len(artifacts) != 1 || !fileExists(artifacts[0]) {
		t.Fatalf("expected 1 created syso artifact, got %v", artifacts)
	}
}

func TestWindowsPackagerPackageMSIX(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cook-winpack-msix-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	guiPath := filepath.Join(tmpDir, "simtx.exe")
	_ = os.WriteFile(guiPath, []byte("fake-gui-binary"), 0755)

	binaries := map[string]string{
		"simtx": guiPath,
	}

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:      "simtx",
			OutputDir: "bin",
		},
		Version: &config.VersionInfo{
			Raw:     "0.1.0",
			Windows: "0.1.0.0",
		},
		RootDir: tmpDir,
	}

	p := New()
	artifacts, err := p.Package(context.Background(), cfg, "msix", "amd64", binaries)
	if err != nil {
		t.Fatalf("Package(msix) failed: %v", err)
	}
	if len(artifacts) != 1 || !fileExists(artifacts[0]) {
		t.Fatalf("expected 1 created msix artifact, got %v", artifacts)
	}
}

func TestWindowsPackagerUnsupportedTarget(t *testing.T) {
	p := New()
	_, err := p.Package(context.Background(), &config.Config{}, "unknown", "", nil)
	if err == nil {
		t.Fatalf("expected error for unsupported target")
	}
}
