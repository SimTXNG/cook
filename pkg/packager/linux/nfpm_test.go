package linux

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/simtxng/cook/pkg/config"
)

func TestBuildNFPMInfo_Configurations(t *testing.T) {
	tmpDir := t.TempDir()

	// Create dummy binary files
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	guiBin := filepath.Join(binDir, "simtx")
	_ = os.WriteFile(guiBin, []byte("#!/bin/sh\necho simtx\n"), 0755)
	cliBin := filepath.Join(binDir, "simtx-cli")
	_ = os.WriteFile(cliBin, []byte("#!/bin/sh\necho simtx-cli\n"), 0755)
	iconPath := filepath.Join(tmpDir, "appicon.png")
	_ = os.WriteFile(iconPath, []byte("fake-png-data"), 0644)

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:        "simtx",
			ProductName: "SimTX Desktop",
			Description: "SimTX desktop application",
			Company:     "SimTX",
			Maintainer:  "SimTX Maintainers <packages@simtx.net>",
			License:     "MIT",
			Homepage:    "https://simtx.net",
			OutputDir:   "bin",
			Icon:        iconPath,
		},
		Linux: config.LinuxConfig{
			Categories: "Development;Utility;",
			BundledCLI: "simtx-cli",
			Deb: config.LinuxDebConfig{
				Section:  "utils",
				Priority: "optional",
				Depends:  []string{"libgtk-4-1", "libwebkitgtk-6.0-4"},
			},
			RPM: config.LinuxRPMConfig{
				Group:   "Applications/Communications",
				Depends: []string{"gtk4", "webkitgtk6.0"},
			},
			Arch: config.LinuxArchConfig{
				PkgBase: "simtx",
				Depends: []string{"gtk4", "webkitgtk-6.0", "hicolor-icon-theme"},
			},
		},
		Version: &config.VersionInfo{
			Raw:    "0.1.0-alpha-12",
			Debian: "0.1.0~alpha12",
			RPM:    "0.1.0~alpha12",
			Arch:   "0.1.0alpha12",
		},
		RootDir: tmpDir,
	}

	binaries := map[string]string{
		"simtx":     guiBin,
		"simtx-cli": cliBin,
	}

	// 1. Test Debian configuration
	debInfo, debCleanup, err := BuildNFPMInfo(cfg, "deb", "amd64", binaries)
	if err != nil {
		t.Fatalf("BuildNFPMInfo(deb) failed: %v", err)
	}
	defer debCleanup()

	if debInfo.Version != "0.1.0~alpha12" {
		t.Errorf("expected debian version 0.1.0~alpha12, got %s", debInfo.Version)
	}
	if debInfo.Release != "1" {
		t.Errorf("expected debian release 1, got %s", debInfo.Release)
	}
	if debInfo.Section != "utils" {
		t.Errorf("expected debian section utils, got %s", debInfo.Section)
	}
	if len(debInfo.Depends) != 2 || debInfo.Depends[0] != "libgtk-4-1" {
		t.Errorf("unexpected debian depends: %v", debInfo.Depends)
	}

	// Verify content mappings
	destMap := make(map[string]string)
	for _, c := range debInfo.Contents {
		destMap[c.Destination] = c.Source
	}

	expectedMappings := []string{
		"/usr/bin/simtx",
		"/usr/bin/simtx-cli",
		"/usr/share/applications/simtx.desktop",
		"/usr/share/icons/hicolor/128x128/apps/simtx.png",
	}

	for _, expectedDst := range expectedMappings {
		if _, ok := destMap[expectedDst]; !ok {
			t.Errorf("missing expected destination mapping: %s", expectedDst)
		}
	}

	// Verify maintainer scripts
	if debInfo.Scripts.PostInstall == "" {
		t.Errorf("expected PostInstall script to be populated")
	}
	postinstallBytes, err := os.ReadFile(debInfo.Scripts.PostInstall)
	if err != nil {
		t.Fatalf("failed to read postinstall script: %v", err)
	}
	if !strings.Contains(string(postinstallBytes), "update-desktop-database") {
		t.Errorf("postinstall script missing update-desktop-database")
	}

	// 2. Test RPM configuration
	rpmInfo, rpmCleanup, err := BuildNFPMInfo(cfg, "rpm", "amd64", binaries)
	if err != nil {
		t.Fatalf("BuildNFPMInfo(rpm) failed: %v", err)
	}
	defer rpmCleanup()

	if rpmInfo.Version != "0.1.0~alpha12" {
		t.Errorf("expected RPM version 0.1.0~alpha12, got %s", rpmInfo.Version)
	}
	if rpmInfo.RPM.Group != "Applications/Communications" {
		t.Errorf("expected RPM group Applications/Communications, got %s", rpmInfo.RPM.Group)
	}
	if len(rpmInfo.Depends) != 2 || rpmInfo.Depends[0] != "gtk4" {
		t.Errorf("unexpected RPM depends: %v", rpmInfo.Depends)
	}

	// 3. Test Arch Linux configuration
	archInfo, archCleanup, err := BuildNFPMInfo(cfg, "archlinux", "amd64", binaries)
	if err != nil {
		t.Fatalf("BuildNFPMInfo(archlinux) failed: %v", err)
	}
	defer archCleanup()

	if archInfo.Version != "0.1.0alpha12" {
		t.Errorf("expected Arch version 0.1.0alpha12, got %s", archInfo.Version)
	}
	if archInfo.ArchLinux.Pkgbase != "simtx" {
		t.Errorf("expected Arch pkgbase simtx, got %s", archInfo.ArchLinux.Pkgbase)
	}
	if len(archInfo.Depends) != 3 || archInfo.Depends[2] != "hicolor-icon-theme" {
		t.Errorf("unexpected Arch depends: %v", archInfo.Depends)
	}
}

