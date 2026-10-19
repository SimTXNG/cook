package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	tempDir := t.TempDir()

	// Create single-source-of-truth version file matching cook.example.toml
	versionDir := filepath.Join(tempDir, "pkg", "version")
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		t.Fatalf("failed to create version dir: %v", err)
	}
	versionGo := `package version

const AppVersion = "1.0.0"
var AppCommit = "deadbeef"
`
	if err := os.WriteFile(filepath.Join(versionDir, "version.go"), []byte(versionGo), 0644); err != nil {
		t.Fatalf("failed to write version.go: %v", err)
	}

	// Read exact cook.example.toml reference configuration
	exampleTomlPath := filepath.Join("..", "..", "cook.example.toml")
	tomlData, err := os.ReadFile(exampleTomlPath)
	if err != nil {
		t.Fatalf("failed to read cook.example.toml at %s: %v", exampleTomlPath, err)
	}

	configFile := filepath.Join(tempDir, "cook.toml")
	if err := os.WriteFile(configFile, tomlData, 0644); err != nil {
		t.Fatalf("failed to write test toml: %v", err)
	}

	cfg, err := LoadConfig(configFile)
	if err != nil {
		t.Fatalf("LoadConfig failed on cook.example.toml: %v", err)
	}

	// Assertions against cook.example.toml
	if cfg.Package.Name != "myapp" {
		t.Errorf("Package.Name = %q, want 'myapp'", cfg.Package.Name)
	}
	if cfg.Package.ProductName != "My Application" {
		t.Errorf("Package.ProductName = %q, want 'My Application'", cfg.Package.ProductName)
	}
	if cfg.Package.Company != "Acme Corp" {
		t.Errorf("Package.Company = %q, want 'Acme Corp'", cfg.Package.Company)
	}
	if cfg.Package.Identifier != "com.acme.myapp" {
		t.Errorf("Package.Identifier = %q, want 'com.acme.myapp'", cfg.Package.Identifier)
	}
	if len(cfg.Binaries) != 2 {
		t.Fatalf("len(Binaries) = %d, want 2", len(cfg.Binaries))
	}
	if cfg.Binaries[0].Name != "myapp" || cfg.Binaries[1].Name != "myapp-cli" {
		t.Errorf("Binary names mismatch: %+v", cfg.Binaries)
	}
	if cfg.Binaries[1].Path != "./cmd/myapp-cli" {
		t.Errorf("Binary[1].Path = %q, want './cmd/myapp-cli'", cfg.Binaries[1].Path)
	}
	if cfg.Binaries[1].CommitVar != "main.AppCommit" {
		t.Errorf("Binary[1].CommitVar = %q, want 'main.AppCommit'", cfg.Binaries[1].CommitVar)
	}
	if !cfg.Binaries[1].UPX {
		t.Errorf("Binary[1].UPX = false, want true")
	}
	if cfg.Version == nil {
		t.Fatalf("Version is nil")
	}
	if cfg.Version.Raw != "1.0.0" {
		t.Errorf("Version.Raw = %q, want '1.0.0'", cfg.Version.Raw)
	}
	if cfg.Linux.AppImage.FixScript != "scripts/fix-appimage.sh" {
		t.Errorf("AppImage FixScript = %q, want 'scripts/fix-appimage.sh'", cfg.Linux.AppImage.FixScript)
	}
	if cfg.Windows.Syso.Out != "wails_windows_amd64.syso" {
		t.Errorf("Windows.Syso.Out = %q, want 'wails_windows_amd64.syso'", cfg.Windows.Syso.Out)
	}
	if cfg.Darwin.BundleID != "com.acme.myapp" {
		t.Errorf("Darwin.BundleID = %q, want 'com.acme.myapp'", cfg.Darwin.BundleID)
	}
	if cfg.Darwin.MicrophoneUsageDescription != "MyApp requires microphone access." {
		t.Errorf("Darwin.MicrophoneUsageDescription mismatch: %q", cfg.Darwin.MicrophoneUsageDescription)
	}
	if cfg.Darwin.DMG.WindowWidth != 540 || cfg.Darwin.DMG.WindowHeight != 380 {
		t.Errorf("Darwin.DMG dimensions mismatch: %dx%d", cfg.Darwin.DMG.WindowWidth, cfg.Darwin.DMG.WindowHeight)
	}
}

