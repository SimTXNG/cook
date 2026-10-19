package windows

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/simtxng/cook/pkg/config"
)

func TestBuildNSISDefines(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cook-nsis-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	guiPath := filepath.Join(tmpDir, "simtx.exe")
	cliPath := filepath.Join(tmpDir, "simtx-cli.exe")
	_ = os.WriteFile(guiPath, []byte("fake-gui"), 0755)
	_ = os.WriteFile(cliPath, []byte("fake-cli"), 0755)

	binaries := map[string]string{
		"simtx":     guiPath,
		"simtx-cli": cliPath,
	}

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:        "simtx",
			ProductName: "SimTX",
			Company:     "SimTX Org",
			Copyright:   "© 2026 SimTX",
		},
		Windows: config.WindowsConfig{
			NSIS: config.WindowsNSISConfig{
				InstallScope: "machine",
				Defines: map[string]string{
					"CUSTOM_FLAG": "1",
				},
			},
		},
		Version: &config.VersionInfo{
			Raw:     "0.1.0-alpha-12",
			Windows: "0.1.0",
		},
		RootDir: tmpDir,
	}

	defines, err := BuildNSISDefines(cfg, binaries, "amd64")
	if err != nil {
		t.Fatalf("BuildNSISDefines failed: %v", err)
	}

	definesMap := make(map[string]string)
	for _, d := range defines {
		d = strings.TrimPrefix(d, "-D")
		parts := strings.SplitN(d, "=", 2)
		if len(parts) == 2 {
			definesMap[parts[0]] = parts[1]
		}
	}

	if definesMap["INFO_PROJECTNAME"] != "simtx" {
		t.Errorf("expected INFO_PROJECTNAME=simtx, got %q", definesMap["INFO_PROJECTNAME"])
	}
	if definesMap["INFO_PRODUCTVERSION"] != "0.1.0.0" {
		t.Errorf("expected numeric INFO_PRODUCTVERSION=0.1.0.0, got %q", definesMap["INFO_PRODUCTVERSION"])
	}
	if definesMap["WAILS_INSTALL_SCOPE"] != "machine" {
		t.Errorf("expected WAILS_INSTALL_SCOPE=machine, got %q", definesMap["WAILS_INSTALL_SCOPE"])
	}
	if definesMap["REQUEST_EXECUTION_LEVEL"] != "admin" {
		t.Errorf("expected REQUEST_EXECUTION_LEVEL=admin, got %q", definesMap["REQUEST_EXECUTION_LEVEL"])
	}
	if definesMap["ARG_WAILS_AMD64_BINARY"] != guiPath {
		t.Errorf("expected ARG_WAILS_AMD64_BINARY=%s, got %q", guiPath, definesMap["ARG_WAILS_AMD64_BINARY"])
	}
	if definesMap["ARG_WAILS_CLI_BINARY"] != cliPath {
		t.Errorf("expected ARG_WAILS_CLI_BINARY=%s, got %q", cliPath, definesMap["ARG_WAILS_CLI_BINARY"])
	}
	if definesMap["CUSTOM_FLAG"] != "1" {
		t.Errorf("expected CUSTOM_FLAG=1, got %q", definesMap["CUSTOM_FLAG"])
	}
}

func TestBuildNSISDefinesUserScope(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cook-nsis-userscope-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	guiPath := filepath.Join(tmpDir, "simtx.exe")
	_ = os.WriteFile(guiPath, []byte("fake-gui"), 0755)

	binaries := map[string]string{
		"simtx": guiPath,
	}

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name: "simtx",
		},
		Windows: config.WindowsConfig{
			NSIS: config.WindowsNSISConfig{
				InstallScope: "user",
			},
		},
		Version: &config.VersionInfo{
			Raw:     "0.1.0",
			Windows: "0.1.0.0",
		},
		RootDir: tmpDir,
	}

	defines, err := BuildNSISDefines(cfg, binaries, "amd64")
	if err != nil {
		t.Fatalf("BuildNSISDefines failed: %v", err)
	}

	definesMap := make(map[string]string)
	for _, d := range defines {
		d = strings.TrimPrefix(d, "-D")
		parts := strings.SplitN(d, "=", 2)
		if len(parts) == 2 {
			definesMap[parts[0]] = parts[1]
		}
	}

	if definesMap["WAILS_INSTALL_SCOPE"] != "user" {
		t.Errorf("expected WAILS_INSTALL_SCOPE=user, got %q", definesMap["WAILS_INSTALL_SCOPE"])
	}
	if definesMap["REQUEST_EXECUTION_LEVEL"] != "user" {
		t.Errorf("expected REQUEST_EXECUTION_LEVEL=user, got %q", definesMap["REQUEST_EXECUTION_LEVEL"])
	}
}

