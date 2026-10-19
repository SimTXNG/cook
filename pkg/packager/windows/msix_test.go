package windows

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/simtxng/cook/pkg/config"
)

func TestBuildMSIX(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cook-msix-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	guiPath := filepath.Join(tmpDir, "simtx.exe")
	// The configured key omits ".exe" and its name does not contain "cli", so
	// only the cfg.Windows.BundledCLI lookup can select it. The decoy would be
	// chosen by the "contains cli" heuristic if the configured path regressed.
	cliPath := filepath.Join(tmpDir, "stx.exe")
	decoyPath := filepath.Join(tmpDir, "simtx-cli.exe")
	_ = os.WriteFile(guiPath, []byte("fake-gui-binary-content"), 0755)
	_ = os.WriteFile(cliPath, []byte("fake-cli-binary-content"), 0755)
	_ = os.WriteFile(decoyPath, []byte("fake-decoy-binary-content"), 0755)

	binaries := map[string]string{
		"simtx":     guiPath,
		"stx":       cliPath,
		"simtx-cli": decoyPath,
	}

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:        "simtx",
			ProductName: "SimTX Desktop",
			Company:     "SimTX Org",
			Description: "SimTX Desktop Application",
			Identifier:  "net.simtx.app",
			OutputDir:   "bin",
		},
		Windows: config.WindowsConfig{
			BundledCLI: "stx",
			Signing: config.WindowsSignConfig{
				Publisher: "CN=SimTX Signing",
			},
		},
		Version: &config.VersionInfo{
			Raw:     "0.1.0-alpha-12",
			Windows: "0.1.0.0",
		},
		RootDir: tmpDir,
	}

	if got := findCLIBinary(cfg, binaries); got != cliPath {
		t.Fatalf("expected configured CLI %s, got %s", cliPath, got)
	}

	res, err := BuildMSIX(context.Background(), cfg, "amd64", binaries)
	if err != nil {
		t.Fatalf("BuildMSIX failed: %v", err)
	}

	if len(res) == 0 {
		t.Fatalf("expected at least one MSIX output file")
	}

	msixPath := res[0]
	if !fileExists(msixPath) {
		t.Fatalf("MSIX file does not exist at %s", msixPath)
	}

	// Verify MSIX is a valid ZIP containing required Appx artifacts
	data, err := os.ReadFile(msixPath)
	if err != nil {
		t.Fatal(err)
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("MSIX is not a valid zip archive: %v", err)
	}

	filesInArchive := make(map[string][]byte)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, _ := io.ReadAll(rc)
		rc.Close()
		filesInArchive[f.Name] = content
	}

	// Check core MSIX structure
	requiredFiles := []string{
		"AppxManifest.xml",
		"[Content_Types].xml",
		"AppxBlockMap.xml",
		"simtx.exe",
		"stx.exe",
	}

	for _, rf := range requiredFiles {
		if _, ok := filesInArchive[rf]; !ok {
			t.Errorf("missing expected file %s in MSIX archive", rf)
		}
	}
	if _, ok := filesInArchive["simtx-cli.exe"]; ok {
		t.Errorf("MSIX bundled the heuristic CLI decoy instead of the configured CLI")
	}
	if got := string(filesInArchive["stx.exe"]); got != "fake-cli-binary-content" {
		t.Errorf("expected configured CLI contents in MSIX, got %q", got)
	}

	// Inspect AppxManifest.xml
	manifestXML := string(filesInArchive["AppxManifest.xml"])
	if !strings.Contains(manifestXML, "simtx.exe") {
		t.Errorf("AppxManifest.xml missing executable reference: %s", manifestXML)
	}
	if !strings.Contains(manifestXML, "runFullTrust") {
		t.Errorf("AppxManifest.xml missing runFullTrust capability: %s", manifestXML)
	}
	if !strings.Contains(manifestXML, "SimTX Desktop") {
		t.Errorf("AppxManifest.xml missing DisplayName: %s", manifestXML)
	}
	if !strings.Contains(manifestXML, "CN=SimTX Signing") {
		t.Errorf("AppxManifest.xml missing configured Publisher: %s", manifestXML)
	}
	if !strings.Contains(manifestXML, `ProcessorArchitecture="x64"`) {
		t.Errorf("AppxManifest.xml missing x64 processor architecture: %s", manifestXML)
	}
}

func TestFindCLIBinaryConfiguredBundledCLI(t *testing.T) {
	cases := []struct {
		name       string
		bundledCLI string
	}{
		{"configured key without exe suffix", "simtx-cli"},
		{"configured name without cli substring", "stx"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configured := filepath.Join(tmpDir, tc.bundledCLI+".exe")
			decoy := filepath.Join(tmpDir, "decoy-cli.exe")
			_ = os.WriteFile(configured, []byte("configured"), 0755)
			_ = os.WriteFile(decoy, []byte("decoy"), 0755)

			cfg := &config.Config{
				Windows: config.WindowsConfig{BundledCLI: tc.bundledCLI},
			}
			binaries := map[string]string{
				tc.bundledCLI: configured,
				"decoy-cli":   decoy,
			}

			if got := findCLIBinary(cfg, binaries); got != configured {
				t.Errorf("expected configured CLI %s, got %s", configured, got)
			}
		})
	}
}

