package linux

import (
	"archive/tar"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/simtxng/cook/pkg/config"
	"howett.net/plist"
)

func TestVoidArch(t *testing.T) {
	tests := map[string]string{
		"amd64":   "x86_64",
		"arm64":   "aarch64",
		"386":     "i686",
		"arm":     "armv7l",
		"riscv64": "riscv64",
		"ppc64le": "ppc64le",
	}
	for in, want := range tests {
		if got := voidArch(in); got != want {
			t.Errorf("voidArch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVoidPkgver(t *testing.T) {
	cfg := &config.Config{Package: config.PackageConfig{Name: "simtx"}}

	got, err := voidPkgver(cfg, "0.1.0alpha12", 3)
	if err != nil {
		t.Fatalf("voidPkgver returned error: %v", err)
	}
	if got != "simtx-0.1.0alpha12_3" {
		t.Errorf("voidPkgver = %q, want %q", got, "simtx-0.1.0alpha12_3")
	}

	for _, bad := range []string{"0.1.0-alpha", "0.1.0_alpha", ""} {
		if _, err := voidPkgver(cfg, bad, 1); err == nil {
			t.Errorf("voidPkgver(%q) expected error, got nil", bad)
		}
	}
}

// voidTestConfig builds a minimal Void packaging configuration with staged
// binaries in a temporary directory.
func voidTestConfig(t *testing.T) (*config.Config, map[string]string) {
	t.Helper()
	tmpDir := t.TempDir()

	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	guiBin := filepath.Join(binDir, "simtx")
	if err := os.WriteFile(guiBin, []byte("#!/bin/sh\necho simtx\n"), 0755); err != nil {
		t.Fatalf("write gui bin: %v", err)
	}
	cliBin := filepath.Join(binDir, "simtx-cli")
	if err := os.WriteFile(cliBin, []byte("#!/bin/sh\necho simtx-cli\n"), 0755); err != nil {
		t.Fatalf("write cli bin: %v", err)
	}
	iconPath := filepath.Join(tmpDir, "appicon.png")
	if err := os.WriteFile(iconPath, []byte("fake-png-data"), 0644); err != nil {
		t.Fatalf("write icon: %v", err)
	}

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
			Void: config.LinuxVoidConfig{
				Depends:      []string{"gtk4", "libwebkitgtk60"},
				Revision:     2,
				Tags:         "development",
				ConfFiles:    []string{"usr/share/applications/simtx.desktop"},
				MutableFiles: []string{"/usr/share/icons/hicolor/128x128/apps/simtx.png"},
			},
		},
		Version: &config.VersionInfo{
			Raw:  "0.1.0-alpha-12",
			Void: "0.1.0alpha12",
		},
		RootDir: tmpDir,
	}

	binaries := map[string]string{
		"simtx":     guiBin,
		"simtx-cli": cliBin,
	}
	return cfg, binaries
}

func readVoidArchive(t *testing.T, path string) map[string][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open package: %v", err)
	}
	defer f.Close()

	zr, err := zstd.NewReader(f)
	if err != nil {
		t.Fatalf("zstd reader: %v", err)
	}
	defer zr.Close()

	entries := map[string][]byte{}
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		entries[hdr.Name] = data
	}
	return entries
}

