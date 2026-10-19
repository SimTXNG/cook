package linux

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/simtxng/cook/pkg/config"
)

func boolPtr(v bool) *bool {
	return &v
}

func TestStageAppDir(t *testing.T) {
	tmpDir := t.TempDir()

	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	guiBin := filepath.Join(binDir, "simtx")
	_ = os.WriteFile(guiBin, []byte("#!/bin/sh\necho simtx\n"), 0755)
	cliBin := filepath.Join(binDir, "simtx-cli")
	_ = os.WriteFile(cliBin, []byte("#!/bin/sh\necho simtx-cli\n"), 0755)
	iconPath := filepath.Join(tmpDir, "appicon.png")
	_ = os.WriteFile(iconPath, []byte("fake-icon-data"), 0644)

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:        "simtx",
			ProductName: "SimTX Transmitter",
			Description: "SimTX AppImage test",
			OutputDir:   "bin",
			Icon:        iconPath,
		},
		Linux: config.LinuxConfig{
			Categories: "Development;HamRadio;",
			BundledCLI: "simtx-cli",
		},
		RootDir: tmpDir,
	}

	binaries := map[string]string{
		"simtx":     guiBin,
		"simtx-cli": cliBin,
	}

	appDir := filepath.Join(tmpDir, "simtx-x86_64.AppDir")
	info, err := StageAppDir(cfg, appDir, binaries)
	if err != nil {
		t.Fatalf("StageAppDir failed: %v", err)
	}

	if info.BinaryName != "simtx" {
		t.Errorf("expected binaryName simtx, got %s", info.BinaryName)
	}

	// 1. Verify GUI binary
	guiStaged := filepath.Join(appDir, "usr", "bin", "simtx")
	guiFi, err := os.Stat(guiStaged)
	if err != nil {
		t.Fatalf("staged GUI binary not found: %v", err)
	}
	if guiFi.Mode()&0111 == 0 {
		t.Errorf("staged GUI binary is not executable: mode=%v", guiFi.Mode())
	}

	// 2. Verify companion CLI binary
	cliStaged := filepath.Join(appDir, "usr", "bin", "simtx-cli")
	cliFi, err := os.Stat(cliStaged)
	if err != nil {
		t.Fatalf("staged CLI binary not found: %v", err)
	}
	if cliFi.Mode()&0111 == 0 {
		t.Errorf("staged CLI binary is not executable: mode=%v", cliFi.Mode())
	}

	// 3. Verify Desktop files
	desktopRoot := filepath.Join(appDir, "simtx.desktop")
	if _, err := os.Stat(desktopRoot); err != nil {
		t.Errorf("simtx.desktop missing from AppDir root: %v", err)
	}
	desktopShare := filepath.Join(appDir, "usr", "share", "applications", "simtx.desktop")
	if _, err := os.Stat(desktopShare); err != nil {
		t.Errorf("simtx.desktop missing from usr/share/applications: %v", err)
	}

	// 4. Verify Icon files
	iconRoot := filepath.Join(appDir, "simtx.png")
	if _, err := os.Stat(iconRoot); err != nil {
		t.Errorf("simtx.png missing from AppDir root: %v", err)
	}
	dirIcon := filepath.Join(appDir, ".DirIcon")
	if _, err := os.Stat(dirIcon); err != nil {
		t.Errorf(".DirIcon missing from AppDir root: %v", err)
	}

	// 5. Verify AppRun wrapper
	appRunPath := filepath.Join(appDir, "AppRun")
	appRunFi, err := os.Stat(appRunPath)
	if err != nil {
		t.Fatalf("AppRun wrapper missing: %v", err)
	}
	if appRunFi.Mode()&0111 == 0 {
		t.Errorf("AppRun is not executable: mode=%v", appRunFi.Mode())
	}
}

func TestGenerateSHA256(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.AppImage")
	content := []byte("hello world appimage payload")
	if err := os.WriteFile(testFile, content, 0755); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	shaPath, err := GenerateSHA256(testFile)
	if err != nil {
		t.Fatalf("GenerateSHA256 failed: %v", err)
	}

	if shaPath != testFile+".sha256" {
		t.Errorf("expected shaPath=%s, got %s", testFile+".sha256", shaPath)
	}

	shaData, err := os.ReadFile(shaPath)
	if err != nil {
		t.Fatalf("failed to read sha256 file: %v", err)
	}

	shaStr := string(shaData)
	if !strings.HasSuffix(shaStr, "  test.AppImage\n") {
		t.Errorf("unexpected sha256 formatting:\n%s", shaStr)
	}
}

