package darwin

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"howett.net/plist"

	"github.com/simtxng/cook/pkg/config"
)

func TestAssembleAppBundle(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cook-bundle-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	guiPath := filepath.Join(tmpDir, "simtx")
	cliPath := filepath.Join(tmpDir, "simtx-cli")
	_ = os.WriteFile(guiPath, []byte("fake-gui-binary"), 0755)
	_ = os.WriteFile(cliPath, []byte("fake-cli-binary"), 0755)

	binaries := map[string]string{
		"simtx":     guiPath,
		"simtx-cli": cliPath,
	}

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:        "simtx",
			ProductName: "SimTX",
			Company:     "SimTX Org",
			Description: "SimTX Application",
			Identifier:  "net.simtx.app",
			OutputDir:   "bin",
		},
		Darwin: config.DarwinConfig{
			MinOSVersion: "12.0",
		},
		Binaries: []config.BinaryConfig{
			{Name: "simtx", GUI: true},
			{Name: "simtx-cli"},
		},
		Version: &config.VersionInfo{
			Raw: "0.1.0-alpha-12",
		},
		RootDir: tmpDir,
	}

	appPath, err := AssembleAppBundle(cfg, binaries, AppBundleOptions{DevMode: false})
	if err != nil {
		t.Fatalf("AssembleAppBundle failed: %v", err)
	}

	if !fileExists(appPath) && !dirExists(appPath) {
		t.Fatalf("app bundle does not exist at %s", appPath)
	}

	// Verify Contents/Resources exists.
	resourcesPath := filepath.Join(appPath, "Contents", "Resources")
	if !dirExists(resourcesPath) {
		t.Errorf("Contents/Resources directory missing from bundle at %s", resourcesPath)
	}

	// Verify Contents/MacOS executables
	destGUI := filepath.Join(appPath, "Contents", "MacOS", "simtx")
	destCLI := filepath.Join(appPath, "Contents", "MacOS", "simtx-cli")

	fiGUI, err := os.Stat(destGUI)
	if err != nil {
		t.Fatalf("GUI executable not found in bundle: %v", err)
	}
	if fiGUI.Mode()&0111 == 0 {
		t.Errorf("GUI executable is not marked executable: %v", fiGUI.Mode())
	}

	fiCLI, err := os.Stat(destCLI)
	if err != nil {
		t.Fatalf("companion CLI executable not found in bundle: %v", err)
	}
	if fiCLI.Mode()&0111 == 0 {
		t.Errorf("companion CLI executable is not marked executable: %v", fiCLI.Mode())
	}

	// Verify Info.plist
	infoPlistPath := filepath.Join(appPath, "Contents", "Info.plist")
	plistData, err := os.ReadFile(infoPlistPath)
	if err != nil {
		t.Fatalf("Info.plist missing from bundle: %v", err)
	}

	var m map[string]interface{}
	decoder := plist.NewDecoder(bytes.NewReader(plistData))
	if err := decoder.Decode(&m); err != nil {
		t.Fatalf("failed to parse Info.plist: %v", err)
	}

	if m["CFBundleExecutable"] != "simtx" {
		t.Errorf("expected CFBundleExecutable=simtx, got %v", m["CFBundleExecutable"])
	}
	if m["CFBundleIdentifier"] != "net.simtx.app" {
		t.Errorf("expected CFBundleIdentifier=net.simtx.app, got %v", m["CFBundleIdentifier"])
	}
	if m["CFBundleShortVersionString"] != "0.1.0-alpha-12" {
		t.Errorf("expected version 0.1.0-alpha-12, got %v", m["CFBundleShortVersionString"])
	}
	if m["CFBundleVersion"] != "0.1.0.0" {
		t.Errorf("expected numeric CFBundleVersion 0.1.0.0, got %v", m["CFBundleVersion"])
	}

	// Verify critical audio entitlement
	micDesc, ok := m["NSMicrophoneUsageDescription"]
	if !ok || micDesc == "" {
		t.Errorf("NSMicrophoneUsageDescription missing in Info.plist!")
	}
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func TestAssembleAppBundleConfiguredInfoPlistDecodeError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cook-bundle-plist-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	guiPath := filepath.Join(tmpDir, "simtx")
	_ = os.WriteFile(guiPath, []byte("fake-gui-binary"), 0755)

	badTemplate := filepath.Join(tmpDir, "bad.plist")
	if err := os.WriteFile(badTemplate, []byte("this is not a plist"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:      "simtx",
			OutputDir: "bin",
		},
		Darwin: config.DarwinConfig{
			InfoPlist: badTemplate,
		},
		RootDir: tmpDir,
	}

	if _, err := AssembleAppBundle(cfg, map[string]string{"simtx": guiPath}, AppBundleOptions{}); err == nil {
		t.Fatalf("expected error for a malformed user-configured Info.plist template")
	}
}

