package linux

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/simtxng/cook/pkg/config"
	"github.com/simtxng/cook/pkg/packager"
)

func TestLinuxPackager_Interface(t *testing.T) {
	var p packager.Packager = NewPackager()

	if p.Name() != "linux" {
		t.Errorf("expected Name() to be 'linux', got %s", p.Name())
	}

	expectedTargets := []string{"deb", "rpm", "archlinux", "appimage"}
	if !reflect.DeepEqual(p.SupportedTargets(), expectedTargets) {
		t.Errorf("expected SupportedTargets() to be %v, got %v", expectedTargets, p.SupportedTargets())
	}

	cfg := &config.Config{
		Package: config.PackageConfig{Name: "test"},
	}

	// Test unsupported target
	_, err := p.Package(context.Background(), cfg, "msix", "amd64", nil)
	if err == nil {
		t.Errorf("expected error for unsupported target 'msix', got nil")
	}
}

func TestLinuxPackager_PackageRouting(t *testing.T) {
	tmpDir := t.TempDir()

	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	guiBin := filepath.Join(binDir, "simtx")
	_ = os.WriteFile(guiBin, []byte("#!/bin/sh\necho test\n"), 0755)

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:        "simtx",
			Description: "Routing test",
			OutputDir:   "bin",
			Maintainer:  "SimTX <packages@simtx.net>",
			License:     "MIT",
		},
		Linux: config.LinuxConfig{
			Deb: config.LinuxDebConfig{
				Depends: []string{"libgtk-4-1"},
			},
		},
		Version: &config.VersionInfo{
			Raw:    "0.1.0",
			Debian: "0.1.0",
		},
		RootDir: tmpDir,
	}

	binaries := map[string]string{
		"simtx": guiBin,
	}

	p := NewPackager()
	artifacts, err := p.Package(context.Background(), cfg, "deb", "amd64", binaries)
	if err != nil {
		t.Fatalf("Package(deb) failed: %v", err)
	}

	if len(artifacts) == 0 {
		t.Errorf("expected artifacts returned from Package(deb)")
	}
}