func TestVerifyAppImage(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Valid mock AppImage (ELF header + executable)
	validAppImage := filepath.Join(tmpDir, "valid.AppImage")
	// ELF header: 0x7f 'E' 'L' 'F', then 4 bytes, then 'A' 'I' 0x02
	mockHeader := []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00, 'A', 'I', 0x02, 0x00, 0x00, 0x00, 0x00, 0x00}
	if err := os.WriteFile(validAppImage, mockHeader, 0755); err != nil {
		t.Fatalf("failed to write validAppImage: %v", err)
	}

	if err := VerifyAppImage(validAppImage); err != nil {
		t.Errorf("expected validAppImage to pass verification: %v", err)
	}

	// 2. Non-executable file
	nonExecAppImage := filepath.Join(tmpDir, "non-exec.AppImage")
	if err := os.WriteFile(nonExecAppImage, mockHeader, 0644); err != nil {
		t.Fatalf("failed to write nonExecAppImage: %v", err)
	}
	if err := VerifyAppImage(nonExecAppImage); err == nil {
		t.Errorf("expected non-executable AppImage to fail verification")
	}

	// 3. Invalid ELF magic
	invalidHeader := []byte("INVALID_HEADER_HERE")
	invalidAppImage := filepath.Join(tmpDir, "invalid.AppImage")
	if err := os.WriteFile(invalidAppImage, invalidHeader, 0755); err != nil {
		t.Fatalf("failed to write invalidAppImage: %v", err)
	}
	if err := VerifyAppImage(invalidAppImage); err == nil {
		t.Errorf("expected invalid header to fail verification")
	}

	// 4. Empty file
	emptyAppImage := filepath.Join(tmpDir, "empty.AppImage")
	if err := os.WriteFile(emptyAppImage, []byte{}, 0755); err != nil {
		t.Fatalf("failed to write emptyAppImage: %v", err)
	}
	if err := VerifyAppImage(emptyAppImage); err == nil {
		t.Errorf("expected empty file to fail verification")
	}
}

func TestGetLinuxArch(t *testing.T) {
	tests := []struct {
		goarch   string
		expected string
	}{
		{"amd64", "x86_64"},
		{"arm64", "aarch64"},
		{"386", "i686"},
		{"arm", "armhf"},
		{"mips", "mips"},
	}

	for _, tc := range tests {
		got := GetLinuxArch(tc.goarch)
		if got != tc.expected {
			t.Errorf("GetLinuxArch(%s): got %s, want %s", tc.goarch, got, tc.expected)
		}
	}
}