func TestMSIXIdentityVersionForcesRevisionZero(t *testing.T) {
	cfg := &config.Config{
		Version: &config.VersionInfo{
			Raw:     "1.2.3.4",
			Windows: "1.2.3.4",
		},
	}
	if got := msixIdentityVersion(cfg); got != "1.2.3.0" {
		t.Errorf("expected msix identity version 1.2.3.0, got %q", got)
	}
}

func TestValidatePackageName(t *testing.T) {
	valid := []string{"net.simtx.app", "simtx", "com.example.app-1"}
	for _, name := range valid {
		if err := validatePackageName(name); err != nil {
			t.Errorf("expected %q valid, got error: %v", name, err)
		}
	}

	invalid := []string{"ab", "-simtx", "simtx-", ".simtx", "simtx.", "a..b", "simtx app", "simtx_", strings.Repeat("a", 51)}
	for _, name := range invalid {
		if err := validatePackageName(name); err == nil {
			t.Errorf("expected %q invalid, got nil", name)
		}
	}
}

func TestSanitizePackageNameCollapsesDots(t *testing.T) {
	if got := sanitizePackageName("my  app!"); got != "my.app" {
		t.Errorf("expected my.app, got %q", got)
	}
	if got := sanitizePackageName("!!!"); got != "App" {
		t.Errorf("expected App fallback, got %q", got)
	}
}

func TestBuildMSIXRevisionZero(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cook-msix-rev-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	guiPath := filepath.Join(tmpDir, "simtx.exe")
	if err := os.WriteFile(guiPath, []byte("fake-gui"), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:       "simtx",
			Identifier: "net.simtx.app",
			OutputDir:  "bin",
		},
		Version: &config.VersionInfo{
			Raw:     "1.2.3.4",
			Windows: "1.2.3.4",
		},
		RootDir: tmpDir,
	}

	res, err := BuildMSIX(context.Background(), cfg, "amd64", map[string]string{"simtx": guiPath})
	if err != nil {
		t.Fatalf("BuildMSIX failed: %v", err)
	}
	manifestXML := readArchiveFile(t, res[0], "AppxManifest.xml")
	if !strings.Contains(manifestXML, `Version="1.2.3.0"`) {
		t.Errorf("expected identity version 1.2.3.0 in manifest: %s", manifestXML)
	}
}

func TestBuildMSIXRejectsInvalidIdentifier(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cook-msix-badid-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	guiPath := filepath.Join(tmpDir, "simtx.exe")
	if err := os.WriteFile(guiPath, []byte("fake-gui"), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:       "simtx",
			Identifier: "ab",
			OutputDir:  "bin",
		},
		Version: &config.VersionInfo{Raw: "1.0.0", Windows: "1.0.0.0"},
		RootDir: tmpDir,
	}

	if _, err := BuildMSIX(context.Background(), cfg, "amd64", map[string]string{"simtx": guiPath}); err == nil {
		t.Fatalf("expected error for invalid package identifier")
	}
}

func TestBuildMSIXUsesConfiguredIcon(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cook-msix-icon-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	guiPath := filepath.Join(tmpDir, "simtx.exe")
	if err := os.WriteFile(guiPath, []byte("fake-gui"), 0755); err != nil {
		t.Fatal(err)
	}

	iconPath := filepath.Join(tmpDir, "icon.png")
	iconImg := image.NewRGBA(image.Rect(0, 0, 1, 1))
	iconImg.Set(0, 0, color.RGBA{R: 0xFF, A: 0xFF})
	f, err := os.Create(iconPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, iconImg); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:       "simtx",
			Identifier: "net.simtx.app",
			Icon:       "icon.png",
			OutputDir:  "bin",
		},
		Version: &config.VersionInfo{Raw: "1.0.0", Windows: "1.0.0.0"},
		RootDir: tmpDir,
	}

	res, err := BuildMSIX(context.Background(), cfg, "amd64", map[string]string{"simtx": guiPath})
	if err != nil {
		t.Fatalf("BuildMSIX failed: %v", err)
	}

	logo := readArchiveBytes(t, res[0], "Assets/Square150x150Logo.png")
	img, err := png.Decode(bytes.NewReader(logo))
	if err != nil {
		t.Fatalf("failed to decode logo: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 150 || b.Dy() != 150 {
		t.Errorf("expected 150x150 logo, got %dx%d", b.Dx(), b.Dy())
	}
	r, g, _, _ := img.At(0, 0).RGBA()
	if r>>8 < 200 || g>>8 > 60 {
		t.Errorf("expected configured red icon to be used, got r=%d g=%d", r>>8, g>>8)
	}
}

func readArchiveFile(t *testing.T, msixPath, name string) string {
	t.Helper()
	return string(readArchiveBytes(t, msixPath, name))
}

func readArchiveBytes(t *testing.T, msixPath, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(msixPath)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		content, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		return content
	}
	t.Fatalf("file %s not found in MSIX archive", name)
	return nil
}
