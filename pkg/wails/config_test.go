package wails

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atterpac/refresh/engine"
	"gopkg.in/yaml.v3"

	"github.com/simtxng/cook/pkg/config"
)

func TestBuildWailsConfig_Defaults(t *testing.T) {
	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:        "myapp",
			ProductName: "My Application",
			Company:     "Acme Corp",
			Identifier:  "com.acme.myapp",
			Description: "An awesome desktop app",
			Copyright:   "(c) 2026 Acme Corp",
			OutputDir:   "dist-bin",
		},
		Frontend: config.FrontendConfig{
			Dir: "web",
		},
		Version: &config.VersionInfo{
			Raw: "1.2.3",
		},
	}

	wailsCfg := BuildWailsConfig(cfg)

	if wailsCfg.Version != "3" {
		t.Errorf("expected version 3, got %s", wailsCfg.Version)
	}
	if wailsCfg.Info.CompanyName != "Acme Corp" {
		t.Errorf("expected company Acme Corp, got %s", wailsCfg.Info.CompanyName)
	}
	if wailsCfg.Info.ProductName != "My Application" {
		t.Errorf("expected product My Application, got %s", wailsCfg.Info.ProductName)
	}
	if wailsCfg.Info.ProductIdentifier != "com.acme.myapp" {
		t.Errorf("expected identifier com.acme.myapp, got %s", wailsCfg.Info.ProductIdentifier)
	}
	if wailsCfg.Info.Version != "1.2.3" {
		t.Errorf("expected version 1.2.3, got %s", wailsCfg.Info.Version)
	}
	if wailsCfg.DevMode.RootPath != "." {
		t.Errorf("expected root_path '.', got %s", wailsCfg.DevMode.RootPath)
	}
	if wailsCfg.DevMode.LogLevel != "warn" {
		t.Errorf("expected log_level 'warn', got %s", wailsCfg.DevMode.LogLevel)
	}
	if wailsCfg.DevMode.Debounce != 1000 {
		t.Errorf("expected debounce 1000, got %d", wailsCfg.DevMode.Debounce)
	}

	// Verify ignored dirs contain custom frontend and output dir
	foundWeb := false
	foundDistBin := false
	for _, d := range wailsCfg.DevMode.Ignore.Dir {
		if d == "web" {
			foundWeb = true
		}
		if d == "dist-bin" {
			foundDistBin = true
		}
	}
	if !foundWeb || !foundDistBin {
		t.Errorf("expected ignore.dir to contain web and dist-bin: %v", wailsCfg.DevMode.Ignore.Dir)
	}

	// Verify standard 3 executes
	if len(wailsCfg.DevMode.Executes) != 3 {
		t.Fatalf("expected 3 executes, got %d", len(wailsCfg.DevMode.Executes))
	}
	if wailsCfg.DevMode.Executes[0].Cmd != "wails3 build DEV=true" {
		t.Errorf("unexpected step 0: %+v", wailsCfg.DevMode.Executes[0])
	}
	if wailsCfg.DevMode.Executes[1].Cmd != "wails3 task common:dev:frontend" {
		t.Errorf("unexpected step 1: %+v", wailsCfg.DevMode.Executes[1])
	}
	if wailsCfg.DevMode.Executes[2].Cmd != "wails3 task run" {
		t.Errorf("unexpected step 2: %+v", wailsCfg.DevMode.Executes[2])
	}
}

func TestBuildWailsConfig_CustomDev(t *testing.T) {
	cfg := &config.Config{
		Package: config.PackageConfig{
			Name: "customapp",
		},
		Wails: config.WailsConfig{
			Dev: config.WailsDevTomlConfig{
				Debounce: 500,
				LogLevel: "debug",
				Executes: []config.WailsDevExecute{
					{
						Cmd:  "cook build",
						Type: "blocking",
					},
					{
						Cmd:  "npm run dev",
						Type: "background",
					},
				},
			},
		},
	}

	wailsCfg := BuildWailsConfig(cfg)
	if wailsCfg.DevMode.Debounce != 500 {
		t.Errorf("expected debounce 500, got %d", wailsCfg.DevMode.Debounce)
	}
	if wailsCfg.DevMode.LogLevel != "debug" {
		t.Errorf("expected log_level debug, got %s", wailsCfg.DevMode.LogLevel)
	}
	if len(wailsCfg.DevMode.Executes) != 2 {
		t.Fatalf("expected 2 custom executes, got %d", len(wailsCfg.DevMode.Executes))
	}
}