func TestPackageAppImage_FixScriptIntegration(t *testing.T) {
	tmpDir := t.TempDir()

	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	guiBin := filepath.Join(binDir, "simtx")
	_ = os.WriteFile(guiBin, []byte("#!/bin/sh\necho simtx\n"), 0755)
	cliBin := filepath.Join(binDir, "simtx-cli")
	_ = os.WriteFile(cliBin, []byte("#!/bin/sh\necho simtx-cli\n"), 0755)

	// Hermetic toolchain: both tools live in a temp tools dir, which takes
	// precedence over PATH, so no system binary or network download can be used.
	toolsDir := filepath.Join(tmpDir, "tools")
	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		t.Fatalf("failed to create tools dir: %v", err)
	}
	appImageToolPath := filepath.Join(toolsDir, "appimagetool")
	appImageToolScript := `#!/bin/sh
set -e
for last; do :; done
printf '\177ELF\002\001\001\000AI\002\000\000\000\000\000' > "$last"
/bin/chmod +x "$last"
`
	if err := os.WriteFile(appImageToolPath, []byte(appImageToolScript), 0755); err != nil {
		t.Fatalf("failed to write fake appimagetool: %v", err)
	}

	// Fake linuxdeploy delegates to the fake appimagetool, which writes a valid
	// mock AppImage at the target path derived from --appdir.
	linuxDeployPath := filepath.Join(toolsDir, "linuxdeploy-x86_64.AppImage")
	linuxDeployScript := `#!/bin/sh
set -e
appdir=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "--appdir" ]; then
    appdir="$arg"
  fi
  prev="$arg"
done
out="${appdir%.AppDir}.AppImage"
"` + appImageToolPath + `" "$appdir" "$out"
`
	if err := os.WriteFile(linuxDeployPath, []byte(linuxDeployScript), 0755); err != nil {
		t.Fatalf("failed to write fake linuxdeploy: %v", err)
	}

	// Keep bash reachable for the hook runner while ensuring no ordinary
	// system directory is searched for packaging tools.
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("bash is required to run fix scripts: %v", err)
	}
	t.Setenv("PATH", filepath.Dir(bashPath))

	envOut := filepath.Join(tmpDir, "fix-env.txt")
	argsOut := filepath.Join(tmpDir, "fix-args.txt")

	// Create a mock fix-script that records its environment/arguments and
	// rewrites a valid mock AppImage.
	fixScript := filepath.Join(tmpDir, "mock-fix.sh")
	fixScriptContent := `#!/bin/sh
set -e
APPIMAGE="$1"
MAIN_BIN="$2"
CLI_BIN="${3:-}"

# Verify received environment variables
if [ -z "$APPIMAGE_PATH" ] || [ -z "$APPDIR" ] || [ -z "$CLI_BINARY" ] || [ -z "$UPDATE_INFORMATION" ] || [ -z "$APPIMAGETOOL_DIR" ]; then
  echo "Missing required environment variables" >&2
  exit 1
fi

printf '%s|%s|%s|%s|%s\n' "$APPIMAGE_PATH" "$APPDIR" "$CLI_BINARY" "$UPDATE_INFORMATION" "$APPIMAGETOOL_DIR" > "` + envOut + `"
printf '%s|%s|%s\n' "$APPIMAGE" "$MAIN_BIN" "$CLI_BIN" > "` + argsOut + `"

# Write valid ELF + AI\x02 AppImage header
printf '\177ELF\002\001\001\000AI\002\000\000\000\000\000' > "$APPIMAGE"
/bin/chmod +x "$APPIMAGE"
`
	if err := os.WriteFile(fixScript, []byte(fixScriptContent), 0755); err != nil {
		t.Fatalf("failed to write fix script: %v", err)
	}

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:        "simtx",
			Description: "AppImage fix script test",
			OutputDir:   "bin",
		},
		Linux: config.LinuxConfig{
			BundledCLI: "simtx-cli",
			AppImage: config.AppImageConfig{
				ToolsDir:       toolsDir,
				FixScript:      fixScript,
				UpdateInfo:     "zsync|https://manifest.simtx.net/latest/app/linux/amd64/appimage-zsync",
				GenerateSHA256: boolPtr(true),
				GenerateZsync:  boolPtr(false),
			},
		},
		RootDir: tmpDir,
	}

	binaries := map[string]string{
		"simtx":     guiBin,
		"simtx-cli": cliBin,
	}

	artifacts, err := PackageAppImage(context.Background(), cfg, "amd64", binaries)
	if err != nil {
		t.Fatalf("PackageAppImage failed with fix script: %v", err)
	}

	if len(artifacts) < 2 {
		t.Fatalf("expected at least AppImage and SHA256 artifacts, got: %v", artifacts)
	}

	// Verify AppImage artifact exists
	appImagePath := artifacts[0]
	if !strings.HasSuffix(appImagePath, ".AppImage") {
		t.Errorf("expected first artifact to be .AppImage, got: %s", appImagePath)
	}
	if err := VerifyAppImage(appImagePath); err != nil {
		t.Errorf("repacked AppImage failed verification: %v", err)
	}

	// Verify the fix script received the expected environment.
	envData, err := os.ReadFile(envOut)
	if err != nil {
		t.Fatalf("fix script did not record its environment: %v", err)
	}
	wantEnv := strings.Join([]string{
		appImagePath,
		filepath.Join(tmpDir, "bin", "simtx-x86_64.AppDir"),
		cliBin,
		cfg.Linux.AppImage.UpdateInfo,
		toolsDir,
	}, "|") + "\n"
	if got := string(envData); got != wantEnv {
		t.Errorf("fix script environment mismatch:\n got: %q\nwant: %q", got, wantEnv)
	}

	// Verify the fix script received the expected positional arguments.
	argsData, err := os.ReadFile(argsOut)
	if err != nil {
		t.Fatalf("fix script did not record its arguments: %v", err)
	}
	wantArgs := strings.Join([]string{appImagePath, "simtx", cliBin}, "|") + "\n"
	if got := string(argsData); got != wantArgs {
		t.Errorf("fix script argument mismatch:\n got: %q\nwant: %q", got, wantArgs)
	}

	// Verify SHA256 file exists
	hasSHA := false
	for _, a := range artifacts {
		if strings.HasSuffix(a, ".sha256") {
			hasSHA = true
			if _, err := os.Stat(a); err != nil {
				t.Errorf("sha256 file does not exist: %v", err)
			}
		}
	}
	if !hasSHA {
		t.Errorf("expected .sha256 among artifacts, got: %v", artifacts)
	}
}