func TestBuildNFPMInfo_EmptyTargetVersionFallsBackToRaw(t *testing.T) {
	tmpDir := t.TempDir()

	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	guiBin := filepath.Join(binDir, "simtx")
	_ = os.WriteFile(guiBin, []byte("#!/bin/sh\n"), 0755)

	// RPM and Arch are deliberately empty: an unset per-target version must not
	// win over Raw.
	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:      "simtx",
			OutputDir: "bin",
		},
		Version: &config.VersionInfo{
			Raw:    "1.2.3-alpha-1",
			Debian: "1.2.3~alpha1",
		},
		RootDir: tmpDir,
	}
	binaries := map[string]string{"simtx": guiBin}

	for _, tc := range []struct {
		target string
		want   string
	}{
		{"deb", "1.2.3~alpha1"},
		{"rpm", "1.2.3-alpha-1"},
		{"archlinux", "1.2.3-alpha-1"},
	} {
		info, cleanup, err := BuildNFPMInfo(cfg, tc.target, "amd64", binaries)
		if err != nil {
			t.Fatalf("BuildNFPMInfo(%s) failed: %v", tc.target, err)
		}
		if info.Version != tc.want {
			t.Errorf("%s version = %q, want %q", tc.target, info.Version, tc.want)
		}
		cleanup()
	}
}

func TestPackageNFPM_EndToEndDeb(t *testing.T) {
	tmpDir := t.TempDir()

	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	guiBin := filepath.Join(binDir, "simtx")
	_ = os.WriteFile(guiBin, []byte("#!/bin/sh\necho test\n"), 0755)

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:        "simtx",
			Description: "SimTX test package",
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
			Raw:    "0.1.0-alpha-12",
			Debian: "0.1.0~alpha12",
		},
		RootDir: tmpDir,
	}

	binaries := map[string]string{
		"simtx": guiBin,
	}

	artifacts, err := PackageNFPM(context.Background(), cfg, "deb", "amd64", binaries)
	if err != nil {
		t.Fatalf("PackageNFPM(deb) failed: %v", err)
	}

	if len(artifacts) == 0 {
		t.Fatalf("expected at least one artifact returned")
	}

	debPath := artifacts[0]
	if !strings.HasSuffix(debPath, ".deb") {
		t.Errorf("expected artifact to end with .deb, got %s", debPath)
	}

	fi, err := os.Stat(debPath)
	if err != nil {
		t.Fatalf("deb package file does not exist: %v", err)
	}
	if fi.Size() == 0 {
		t.Errorf("deb package is empty (0 bytes)")
	}
}