func TestAssembleAppBundleConfiguredFilesMissing(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cook-bundle-missing-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	guiPath := filepath.Join(tmpDir, "simtx")
	_ = os.WriteFile(guiPath, []byte("fake-gui-binary"), 0755)

	tests := []struct {
		name   string
		darwin config.DarwinConfig
	}{
		{name: "icons", darwin: config.DarwinConfig{Icons: "missing.icns"}},
		{name: "assets car", darwin: config.DarwinConfig{AssetsCar: "missing/Assets.car"}},
		{name: "info plist", darwin: config.DarwinConfig{InfoPlist: "missing.plist"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{
				Package: config.PackageConfig{
					Name:      "simtx",
					OutputDir: "bin",
				},
				Darwin:  tc.darwin,
				RootDir: tmpDir,
			}
			if _, err := AssembleAppBundle(cfg, map[string]string{"simtx": guiPath}, AppBundleOptions{}); err == nil {
				t.Fatalf("expected error for missing configured %s", tc.name)
			}
		})
	}
}

func TestFindDarwinCLIBinaryDoesNotReuseGUI(t *testing.T) {
	tmpDir := t.TempDir()
	guiPath := filepath.Join(tmpDir, "simtx")
	cliPath := filepath.Join(tmpDir, "simtx-cli")
	for _, p := range []string{guiPath, cliPath} {
		if err := os.WriteFile(p, []byte("binary"), 0755); err != nil {
			t.Fatal(err)
		}
	}

	// Nothing is marked as GUI: the main binary must not be bundled as the CLI.
	cfg := &config.Config{
		Binaries: []config.BinaryConfig{{Name: "simtx"}},
	}
	if got := findDarwinCLIBinary(cfg, map[string]string{"simtx": guiPath}, guiPath); got != "" {
		t.Fatalf("expected no CLI binary, got %s", got)
	}

	// An explicitly configured non-GUI binary is selected, in config order.
	cfg.Binaries = []config.BinaryConfig{{Name: "simtx"}, {Name: "simtx-cli"}}
	got := findDarwinCLIBinary(cfg, map[string]string{"simtx": guiPath, "simtx-cli": cliPath}, guiPath)
	if got != cliPath {
		t.Fatalf("expected CLI %s, got %s", cliPath, got)
	}
}

func TestFindDarwinGUIBinaryDeterministicConfigOrder(t *testing.T) {
	tmpDir := t.TempDir()
	first := filepath.Join(tmpDir, "first")
	second := filepath.Join(tmpDir, "second")
	for _, p := range []string{first, second} {
		if err := os.WriteFile(p, []byte("binary"), 0755); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.Config{
		Binaries: []config.BinaryConfig{{Name: "first"}, {Name: "second"}},
	}
	binaries := map[string]string{"first": first, "second": second}

	want := first
	for i := 0; i < 20; i++ {
		if got := findDarwinGUIBinary(cfg, binaries); got != want {
			t.Fatalf("selection was not deterministic: got %s, want %s", got, want)
		}
	}
}