func TestBuildNSISDefinesArm64(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cook-nsis-arm64-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	guiPath := filepath.Join(tmpDir, "simtx.exe")
	if err := os.WriteFile(guiPath, []byte("fake-gui"), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Package: config.PackageConfig{Name: "simtx"},
		Version: &config.VersionInfo{
			Raw:     "1.2.3-beta-4",
			Windows: "1.2.3",
		},
		RootDir: tmpDir,
	}

	defines, err := BuildNSISDefines(cfg, map[string]string{"simtx": guiPath}, "arm64")
	if err != nil {
		t.Fatalf("BuildNSISDefines failed: %v", err)
	}

	definesMap := make(map[string]string)
	for _, d := range defines {
		d = strings.TrimPrefix(d, "-D")
		parts := strings.SplitN(d, "=", 2)
		if len(parts) == 2 {
			definesMap[parts[0]] = parts[1]
		}
	}

	if definesMap["ARG_WAILS_ARM64_BINARY"] != guiPath {
		t.Errorf("expected ARG_WAILS_ARM64_BINARY=%s, got %q", guiPath, definesMap["ARG_WAILS_ARM64_BINARY"])
	}
	if _, ok := definesMap["ARG_WAILS_AMD64_BINARY"]; ok {
		t.Errorf("did not expect ARG_WAILS_AMD64_BINARY for arm64 build")
	}
	if definesMap["INFO_PRODUCTVERSION"] != "1.2.3.0" {
		t.Errorf("expected INFO_PRODUCTVERSION=1.2.3.0, got %q", definesMap["INFO_PRODUCTVERSION"])
	}
}

func TestBuildNSISVerifiesFreshOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake makensis shell script requires a POSIX shell")
	}

	tmpDir, err := os.MkdirTemp("", "cook-nsis-output-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	guiPath := filepath.Join(tmpDir, "simtx.exe")
	if err := os.WriteFile(guiPath, []byte("fake-gui"), 0755); err != nil {
		t.Fatal(err)
	}

	scriptPath := filepath.Join(tmpDir, "build", "windows", "nsis", "project.nsi")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte("; fake"), 0644); err != nil {
		t.Fatal(err)
	}

	expected := filepath.Join(tmpDir, "bin", "simtx-arm64-installer.exe")
	fakeBin := filepath.Join(tmpDir, "fakebin")
	if err := os.MkdirAll(fakeBin, 0755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n: > \"$NSIS_TEST_OUT\"\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "makensis"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NSIS_TEST_OUT", expected)

	cfg := &config.Config{
		Package: config.PackageConfig{Name: "simtx", OutputDir: "bin"},
		Version: &config.VersionInfo{Raw: "1.2.3", Windows: "1.2.3.0"},
		RootDir: tmpDir,
	}

	res, err := BuildNSIS(context.Background(), cfg, "arm64", map[string]string{"simtx": guiPath})
	if err != nil {
		t.Fatalf("BuildNSIS failed: %v", err)
	}
	if len(res) != 1 || res[0] != expected {
		t.Fatalf("expected [%s], got %v", expected, res)
	}
	if !fileExists(expected) {
		t.Fatalf("expected installer at %s", expected)
	}
}

func TestBuildNSISRejectsStaleOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake makensis shell script requires a POSIX shell")
	}

	tmpDir, err := os.MkdirTemp("", "cook-nsis-stale-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	guiPath := filepath.Join(tmpDir, "simtx.exe")
	if err := os.WriteFile(guiPath, []byte("fake-gui"), 0755); err != nil {
		t.Fatal(err)
	}

	scriptPath := filepath.Join(tmpDir, "build", "windows", "nsis", "project.nsi")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte("; fake"), 0644); err != nil {
		t.Fatal(err)
	}

	// A stale installer from an earlier run must not be returned.
	expected := filepath.Join(tmpDir, "bin", "simtx-amd64-installer.exe")
	if err := os.MkdirAll(filepath.Dir(expected), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(expected, []byte("stale"), 0755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(expected, old, old); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(tmpDir, "fakebin")
	if err := os.MkdirAll(fakeBin, 0755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "makensis"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := &config.Config{
		Package: config.PackageConfig{Name: "simtx", OutputDir: "bin"},
		Version: &config.VersionInfo{Raw: "1.2.3", Windows: "1.2.3.0"},
		RootDir: tmpDir,
	}

	if _, err := BuildNSIS(context.Background(), cfg, "amd64", map[string]string{"simtx": guiPath}); err == nil {
		t.Fatalf("expected error when makensis produces no fresh installer")
	}
}

func TestBuildNSISResolvesConfiguredScriptAndFallbackOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake makensis shell script requires a POSIX shell")
	}

	tmpDir, err := os.MkdirTemp("", "cook-nsis-config-script-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	guiPath := filepath.Join(tmpDir, "simtx.exe")
	if err := os.WriteFile(guiPath, []byte("fake-gui"), 0755); err != nil {
		t.Fatal(err)
	}

	scriptPath := filepath.Join(tmpDir, "custom", "installer.nsi")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte("; fake"), 0644); err != nil {
		t.Fatal(err)
	}

	// The configured script lives outside the default build/windows/nsis path,
	// and makensis writes a fresh executable into the script directory under a
	// name that differs from the guessed output path, exercising the fallback
	// scan over outDir and scriptDir.
	fallbackOut := filepath.Join(filepath.Dir(scriptPath), "custom-installer.exe")
	fakeBin := filepath.Join(tmpDir, "fakebin")
	if err := os.MkdirAll(fakeBin, 0755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n: > \"$NSIS_TEST_OUT\"\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "makensis"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NSIS_TEST_OUT", fallbackOut)

	cfg := &config.Config{
		Package: config.PackageConfig{Name: "simtx", OutputDir: "bin"},
		Windows: config.WindowsConfig{
			NSIS: config.WindowsNSISConfig{Script: "custom/installer.nsi"},
		},
		Version: &config.VersionInfo{Raw: "1.2.3", Windows: "1.2.3.0"},
		RootDir: tmpDir,
	}

	res, err := BuildNSIS(context.Background(), cfg, "amd64", map[string]string{"simtx": guiPath})
	if err != nil {
		t.Fatalf("BuildNSIS failed: %v", err)
	}
	if len(res) != 1 || res[0] != fallbackOut {
		t.Fatalf("expected fallback output [%s], got %v", fallbackOut, res)
	}
}

func TestEnsureWebView2BootstrapperConfiguredPath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cook-nsis-wv2-config-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	customPath := filepath.Join(tmpDir, "vendor", "wv2setup.exe")
	if err := os.MkdirAll(filepath.Dir(customPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(customPath, []byte("custom-bootstrapper"), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Windows: config.WindowsConfig{
			NSIS: config.WindowsNSISConfig{
				Webview2Bootstrapper: "vendor/wv2setup.exe",
			},
		},
		RootDir: tmpDir,
	}

	res, err := EnsureWebView2Bootstrapper(cfg, filepath.Join(tmpDir, "script"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != customPath {
		t.Errorf("expected configured bootstrapper %s, got %s", customPath, res)
	}

	defines, err := BuildNSISDefines(cfg, map[string]string{"simtx": customPath}, "amd64")
	if err != nil {
		t.Fatalf("BuildNSISDefines failed: %v", err)
	}
	found := false
	for _, d := range defines {
		if strings.HasPrefix(d, "-DARG_WEBVIEW2_BOOTSTRAPPER="+customPath) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ARG_WEBVIEW2_BOOTSTRAPPER define for %s in %v", customPath, defines)
	}
}

func TestEnsureWebView2BootstrapperMissingConfigured(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Windows: config.WindowsConfig{
			NSIS: config.WindowsNSISConfig{
				Webview2Bootstrapper: "missing.exe",
			},
		},
		RootDir: tmpDir,
	}

	if _, err := EnsureWebView2Bootstrapper(cfg, tmpDir); err == nil {
		t.Fatalf("expected error for configured but missing bootstrapper")
	}
}

func TestBuildNSISDefinesMissingConfiguredBootstrapper(t *testing.T) {
	tmpDir := t.TempDir()
	guiPath := filepath.Join(tmpDir, "simtx.exe")
	if err := os.WriteFile(guiPath, []byte("fake-gui"), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Package: config.PackageConfig{Name: "simtx"},
		Windows: config.WindowsConfig{
			NSIS: config.WindowsNSISConfig{
				Webview2Bootstrapper: "missing.exe",
			},
		},
		Version: &config.VersionInfo{Raw: "1.0.0", Windows: "1.0.0.0"},
		RootDir: tmpDir,
	}

	if _, err := BuildNSISDefines(cfg, map[string]string{"simtx": guiPath}, "amd64"); err == nil {
		t.Fatalf("expected error for configured but missing bootstrapper")
	}
}

func TestEnsureWebView2BootstrapperExisting(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cook-nsis-wv2-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	wv2Path := filepath.Join(tmpDir, WebView2BootstrapperFileName)
	if err := os.WriteFile(wv2Path, []byte("bootstrapper"), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Windows: config.WindowsConfig{
			NSIS: config.WindowsNSISConfig{
				AutoDownloadWebview2: false,
			},
		},
		RootDir: tmpDir,
	}

	res, err := EnsureWebView2Bootstrapper(cfg, tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != wv2Path {
		t.Errorf("expected %s, got %s", wv2Path, res)
	}
}