func TestSignArchPackage_PureGo(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Generate an in-memory OpenPGP key pair for testing
	entity, err := openpgp.NewEntity("SimTX Packager", "Testing Key", "packages@simtx.net", nil)
	if err != nil {
		t.Fatalf("failed to create openpgp entity: %v", err)
	}

	keyPath := filepath.Join(tmpDir, "test-key.asc")
	keyFile, err := os.Create(keyPath)
	if err != nil {
		t.Fatalf("failed to create key file: %v", err)
	}
	if err := entity.SerializePrivate(keyFile, nil); err != nil {
		t.Fatalf("failed to serialize private key: %v", err)
	}
	keyFile.Close()

	// 2. Create dummy package file
	pkgPath := filepath.Join(tmpDir, "simtx-0.1.0alpha12-1-x86_64.pkg.tar.zst")
	if err := os.WriteFile(pkgPath, []byte("dummy-package-archive-data"), 0644); err != nil {
		t.Fatalf("failed to write dummy package: %v", err)
	}

	// 3. Sign package
	sigPath, err := SignArchPackage(pkgPath, keyPath, "")
	if err != nil {
		t.Fatalf("SignArchPackage failed: %v", err)
	}

	if sigPath != pkgPath+".sig" {
		t.Errorf("expected sigPath %s, got %s", pkgPath+".sig", sigPath)
	}

	sigData, err := os.ReadFile(sigPath)
	if err != nil {
		t.Fatalf("failed to read signature file: %v", err)
	}

	if len(sigData) == 0 {
		t.Fatalf("signature file is empty")
	}

	// 4. Verify detached signature against the package file using the public keyring
	keyring := openpgp.EntityList{entity}
	pkgData, err := os.ReadFile(pkgPath)
	if err != nil {
		t.Fatalf("failed to read pkg file: %v", err)
	}

	signer, err := openpgp.CheckDetachedSignature(keyring, bytes.NewReader(pkgData), bytes.NewReader(sigData), nil)
	if err != nil {
		t.Fatalf("detached signature verification failed: %v", err)
	}

	if signer == nil {
		t.Errorf("expected non-nil signer after signature check")
	}
}

func TestBuildNFPMInfo_DebOnlyDefaults(t *testing.T) {
	tmpDir := t.TempDir()

	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	guiBin := filepath.Join(binDir, "simtx")
	_ = os.WriteFile(guiBin, []byte("#!/bin/sh\necho simtx\n"), 0755)

	postinstall := filepath.Join(tmpDir, "postinstall.sh")
	prerm := filepath.Join(tmpDir, "prerm.sh")
	postrm := filepath.Join(tmpDir, "postrm.sh")
	for _, p := range []string{postinstall, prerm, postrm} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
			t.Fatalf("failed to write %s: %v", p, err)
		}
	}

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:        "simtx",
			Description: "deb defaults test",
			OutputDir:   "bin",
		},
		Linux: config.LinuxConfig{
			Deb: config.LinuxDebConfig{
				Postinstall: postinstall,
				Prerm:       prerm,
				Postrm:      postrm,
			},
		},
		Version: &config.VersionInfo{
			Raw:    "1.0.0",
			Debian: "1.0.0",
			RPM:    "1.0.0",
			Arch:   "1.0.0",
		},
		RootDir: tmpDir,
	}
	binaries := map[string]string{"simtx": guiBin}

	debInfo, debCleanup, err := BuildNFPMInfo(cfg, "deb", "amd64", binaries)
	if err != nil {
		t.Fatalf("BuildNFPMInfo(deb) failed: %v", err)
	}
	defer debCleanup()

	if debInfo.Section != "" {
		t.Errorf("expected neutral empty deb section, got %q", debInfo.Section)
	}
	if debInfo.Priority != "optional" {
		t.Errorf("expected deb priority optional, got %q", debInfo.Priority)
	}
	if debInfo.Scripts.PostInstall != postinstall {
		t.Errorf("expected configured postinstall %q, got %q", postinstall, debInfo.Scripts.PostInstall)
	}
	if debInfo.Scripts.PreRemove != prerm {
		t.Errorf("expected configured prerm %q, got %q", prerm, debInfo.Scripts.PreRemove)
	}
	if debInfo.Scripts.PostRemove != postrm {
		t.Errorf("expected configured postrm %q, got %q", postrm, debInfo.Scripts.PostRemove)
	}
	if len(debInfo.Depends) != 2 || debInfo.Depends[0] != "libgtk-4-1" {
		t.Errorf("expected deb-only default depends, got %v", debInfo.Depends)
	}

	// RPM and archlinux must not inherit Debian scripts or deb-only metadata.
	for _, target := range []string{"rpm", "archlinux"} {
		info, cleanup, err := BuildNFPMInfo(cfg, target, "amd64", binaries)
		if err != nil {
			t.Fatalf("BuildNFPMInfo(%s) failed: %v", target, err)
		}
		if info.Scripts.PostInstall != "" || info.Scripts.PreRemove != "" || info.Scripts.PostRemove != "" {
			t.Errorf("%s inherited Debian maintainer scripts: %+v", target, info.Scripts)
		}
		if info.Section != "" {
			t.Errorf("expected empty %s section, got %q", target, info.Section)
		}
		if info.Priority != "" {
			t.Errorf("expected empty %s priority, got %q", target, info.Priority)
		}
		for _, dep := range info.Depends {
			if dep == "libgtk-4-1" || dep == "libwebkitgtk-6.0-4" {
				t.Errorf("%s inherited Debian dependency %q", target, dep)
			}
		}
		cleanup()
	}
}