func TestWriteConfigFile_And_EngineCompat(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:        "simtx",
			ProductName: "SimTX Desktop",
			Company:     "SimTX",
			Identifier:  "net.simtx.desktop",
			Description: "SimTX application",
			OutputDir:   "bin",
		},
		Frontend: config.FrontendConfig{
			Dir: "frontend",
		},
		Version: &config.VersionInfo{
			Raw: "0.1.0-alpha-12",
		},
		RootDir: tempDir,
	}

	outPath, err := WriteConfigFile(cfg, "")
	if err != nil {
		t.Fatalf("WriteConfigFile failed: %v", err)
	}

	expectedPath := filepath.Join(tempDir, "build", "config.yml")
	if outPath != expectedPath {
		t.Errorf("expected outPath %s, got %s", expectedPath, outPath)
	}

	content, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("failed to read written config file: %v", err)
	}

	if !strings.Contains(string(content), "productName: SimTX Desktop") {
		t.Errorf("content missing productName: %s", string(content))
	}
	if !strings.Contains(string(content), "version: 0.1.0-alpha-12") {
		t.Errorf("content missing version: %s", string(content))
	}
	if !strings.Contains(string(content), "cmd: wails3 task common:dev:frontend") {
		t.Errorf("content missing frontend step: %s", string(content))
	}

	// Verify it parses with Wails v3 Watcher devConfig schema (engine.Config)
	type devConfig struct {
		Config engine.Config `yaml:"dev_mode"`
	}
	var dev devConfig
	if err := yaml.Unmarshal(content, &dev); err != nil {
		t.Fatalf("failed to unmarshal into refresh engine.Config: %v", err)
	}

	if len(dev.Config.ExecStruct) != 3 {
		t.Fatalf("engine.Config.ExecStruct len = %d, want 3", len(dev.Config.ExecStruct))
	}

	for i, step := range dev.Config.ExecStruct {
		if err := step.Validate(); err != nil {
			t.Errorf("step %d failed validation: %v", i, err)
		}
	}
}