func TestResolveToolsDir(t *testing.T) {
	root := t.TempDir()

	cfg := &config.Config{RootDir: root}
	if got, want := resolveToolsDir(cfg), filepath.Join(root, "build", "tools"); got != want {
		t.Errorf("default tools dir: got %s, want %s", got, want)
	}

	cfg.Linux.AppImage.ToolsDir = "tools"
	if got, want := resolveToolsDir(cfg), filepath.Join(root, "tools"); got != want {
		t.Errorf("relative tools dir: got %s, want %s", got, want)
	}

	abs := filepath.Join(root, "absolute-tools")
	cfg.Linux.AppImage.ToolsDir = abs
	if got := resolveToolsDir(cfg); got != abs {
		t.Errorf("absolute tools dir: got %s, want %s", got, abs)
	}
}

func TestFindLinuxDeploy_HonorsToolsDir(t *testing.T) {
	root := t.TempDir()
	toolsDir := filepath.Join(root, "custom-tools")
	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		t.Fatalf("failed to create tools dir: %v", err)
	}
	ldPath := filepath.Join(toolsDir, "linuxdeploy-x86_64.AppImage")
	if err := os.WriteFile(ldPath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("failed to write linuxdeploy: %v", err)
	}

	cfg := &config.Config{RootDir: root}
	cfg.Linux.AppImage.ToolsDir = "custom-tools"

	got := FindLinuxDeploy(cfg)
	if got != ldPath {
		t.Errorf("FindLinuxDeploy ignored tools_dir: got %q, want %q", got, ldPath)
	}
}

func TestFindAppImageTool_HonorsToolsDir(t *testing.T) {
	root := t.TempDir()
	toolsDir := filepath.Join(root, "custom-tools")
	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		t.Fatalf("failed to create tools dir: %v", err)
	}
	toolPath := filepath.Join(toolsDir, "appimagetool")
	if err := os.WriteFile(toolPath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("failed to write appimagetool: %v", err)
	}

	cfg := &config.Config{RootDir: root}
	cfg.Linux.AppImage.ToolsDir = "custom-tools"

	got, err := FindAppImageTool(cfg)
	if err != nil {
		t.Fatalf("FindAppImageTool failed: %v", err)
	}
	if got != toolPath {
		t.Errorf("FindAppImageTool ignored tools_dir: got %q, want %q", got, toolPath)
	}
}