func TestPackageXBPS_EndToEnd(t *testing.T) {
	cfg, binaries := voidTestConfig(t)

	artifacts, err := PackageXBPS(context.Background(), cfg, "amd64", binaries)
	if err != nil {
		t.Fatalf("PackageXBPS failed: %v", err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d: %v", len(artifacts), artifacts)
	}

	pkgPath := artifacts[0]
	if base := filepath.Base(pkgPath); base != "simtx-0.1.0alpha12_2.x86_64.xbps" {
		t.Errorf("unexpected package filename %q", base)
	}

	entries := readVoidArchive(t, pkgPath)
	for _, want := range []string{
		"./props.plist",
		"./files.plist",
		"./usr/bin/simtx",
		"./usr/bin/simtx-cli",
		"./usr/share/applications/simtx.desktop",
		"./usr/share/icons/hicolor/128x128/apps/simtx.png",
	} {
		if _, ok := entries[want]; !ok {
			t.Errorf("missing archive entry %s (have: %v)", want, entryNames(entries))
		}
	}

	var props map[string]interface{}
	if _, err := plist.Unmarshal(entries["./props.plist"], &props); err != nil {
		t.Fatalf("parse props.plist: %v", err)
	}
	checks := map[string]string{
		"architecture": "x86_64",
		"pkgname":      "simtx",
		"version":      "0.1.0alpha12",
		"pkgver":       "simtx-0.1.0alpha12_2",
		"short_desc":   "SimTX desktop application",
		"license":      "MIT",
		"maintainer":   "SimTX Maintainers <packages@simtx.net>",
		"homepage":     "https://simtx.net",
		"tags":         "development",
	}
	for key, want := range checks {
		if got, _ := props[key].(string); got != want {
			t.Errorf("props[%q] = %q, want %q", key, got, want)
		}
	}
	if size, _ := props["installed_size"].(uint64); size == 0 {
		t.Errorf("props[installed_size] should be non-zero")
	}
	if deps := toStringSlice(props["run_depends"]); strings.Join(deps, ",") != "gtk4>=0,libwebkitgtk60>=0" {
		t.Errorf("props[run_depends] = %v", deps)
	}
	if confs := toStringSlice(props["conf_files"]); len(confs) != 1 || confs[0] != "/usr/share/applications/simtx.desktop" {
		t.Errorf("props[conf_files] = %v", confs)
	}

	var files map[string]interface{}
	if _, err := plist.Unmarshal(entries["./files.plist"], &files); err != nil {
		t.Fatalf("parse files.plist: %v", err)
	}
	filesArr := toDictSlice(files["files"])
	confArr := toDictSlice(files["conf_files"])
	dirsArr := toDictSlice(files["dirs"])

	binEntry := findEntry(filesArr, "/usr/bin/simtx")
	if binEntry == nil {
		t.Fatalf("files.plist missing /usr/bin/simtx")
	}
	if sha, _ := binEntry["sha256"].(string); len(sha) != 64 {
		t.Errorf("expected 64-char sha256, got %q", sha)
	}
	if size, _ := binEntry["size"].(uint64); size == 0 {
		t.Errorf("expected non-zero size for /usr/bin/simtx")
	}

	if len(confArr) != 1 {
		t.Fatalf("expected 1 conf_files entry, got %d", len(confArr))
	}
	if file, _ := confArr[0]["file"].(string); file != "/usr/share/applications/simtx.desktop" {
		t.Errorf("unexpected conf_files entry: %v", confArr)
	}

	iconEntry := findEntry(filesArr, "/usr/share/icons/hicolor/128x128/apps/simtx.png")
	if iconEntry == nil {
		t.Fatalf("files.plist missing icon entry")
	}
	if mutable, _ := iconEntry["mutable"].(bool); !mutable {
		t.Errorf("expected icon entry to be mutable")
	}

	if findEntry(dirsArr, "/usr/bin") == nil || findEntry(dirsArr, "/usr/share/applications") == nil {
		t.Errorf("files.plist dirs missing expected directories: %v", dirsArr)
	}

	if _, ok := entries["./INSTALL"]; ok {
		t.Errorf("did not expect INSTALL script when none configured")
	}
}

func TestPackageXBPS_MaintainerScripts(t *testing.T) {
	cfg, binaries := voidTestConfig(t)

	install := filepath.Join(cfg.RootDir, "INSTALL")
	remove := filepath.Join(cfg.RootDir, "REMOVE")
	if err := os.WriteFile(install, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write INSTALL: %v", err)
	}
	if err := os.WriteFile(remove, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write REMOVE: %v", err)
	}
	cfg.Linux.Void.InstallScript = "INSTALL"
	cfg.Linux.Void.RemoveScript = "REMOVE"

	artifacts, err := PackageXBPS(context.Background(), cfg, "amd64", binaries)
	if err != nil {
		t.Fatalf("PackageXBPS failed: %v", err)
	}
	entries := readVoidArchive(t, artifacts[0])
	if _, ok := entries["./INSTALL"]; !ok {
		t.Errorf("expected ./INSTALL in archive")
	}
	if _, ok := entries["./REMOVE"]; !ok {
		t.Errorf("expected ./REMOVE in archive")
	}
}

func TestPackageXBPS_MissingScriptIsError(t *testing.T) {
	cfg, binaries := voidTestConfig(t)
	cfg.Linux.Void.InstallScript = "build/does-not-exist.sh"

	if _, err := PackageXBPS(context.Background(), cfg, "amd64", binaries); err == nil {
		t.Fatalf("expected error for missing configured INSTALL script, got nil")
	}
}

func TestPackageXBPS_FormatsRawVersion(t *testing.T) {
	cfg, binaries := voidTestConfig(t)
	// Drop the pre-resolved version so PackageXBPS falls back to formatting the
	// raw "0.1.0-alpha-12" (and its default revision).
	cfg.Package.Version = "0.1.0-alpha-12"
	cfg.Version = nil
	cfg.Linux.Void.Revision = 0

	artifacts, err := PackageXBPS(context.Background(), cfg, "amd64", binaries)
	if err != nil {
		t.Fatalf("PackageXBPS failed: %v", err)
	}
	if base := filepath.Base(artifacts[0]); base != "simtx-0.1.0alpha12_1.x86_64.xbps" {
		t.Errorf("unexpected package filename %q", base)
	}
}

func TestNormalizeVoidPkgSpecs(t *testing.T) {
	tests := map[string]string{
		"gtk4":               "gtk4>=0",
		"libwebkitgtk60":     "libwebkitgtk60>=0",
		"hicolor-icon-theme": "hicolor-icon-theme>=0",
		"gtk4>=1.0":          "gtk4>=1.0",
		"gtk4<2.0_1":         "gtk4<2.0_1",
		"foo-1.2.3_1":        "foo-1.2.3_1",
		"libgtk-4.so.1":      "libgtk-4.so.1>=0",
	}
	for in, want := range tests {
		if got := normalizeVoidPkgSpec(in); got != want {
			t.Errorf("normalizeVoidPkgSpec(%q) = %q, want %q", in, got, want)
		}
	}

	got := normalizeVoidPkgSpecs([]string{"gtk4", "  ", "libwebkitgtk60"})
	if strings.Join(got, ",") != "gtk4>=0,libwebkitgtk60>=0" {
		t.Errorf("normalizeVoidPkgSpecs = %v", got)
	}
}

func TestPackageXBPS_NormalizesConstraints(t *testing.T) {
	cfg, binaries := voidTestConfig(t)
	cfg.Linux.Void.Depends = []string{"gtk4"}
	cfg.Linux.Void.Conflicts = []string{"simtx-legacy"}
	cfg.Linux.Void.Replaces = []string{"simtx-old>=0", "simtx-ancient"}
	cfg.Linux.Void.Provides = []string{"simtx-virtual"}

	artifacts, err := PackageXBPS(context.Background(), cfg, "amd64", binaries)
	if err != nil {
		t.Fatalf("PackageXBPS failed: %v", err)
	}
	entries := readVoidArchive(t, artifacts[0])

	var props map[string]interface{}
	if _, err := plist.Unmarshal(entries["./props.plist"], &props); err != nil {
		t.Fatalf("parse props.plist: %v", err)
	}
	if deps := strings.Join(toStringSlice(props["run_depends"]), ","); deps != "gtk4>=0" {
		t.Errorf("run_depends = %q", deps)
	}
	if c := strings.Join(toStringSlice(props["conflicts"]), ","); c != "simtx-legacy>=0" {
		t.Errorf("conflicts = %q", c)
	}
	if r := strings.Join(toStringSlice(props["replaces"]), ","); r != "simtx-old>=0,simtx-ancient>=0" {
		t.Errorf("replaces = %q", r)
	}
	if p := strings.Join(toStringSlice(props["provides"]), ","); p != "simtx-virtual" {
		t.Errorf("provides = %q, want unchanged virtual name", p)
	}
}

func TestLinuxPackager_VoidAlias(t *testing.T) {
	cfg, binaries := voidTestConfig(t)

	artifacts, err := NewPackager().Package(context.Background(), cfg, "xbps", "amd64", binaries)
	if err != nil {
		t.Fatalf("Package(xbps alias) failed: %v", err)
	}
	if len(artifacts) != 1 || !strings.HasSuffix(artifacts[0], ".xbps") {
		t.Fatalf("expected a single .xbps artifact, got %v", artifacts)
	}
}

func entryNames(entries map[string][]byte) []string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	return names
}

func toStringSlice(v interface{}) []string {
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func toDictSlice(v interface{}) []map[string]interface{} {
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(arr))
	for _, item := range arr {
		if d, ok := item.(map[string]interface{}); ok {
			out = append(out, d)
		}
	}
	return out
}

func findEntry(entries []map[string]interface{}, file string) map[string]interface{} {
	for _, e := range entries {
		if f, _ := e["file"].(string); f == file {
			return e
		}
	}
	return nil
}