func TestGenerateYAML_GitIgnoreKey(t *testing.T) {
	cfg := &config.Config{
		Package: config.PackageConfig{Name: "demo"},
	}

	data, err := GenerateYAML(cfg)
	if err != nil {
		t.Fatalf("GenerateYAML failed: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "git: true") {
		t.Errorf("generated YAML missing git key: %s", content)
	}
	if strings.Contains(content, "git_ignore") {
		t.Errorf("generated YAML must not contain git_ignore: %s", content)
	}

	var parsed struct {
		DevMode struct {
			Ignore WailsDevIgnore `yaml:"ignore"`
		} `yaml:"dev_mode"`
	}
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal generated YAML: %v", err)
	}
	if !parsed.DevMode.Ignore.GitIgnore {
		t.Error("expected git ignore to default to true")
	}

	disabled := false
	cfg.Wails.Dev.GitIgnore = &disabled
	data, err = GenerateYAML(cfg)
	if err != nil {
		t.Fatalf("GenerateYAML failed: %v", err)
	}
	if !strings.Contains(string(data), "git: false") {
		t.Errorf("generated YAML missing git: false: %s", data)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", "x", "y"); got != "x" {
		t.Errorf("firstNonEmpty = %q, want x", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("firstNonEmpty = %q, want empty", got)
	}
}

func TestBuildWailsConfig_IdentifierFallback(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.Config
		want string
	}{
		{"wails identifier wins", &config.Config{Wails: config.WailsConfig{ProductIdentifier: "com.wails"}}, "com.wails"},
		{"package identifier wins", &config.Config{Package: config.PackageConfig{Identifier: "com.pkg"}, Wails: config.WailsConfig{ProductName: "Product"}}, "com.pkg"},
		{"package name", &config.Config{Package: config.PackageConfig{Name: "name"}}, "com.name"},
		{"product name", &config.Config{Package: config.PackageConfig{ProductName: "Product"}}, "com.Product"},
		{"app", &config.Config{}, "com.app"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildWailsConfig(tt.cfg).Info.ProductIdentifier; got != tt.want {
				t.Errorf("ProductIdentifier = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildWailsConfig_NilAndEmptyFallbacks(t *testing.T) {
	wailsCfg := BuildWailsConfig(nil)
	if wailsCfg == nil {
		t.Fatal("expected non-nil config for nil input")
	}
	if wailsCfg.Info.ProductIdentifier == "" || wailsCfg.Info.ProductIdentifier == "com." {
		t.Errorf("identifier fallback must not be empty or com.: %q", wailsCfg.Info.ProductIdentifier)
	}
	if strings.HasSuffix(wailsCfg.Info.Copyright, ", ") {
		t.Errorf("copyright must not end with empty company: %q", wailsCfg.Info.Copyright)
	}
	if !strings.HasPrefix(wailsCfg.Info.Copyright, "(c) ") {
		t.Errorf("unexpected copyright fallback: %q", wailsCfg.Info.Copyright)
	}

	data, err := GenerateYAML(&config.Config{})
	if err != nil {
		t.Fatalf("GenerateYAML failed for empty config: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "other:") {
		t.Errorf("empty other should be omitted: %s", content)
	}
	if strings.Contains(content, "fileAssociations:") {
		t.Errorf("empty fileAssociations should be omitted: %s", content)
	}
}

func TestWriteConfigFile_NilConfig(t *testing.T) {
	target := filepath.Join(t.TempDir(), "config.yml")
	outPath, err := WriteConfigFile(nil, target)
	if err != nil {
		t.Fatalf("WriteConfigFile failed for nil config: %v", err)
	}
	if outPath != target {
		t.Errorf("expected outPath %s, got %s", target, outPath)
	}
}

func TestWriteConfigFile_RelativePathUsesRootDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	cfg := &config.Config{
		Package: config.PackageConfig{Name: "demo"},
		RootDir: root,
	}

	outPath, err := WriteConfigFile(cfg, "nested/../build/config.yml")
	if err != nil {
		t.Fatalf("WriteConfigFile failed: %v", err)
	}

	expected := filepath.Join(root, "build", "config.yml")
	if outPath != expected {
		t.Errorf("expected outPath %s, got %s", expected, outPath)
	}
	if _, err := os.Stat(expected); err != nil {
		t.Errorf("expected config at %s: %v", expected, err)
	}
}

func TestWriteConfigFile_RelativeRootDir(t *testing.T) {
	base := t.TempDir()
	t.Chdir(base)

	cfg := &config.Config{
		Package: config.PackageConfig{Name: "demo"},
		RootDir: "project",
	}

	outPath, err := WriteConfigFile(cfg, "build/config.yml")
	if err != nil {
		t.Fatalf("WriteConfigFile failed: %v", err)
	}

	expected := filepath.Join(base, "project", "build", "config.yml")
	if outPath != expected {
		t.Errorf("expected outPath %s, got %s", expected, outPath)
	}
}

func TestWriteConfigFile_AbsErrorPropagates(t *testing.T) {
	base := t.TempDir()
	t.Chdir(base)
	if err := os.RemoveAll(base); err != nil {
		t.Fatalf("failed to remove working directory: %v", err)
	}

	_, err := WriteConfigFile(&config.Config{
		Package: config.PackageConfig{Name: "demo"},
	}, "build/config.yml")
	if err == nil {
		t.Fatal("expected an error when the working directory cannot be resolved")
	}
	if !strings.Contains(err.Error(), "current working directory") {
		t.Errorf("expected wrapped CWD resolution error, got: %v", err)
	}
}