func TestPackageAppImage_WarnsWithoutLinuxDeploy(t *testing.T) {
	tmpDir := t.TempDir()

	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	guiBin := filepath.Join(binDir, "simtx")
	_ = os.WriteFile(guiBin, []byte("#!/bin/sh\necho simtx\n"), 0755)

	toolsDir := filepath.Join(tmpDir, "tools")
	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		t.Fatalf("failed to create tools dir: %v", err)
	}
	fakeTool := filepath.Join(toolsDir, "appimagetool")
	fakeScript := "#!/bin/sh\nfor last; do :; done\nprintf '\\x7fELF\\x02\\x01\\x01\\x00AI\\x02\\x00\\x00\\x00\\x00\\x00' > \"$last\"\n/bin/chmod +x \"$last\"\n"
	if err := os.WriteFile(fakeTool, []byte(fakeScript), 0755); err != nil {
		t.Fatalf("failed to write fake appimagetool: %v", err)
	}

	// Guarantee linuxdeploy is not discovered via PATH.
	t.Setenv("PATH", "")

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:      "simtx",
			OutputDir: "bin",
		},
		Linux: config.LinuxConfig{
			AppImage: config.AppImageConfig{
				ToolsDir:       "tools",
				GenerateSHA256: boolPtr(false),
				GenerateZsync:  boolPtr(false),
			},
		},
		RootDir: tmpDir,
	}

	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stderr pipe: %v", err)
	}
	os.Stderr = w
	_, pkgErr := PackageAppImage(context.Background(), cfg, "amd64", map[string]string{"simtx": guiBin})
	_ = w.Close()
	os.Stderr = oldStderr
	captured, _ := io.ReadAll(r)
	_ = r.Close()

	if pkgErr != nil {
		t.Fatalf("PackageAppImage failed: %v", pkgErr)
	}

	msg := string(captured)
	if !strings.Contains(msg, "WARNING") || !strings.Contains(msg, "WebKitGTK/GTK") || !strings.Contains(msg, "NOT bundled") {
		t.Errorf("expected prominent missing-linuxdeploy warning, got:\n%s", msg)
	}
}

func TestStageAppDir_PropagatesDesktopFileError(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	guiBin := filepath.Join(binDir, "simtx")
	_ = os.WriteFile(guiBin, []byte("#!/bin/sh\n"), 0755)

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:      "simtx",
			OutputDir: "bin",
		},
		Linux: config.LinuxConfig{
			DesktopFile: "missing.desktop",
		},
		RootDir: tmpDir,
	}

	_, err := StageAppDir(cfg, filepath.Join(tmpDir, "simtx.AppDir"), map[string]string{"simtx": guiBin})
	if err == nil {
		t.Fatal("expected StageAppDir to fail for a configured-but-missing desktop_file")
	}
	if !strings.Contains(err.Error(), "desktop") {
		t.Fatalf("expected desktop error, got: %v", err)
	}
}

func TestStageAppDir_NonPNGIconWarnsAndInstallsPlaceholder(t *testing.T) {
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
		RootDir: tmpDir,
	}

	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stderr pipe: %v", err)
	}
	os.Stderr = w
	appDir := filepath.Join(tmpDir, "simtx.AppDir")
	info, stageErr := StageAppDir(cfg, appDir, map[string]string{"simtx": guiBin})
	_ = w.Close()
	os.Stderr = oldStderr
	captured, _ := io.ReadAll(r)
	_ = r.Close()

	if stageErr != nil {
		t.Fatalf("StageAppDir failed: %v", stageErr)
	}
	if !strings.Contains(string(captured), "not a PNG") {
		t.Errorf("expected a non-PNG warning, got:\n%s", captured)
	}
	if info.Icon != filepath.Join(appDir, "simtx.png") {
		t.Errorf("unexpected staged icon path: %s", info.Icon)
	}
	data, err := os.ReadFile(info.Icon)
	if err != nil {
		t.Fatalf("staged icon missing: %v", err)
	}
	if !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		t.Errorf("staged icon is not a PNG")
	}

	desktop, err := os.ReadFile(filepath.Join(appDir, "simtx.desktop"))
	if err != nil {
		t.Fatalf("desktop file missing: %v", err)
	}
	if !strings.Contains(string(desktop), "Icon=simtx") {
		t.Errorf("desktop Icon= does not match installed icon name:\n%s", desktop)
	}
}

func TestEnsureZsyncFile_RequiresFreshSibling(t *testing.T) {
	tmpDir := t.TempDir()
	appImage := filepath.Join(tmpDir, "simtx-x86_64.AppImage")
	if err := os.WriteFile(appImage, []byte("appimage"), 0755); err != nil {
		t.Fatalf("failed to write AppImage: %v", err)
	}

	if _, err := EnsureZsyncFile(appImage); err == nil {
		t.Fatal("expected missing zsync to fail")
	}

	zsync := appImage + ".zsync"
	if err := os.WriteFile(zsync, []byte("zsync"), 0644); err != nil {
		t.Fatalf("failed to write zsync: %v", err)
	}
	got, err := EnsureZsyncFile(appImage)
	if err != nil {
		t.Fatalf("EnsureZsyncFile failed: %v", err)
	}
	if got != zsync {
		t.Errorf("got %q, want %q", got, zsync)
	}

	// A CWD-relative descriptor must not be considered.
	otherDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(otherDir, filepath.Base(appImage)+".zsync"), []byte("zsync"), 0644); err != nil {
		t.Fatalf("failed to write decoy zsync: %v", err)
	}
	t.Chdir(otherDir)
	if err := os.Remove(zsync); err != nil {
		t.Fatalf("failed to remove sibling zsync: %v", err)
	}
	if _, err := EnsureZsyncFile(appImage); err == nil {
		t.Fatal("expected CWD-relative zsync to be ignored")
	}
}

