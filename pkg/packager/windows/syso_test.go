package windows

import (
	"debug/pe"
	"os"
	"path/filepath"
	"testing"

	"github.com/simtxng/cook/pkg/config"
	"github.com/tc-hib/winres"
)

func TestParseVersion4(t *testing.T) {
	tests := []struct {
		input    string
		expected [4]uint16
	}{
		{"0.1.0-alpha-12", [4]uint16{0, 1, 0, 0}},
		{"1.2.3.4", [4]uint16{1, 2, 3, 4}},
		{"2.5", [4]uint16{2, 5, 0, 0}},
		{"10", [4]uint16{10, 0, 0, 0}},
		{"", [4]uint16{1, 0, 0, 0}},
	}

	for _, tc := range tests {
		got := ParseVersion4(tc.input)
		if got != tc.expected {
			t.Errorf("ParseVersion4(%q) = %v, expected %v", tc.input, got, tc.expected)
		}
	}
}

func TestParseArch(t *testing.T) {
	if ParseArch("amd64") != winres.ArchAMD64 {
		t.Errorf("expected ArchAMD64")
	}
	if ParseArch("arm64") != winres.ArchARM64 {
		t.Errorf("expected ArchARM64")
	}
	if ParseArch("386") != winres.ArchI386 {
		t.Errorf("expected ArchI386")
	}
}

func TestCompileSyso(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cook-syso-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:        "simtx",
			ProductName: "SimTX Desktop",
			Company:     "SimTX Org",
			Description: "Test Description",
			Copyright:   "© 2026 SimTX",
			Identifier:  "net.simtx.app",
			OutputDir:   "bin",
		},
		Version: &config.VersionInfo{
			Raw:     "0.1.0-alpha-12",
			Windows: "0.1.0",
		},
		RootDir: tmpDir,
	}

	sysoPath, err := CompileSyso(cfg, "amd64")
	if err != nil {
		t.Fatalf("CompileSyso failed: %v", err)
	}

	if !fileExists(sysoPath) {
		t.Fatalf("syso file not created at %s", sysoPath)
	}

	// Verify the compiled file is a valid PE COFF object
	f, err := pe.Open(sysoPath)
	if err != nil {
		t.Fatalf("failed to open generated syso as PE file: %v", err)
	}
	defer f.Close()

	if f.FileHeader.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		t.Errorf("expected AMD64 machine type (0x%x), got 0x%x", pe.IMAGE_FILE_MACHINE_AMD64, f.FileHeader.Machine)
	}

	foundRsrc := false
	for _, s := range f.Sections {
		if s.Name == ".rsrc" {
			foundRsrc = true
			break
		}
	}
	if !foundRsrc {
		t.Errorf("expected .rsrc section in syso file")
	}
}

func TestCompileSysoWithInfoJSON(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cook-syso-infojson-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	infoJSONContent := `{
		"fixed": {
			"file_version": "0.2.0.0"
		},
		"info": {
			"0000": {
				"ProductName": "Custom SimTX",
				"CompanyName": "Custom Org"
			}
		}
	}`
	infoPath := filepath.Join(tmpDir, "info.json")
	if err := os.WriteFile(infoPath, []byte(infoJSONContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name: "simtx",
		},
		Windows: config.WindowsConfig{
			Syso: config.WindowsSysoConfig{
				InfoJSON: "info.json",
			},
		},
		Version: &config.VersionInfo{
			Raw:     "0.2.0",
			Windows: "0.2.0.0",
		},
		RootDir: tmpDir,
	}

	sysoPath, err := CompileSyso(cfg, "amd64")
	if err != nil {
		t.Fatalf("CompileSyso failed: %v", err)
	}

	if !fileExists(sysoPath) {
		t.Fatalf("syso file not created at %s", sysoPath)
	}
}

func TestCompileSysoMissingConfiguredFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cook-syso-missing-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	base := func() *config.Config {
		return &config.Config{
			Package: config.PackageConfig{Name: "simtx"},
			Version: &config.VersionInfo{Raw: "1.0.0", Windows: "1.0.0.0"},
			RootDir: tmpDir,
		}
	}

	cfgIcon := base()
	cfgIcon.Windows.Syso.Icon = "missing.ico"
	if _, err := CompileSyso(cfgIcon, "amd64"); err == nil {
		t.Errorf("expected error for configured but missing icon")
	}

	cfgManifest := base()
	cfgManifest.Windows.Syso.Manifest = "missing.manifest"
	if _, err := CompileSyso(cfgManifest, "amd64"); err == nil {
		t.Errorf("expected error for configured but missing manifest")
	}

	cfgInfo := base()
	cfgInfo.Windows.Syso.InfoJSON = "missing.json"
	if _, err := CompileSyso(cfgInfo, "amd64"); err == nil {
		t.Errorf("expected error for configured but missing info_json")
	}
}

func TestCompileSysoInvalidInfoJSON(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cook-syso-badjson-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.WriteFile(filepath.Join(tmpDir, "info.json"), []byte("{ not valid json"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Package: config.PackageConfig{Name: "simtx"},
		Windows: config.WindowsConfig{
			Syso: config.WindowsSysoConfig{InfoJSON: "info.json"},
		},
		Version: &config.VersionInfo{Raw: "1.0.0", Windows: "1.0.0.0"},
		RootDir: tmpDir,
	}

	if _, err := CompileSyso(cfg, "amd64"); err == nil {
		t.Fatalf("expected error for invalid info.json")
	}
}

func TestBuildVersionInfoConsistentVersion(t *testing.T) {
	cfg := &config.Config{
		Package: config.PackageConfig{Name: "simtx"},
		Version: &config.VersionInfo{Raw: "9.9.9", Windows: "1.2.3"},
		RootDir: t.TempDir(),
	}

	vi, err := buildVersionInfo(cfg, resolvedWindowsVersion(cfg), "")
	if err != nil {
		t.Fatalf("buildVersionInfo failed: %v", err)
	}
	want := [4]uint16{1, 2, 3, 0}
	if vi.FileVersion != want {
		t.Errorf("expected FileVersion %v, got %v", want, vi.FileVersion)
	}
	if vi.ProductVersion != want {
		t.Errorf("expected ProductVersion %v, got %v", want, vi.ProductVersion)
	}
}