func TestBuildNFPMInfo_ConfiguredMissingFilesFail(t *testing.T) {
	tmpDir := t.TempDir()

	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	guiBin := filepath.Join(binDir, "simtx")
	_ = os.WriteFile(guiBin, []byte("#!/bin/sh\n"), 0755)

	base := func() *config.Config {
		return &config.Config{
			Package: config.PackageConfig{
				Name:      "simtx",
				OutputDir: "bin",
			},
			Version: &config.VersionInfo{
				Raw:    "1.0.0",
				Debian: "1.0.0",
			},
			RootDir: tmpDir,
		}
	}
	binaries := map[string]string{"simtx": guiBin}

	t.Run("postinstall", func(t *testing.T) {
		cfg := base()
		cfg.Linux.Deb.Postinstall = "missing-postinstall.sh"
		if _, cleanup, err := BuildNFPMInfo(cfg, "deb", "amd64", binaries); err == nil {
			if cleanup != nil {
				cleanup()
			}
			t.Fatal("expected an error for a configured-but-missing postinstall")
		} else if !strings.Contains(err.Error(), "postinstall") {
			t.Fatalf("expected error to mention postinstall, got: %v", err)
		}
	})

	t.Run("postrm", func(t *testing.T) {
		cfg := base()
		cfg.Linux.Deb.Postrm = "missing-postrm.sh"
		if _, cleanup, err := BuildNFPMInfo(cfg, "deb", "amd64", binaries); err == nil {
			if cleanup != nil {
				cleanup()
			}
			t.Fatal("expected an error for a configured-but-missing postrm")
		} else if !strings.Contains(err.Error(), "postrm") {
			t.Fatalf("expected error to mention postrm, got: %v", err)
		}
	})

	t.Run("desktop_file", func(t *testing.T) {
		cfg := base()
		cfg.Linux.DesktopFile = "missing.desktop"
		if _, cleanup, err := BuildNFPMInfo(cfg, "deb", "amd64", binaries); err == nil {
			if cleanup != nil {
				cleanup()
			}
			t.Fatal("expected an error for a configured-but-missing desktop_file")
		} else if !strings.Contains(err.Error(), "desktop_file") {
			t.Fatalf("expected error to mention desktop_file, got: %v", err)
		}
	})
}

func TestPackageNFPM_ArchSigningFailureReturnsNoArtifacts(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	guiBin := filepath.Join(binDir, "simtx")
	_ = os.WriteFile(guiBin, []byte("#!/bin/sh\n"), 0755)

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:        "simtx",
			Description: "arch signing failure test",
			OutputDir:   "bin",
			Maintainer:  "SimTX <packages@simtx.net>",
			License:     "MIT",
		},
		Linux: config.LinuxConfig{
			Arch: config.LinuxArchConfig{
				Sign:   true,
				GPGKey: "missing-key.asc",
			},
		},
		Version: &config.VersionInfo{
			Raw:  "1.0.0",
			Arch: "1.0.0",
		},
		RootDir: tmpDir,
	}

	artifacts, err := PackageNFPM(context.Background(), cfg, "archlinux", "amd64", map[string]string{"simtx": guiBin})
	if err == nil {
		t.Fatal("expected signing failure")
	}
	if artifacts != nil {
		t.Errorf("expected nil artifacts on signing failure, got %v", artifacts)
	}
}

func TestBuildNFPMInfo_NonPNGIconUsesPlaceholder(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	guiBin := filepath.Join(binDir, "simtx")
	_ = os.WriteFile(guiBin, []byte("#!/bin/sh\n"), 0755)
	iconPath := filepath.Join(tmpDir, "appicon.svg")
	_ = os.WriteFile(iconPath, []byte("<svg></svg>"), 0644)

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:      "simtx",
			OutputDir: "bin",
			Icon:      iconPath,
		},
		Version: &config.VersionInfo{Raw: "1.0.0", Debian: "1.0.0"},
		RootDir: tmpDir,
	}

	info, cleanup, err := BuildNFPMInfo(cfg, "deb", "amd64", map[string]string{"simtx": guiBin})
	if err != nil {
		t.Fatalf("BuildNFPMInfo failed: %v", err)
	}
	defer cleanup()

	for _, c := range info.Contents {
		if c.Destination == "/usr/share/icons/hicolor/128x128/apps/simtx.png" {
			if strings.HasSuffix(c.Source, ".svg") {
				t.Fatalf("non-PNG icon was installed as PNG: %s", c.Source)
			}
			data, err := os.ReadFile(c.Source)
			if err != nil {
				t.Fatalf("failed to read staged icon: %v", err)
			}
			if !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
				t.Errorf("installed icon is not a PNG")
			}
			return
		}
	}
	t.Fatal("icon content mapping not found")
}