func TestSelectCLIBinary_Deterministic(t *testing.T) {
	tmpDir := t.TempDir()
	first := filepath.Join(tmpDir, "first")
	second := filepath.Join(tmpDir, "second")
	aCLI := filepath.Join(tmpDir, "a-cli")
	zCLI := filepath.Join(tmpDir, "z-cli")
	legacyCLI := filepath.Join(tmpDir, "legacy-cli")
	for _, p := range []string{first, second, aCLI, zCLI, legacyCLI} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0755); err != nil {
			t.Fatalf("failed to write %s: %v", p, err)
		}
	}

	cfg := &config.Config{Binaries: []config.BinaryConfig{
		{Name: "z-cli", GUI: false},
		{Name: "a-cli", GUI: false},
	}}
	binaries := map[string]string{
		"z-cli": zCLI,
		"a-cli": aCLI,
	}
	if name, path := selectCLIBinary(cfg, binaries, tmpDir); name != "z-cli" || path != zCLI {
		t.Errorf("expected cfg.Binaries order to win, got %q (%s)", name, path)
	}

	cfg.Binaries = nil
	if name, path := selectCLIBinary(cfg, binaries, tmpDir); name != "a-cli" || path != aCLI {
		t.Errorf("expected sorted fallback, got %q (%s)", name, path)
	}

	if name, path := selectCLIBinary(&config.Config{}, map[string]string{"cli": legacyCLI}, tmpDir); name != "cli" || path != legacyCLI {
		t.Errorf("expected cli fallback, got %q (%s)", name, path)
	}
}

func TestRunAppImageFixScript_HookAlias(t *testing.T) {
	tmpDir := t.TempDir()
	envOut := filepath.Join(tmpDir, "env.txt")
	script := filepath.Join(tmpDir, "fix.sh")
	content := "#!/usr/bin/env bash\nprintf '%s|%s|%s|%s|%s\\n' \"$APPIMAGE_PATH\" \"$APPDIR\" \"$CLI_BINARY\" \"$UPDATE_INFORMATION\" \"$APPIMAGETOOL_DIR\" > \"" + envOut + "\"\n"
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatalf("failed to write fix script: %v", err)
	}

	cfg := &config.Config{
		Package: config.PackageConfig{Name: "simtx", OutputDir: "bin"},
		Linux: config.LinuxConfig{
			AppImage: config.AppImageConfig{
				FixScript:  "appimage-fix",
				UpdateInfo: "zsync|https://example.invalid/app.zsync",
			},
		},
		Hooks: config.HooksConfig{
			FixScripts: map[string]string{"appimage-fix": script},
		},
		RootDir: tmpDir,
	}

	paths := &appImagePaths{
		OutDir:   filepath.Join(tmpDir, "bin"),
		Final:    filepath.Join(tmpDir, "bin", "simtx-x86_64.AppImage"),
		StageDir: filepath.Join(tmpDir, "bin", "simtx-x86_64.AppDir"),
	}
	info := &StagedAppDirInfo{BinaryName: "simtx", CLIBinary: filepath.Join(tmpDir, "bin", "simtx-cli")}

	if err := runAppImageFixScript(context.Background(), cfg, paths, info, nil); err != nil {
		t.Fatalf("runAppImageFixScript failed: %v", err)
	}

	data, err := os.ReadFile(envOut)
	if err != nil {
		t.Fatalf("fix script did not run: %v", err)
	}
	got := string(data)
	for _, want := range []string{paths.Final, paths.StageDir, info.CLIBinary, cfg.Linux.AppImage.UpdateInfo, filepath.Join(tmpDir, "build", "tools")} {
		if !strings.Contains(got, want) {
			t.Errorf("fix script env %q missing from %q", want, got)
		}
	}
}