func TestApplyDefaults(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)

	if cfg.Package.OutputDir != "bin" {
		t.Errorf("Package.OutputDir = %q, want 'bin'", cfg.Package.OutputDir)
	}
	if cfg.Frontend.PackageManager != "npm" {
		t.Errorf("Frontend.PackageManager = %q, want 'npm'", cfg.Frontend.PackageManager)
	}
	if cfg.Frontend.Dir != "frontend" {
		t.Errorf("Frontend.Dir = %q, want 'frontend'", cfg.Frontend.Dir)
	}
	if want := filepath.Join("frontend", "dist"); cfg.Frontend.DistDir != want {
		t.Errorf("Frontend.DistDir = %q, want %q", cfg.Frontend.DistDir, want)
	}
	if cfg.Frontend.BuildCmd != "npm run build" {
		t.Errorf("Frontend.BuildCmd = %q, want 'npm run build'", cfg.Frontend.BuildCmd)
	}
	if cfg.Templates.Dir != "build/templates" {
		t.Errorf("Templates.Dir = %q, want 'build/templates'", cfg.Templates.Dir)
	}
	if cfg.Windows.NSIS.InstallScope != "machine" {
		t.Errorf("Windows.NSIS.InstallScope = %q, want 'machine'", cfg.Windows.NSIS.InstallScope)
	}
	if cfg.Darwin.MinOSVersion != "12.0" {
		t.Errorf("Darwin.MinOSVersion = %q, want '12.0'", cfg.Darwin.MinOSVersion)
	}
	if cfg.Darwin.Codesign != "adhoc" {
		t.Errorf("Darwin.Codesign = %q, want 'adhoc'", cfg.Darwin.Codesign)
	}
	if cfg.Darwin.DMG.WindowWidth != 540 || cfg.Darwin.DMG.WindowHeight != 380 {
		t.Errorf("Darwin.DMG dimensions = %dx%d, want 540x380", cfg.Darwin.DMG.WindowWidth, cfg.Darwin.DMG.WindowHeight)
	}
	if got := cfg.Darwin.DMG.AppPosition; len(got) != 2 || got[0] != 135 || got[1] != 190 {
		t.Errorf("Darwin.DMG.AppPosition = %v, want [135 190]", got)
	}
	if got := cfg.Darwin.DMG.ApplicationsPosition; len(got) != 2 || got[0] != 405 || got[1] != 190 {
		t.Errorf("Darwin.DMG.ApplicationsPosition = %v, want [405 190]", got)
	}
}

func TestApplyDefaultsPreservesExplicitPositions(t *testing.T) {
	cfg := &Config{}
	cfg.Darwin.DMG.AppPosition = []int{0, 190}
	cfg.Darwin.DMG.ApplicationsPosition = []int{0, 0}

	applyDefaults(cfg)

	if got := cfg.Darwin.DMG.AppPosition; len(got) != 2 || got[0] != 0 || got[1] != 190 {
		t.Errorf("explicit AppPosition overwritten: got %v, want [0 190]", got)
	}
	if got := cfg.Darwin.DMG.ApplicationsPosition; len(got) != 2 || got[0] != 0 || got[1] != 0 {
		t.Errorf("explicit ApplicationsPosition overwritten: got %v, want [0 0]", got)
	}
}

func TestValidate(t *testing.T) {
	valid := func() *Config {
		return &Config{
			Package:  PackageConfig{Name: "myapp", Version: "1.0.0"},
			Binaries: []BinaryConfig{{Name: "myapp"}},
		}
	}

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{name: "valid config", mutate: func(*Config) {}, wantErr: false},
		{name: "uppercase package name", mutate: func(c *Config) { c.Package.Name = "MyApp" }, wantErr: true},
		{name: "empty package name", mutate: func(c *Config) { c.Package.Name = "" }, wantErr: true},
		{name: "leading punctuation package name", mutate: func(c *Config) { c.Package.Name = ".myapp" }, wantErr: true},
		{name: "version starting with letter", mutate: func(c *Config) { c.Package.Version = "v1.0.0" }, wantErr: true},
		{name: "version with space", mutate: func(c *Config) { c.Package.Version = "1.0 0" }, wantErr: true},
		{name: "version with slash", mutate: func(c *Config) { c.Package.Version = "1.0/0" }, wantErr: true},
		{name: "linux with nsis target", mutate: func(c *Config) { c.Linux.Targets = []string{"nsis"} }, wantErr: true},
		{name: "linux with misspelled deb target", mutate: func(c *Config) { c.Linux.Targets = []string{"debb"} }, wantErr: true},
		{name: "windows with deb target", mutate: func(c *Config) { c.Windows.Targets = []string{"deb"} }, wantErr: true},
		{name: "darwin with nsis target", mutate: func(c *Config) { c.Darwin.Targets = []string{"nsis"} }, wantErr: true},
		{name: "zero binaries", mutate: func(c *Config) { c.Binaries = nil }, wantErr: true},
		{name: "duplicate binary names", mutate: func(c *Config) {
			c.Binaries = []BinaryConfig{{Name: "myapp"}, {Name: "myapp"}}
		}, wantErr: true},
		{name: "app position wrong length", mutate: func(c *Config) {
			c.Darwin.DMG.AppPosition = []int{1, 2, 3}
		}, wantErr: true},
		{name: "applications position wrong length", mutate: func(c *Config) {
			c.Darwin.DMG.ApplicationsPosition = []int{1}
		}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid()
			tt.mutate(cfg)

			err := cfg.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Validate() = nil, want a non-empty error")
				}
				if err.Error() == "" {
					t.Fatalf("Validate() returned an empty error message")
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}
