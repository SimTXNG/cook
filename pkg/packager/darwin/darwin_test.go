package darwin

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/simtxng/cook/pkg/config"
)

func TestDarwinPackagerInterface(t *testing.T) {
	p := New()
	if p.Name() != "darwin" {
		t.Errorf("expected Name() = darwin, got %s", p.Name())
	}

	targets := p.SupportedTargets()
	targetMap := make(map[string]bool)
	for _, tg := range targets {
		targetMap[tg] = true
	}

	for _, expected := range []string{"app", "dmg"} {
		if !targetMap[expected] {
			t.Errorf("expected supported target %s", expected)
		}
	}
}

func TestDarwinPackagerPackageApp(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cook-darwinpack-app-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	guiPath := filepath.Join(tmpDir, "simtx")
	_ = os.WriteFile(guiPath, []byte("fake-gui-binary"), 0755)

	binaries := map[string]string{
		"simtx": guiPath,
	}

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:        "simtx",
			ProductName: "SimTX",
			Identifier:  "net.simtx.app",
			OutputDir:   "bin",
		},
		Darwin: config.DarwinConfig{
			Codesign: "skip", // Skip code signing in unit tests
		},
		Version: &config.VersionInfo{
			Raw: "0.1.0",
		},
		RootDir: tmpDir,
	}

	p := New()
	artifacts, err := p.Package(context.Background(), cfg, "app", "arm64", binaries)
	if err != nil {
		t.Fatalf("Package(app) failed: %v", err)
	}

	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(artifacts))
	}

	appBundle := artifacts[0]
	if !dirExists(appBundle) {
		t.Errorf("expected app bundle directory at %s", appBundle)
	}

	// Verify entitlements.plist was also generated in output dir
	entPath := filepath.Join(filepath.Dir(appBundle), "entitlements.plist")
	if !fileExists(entPath) {
		t.Errorf("expected entitlements.plist generated at %s", entPath)
	}
}

func TestDarwinPackagerUnsupportedTarget(t *testing.T) {
	p := New()
	_, err := p.Package(context.Background(), &config.Config{}, "unknown", "arm64", nil)
	if err == nil {
		t.Fatalf("expected error for unsupported target")
	}
}

func TestDarwinPackagerConfiguredEntitlementsMissing(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cook-darwinpack-entitlements-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	guiPath := filepath.Join(tmpDir, "simtx")
	_ = os.WriteFile(guiPath, []byte("fake-gui-binary"), 0755)

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:        "simtx",
			ProductName: "SimTX",
			Identifier:  "net.simtx.app",
			OutputDir:   "bin",
		},
		Darwin: config.DarwinConfig{
			Codesign:     "skip",
			Entitlements: "missing.plist",
		},
		RootDir: tmpDir,
	}

	p := New()
	_, err = p.Package(context.Background(), cfg, "app", "arm64", map[string]string{"simtx": guiPath})
	if err == nil {
		t.Fatalf("expected app target to fail when configured entitlements are missing")
	}
}

func TestDarwinPackagerAppHonorsNotarize(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cook-darwinpack-notarize-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	guiPath := filepath.Join(tmpDir, "simtx")
	_ = os.WriteFile(guiPath, []byte("fake-gui-binary"), 0755)

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:        "simtx",
			ProductName: "SimTX",
			Identifier:  "net.simtx.app",
			OutputDir:   "bin",
		},
		Darwin: config.DarwinConfig{
			Codesign: "skip",
			Notarize: true,
			// NotarizeProfile intentionally empty
		},
		RootDir: tmpDir,
	}

	p := New()
	_, err = p.Package(context.Background(), cfg, "app", "arm64", map[string]string{"simtx": guiPath})
	if err == nil {
		t.Fatalf("expected app target to fail when notarize is enabled without a profile")
	}
}
