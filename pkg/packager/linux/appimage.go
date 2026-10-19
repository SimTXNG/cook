package linux

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/simtxng/cook/pkg/config"
	hookexec "github.com/simtxng/cook/pkg/exec"
	"github.com/simtxng/cook/pkg/fsutil"
)

// StagedAppDirInfo contains metadata about the staged AppDir.
type StagedAppDirInfo struct {
	AppDir     string
	CLIBinary  string
	Icon       string
	BinaryName string
}

// GetLinuxArch converts Go architectures to AppImage / Linux conventional architectures.
func GetLinuxArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	case "386":
		return "i686"
	case "arm":
		return "armhf"
	default:
		return goarch
	}
}

// resolveToolsDir returns linux.appimage.tools_dir resolved against the project
// root (never the process working directory). When unset it defaults to
// <root>/build/tools.
func resolveToolsDir(cfg *config.Config) string {
	if cfg.Linux.AppImage.ToolsDir != "" {
		return config.ResolvePath(cfg.RootDir, cfg.Linux.AppImage.ToolsDir)
	}
	return filepath.Join(cfg.RootDir, "build", "tools")
}

// isExecutableFile reports whether path exists, is a regular file, and is executable.
func isExecutableFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir() && fi.Mode()&0111 != 0
}

// FindAppImageTool locates appimagetool in tools_dir, the legacy project tools
// directory, or on PATH, downloading it when absent.
func FindAppImageTool(cfg *config.Config) (string, error) {
	toolsDir := resolveToolsDir(cfg)

	// 1. Search each candidate name in every tools directory exactly once.
	// tools_dir and the legacy <root>/tools directory are often the same path.
	dirs := []string{toolsDir}
	if legacy := filepath.Join(cfg.RootDir, "tools"); cfg.RootDir != "" && legacy != toolsDir {
		dirs = append(dirs, legacy)
	}
	for _, dir := range dirs {
		for _, name := range []string{"appimagetool-x86_64.AppImage", "appimagetool"} {
			if c := filepath.Join(dir, name); isExecutableFile(c) {
				return c, nil
			}
		}
	}

	// 2. Check PATH.
	for _, name := range []string{"appimagetool", "appimagetool-x86_64.AppImage"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}

	// 3. Download appimagetool if absent. Download to a temporary file and only
	// rename it into place after a successful transfer so a partial download is
	// never executed.
	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create tools directory %s: %w", toolsDir, err)
	}
	aitPath := filepath.Join(toolsDir, "appimagetool-x86_64.AppImage")
	downloadURL := "https://github.com/AppImage/appimagetool/releases/download/continuous/appimagetool-x86_64.AppImage"

	tmpFile, err := os.CreateTemp(toolsDir, ".appimagetool-download-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary download file in %s: %w", toolsDir, err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer os.Remove(tmpPath)

	fmt.Printf("==> Downloading appimagetool to %s...\n", aitPath)
	fmt.Fprintf(os.Stderr, "WARNING: %s has no checksum available; the download cannot be verified.\n", downloadURL)

	dlCmd := exec.Command("wget", "-q", "-4", "-O", tmpPath, downloadURL)
	if err := dlCmd.Run(); err != nil {
		return "", fmt.Errorf("failed to download appimagetool from %s: %w", downloadURL, err)
	}

	if err := os.Chmod(tmpPath, 0755); err != nil {
		return "", fmt.Errorf("failed to make downloaded appimagetool executable: %w", err)
	}
	if err := os.Rename(tmpPath, aitPath); err != nil {
		return "", fmt.Errorf("failed to install downloaded appimagetool at %s: %w", aitPath, err)
	}

	return aitPath, nil
}

// FindLinuxDeploy locates the linuxdeploy executable if available in tools_dir,
// project build locations, or PATH.
func FindLinuxDeploy(cfg *config.Config) string {
	toolsDir := resolveToolsDir(cfg)
	candidates := []string{
		filepath.Join(toolsDir, "linuxdeploy-x86_64.AppImage"),
		filepath.Join(toolsDir, "linuxdeploy"),
	}
	if cfg.RootDir != "" {
		candidates = append(candidates,
			filepath.Join(cfg.RootDir, "build", "linux", "appimage", "build", "linuxdeploy-x86_64.AppImage"),
			filepath.Join(cfg.RootDir, "tools", "linuxdeploy-x86_64.AppImage"),
		)
	}
	for _, c := range candidates {
		if isExecutableFile(c) {
			if abs, err := filepath.Abs(c); err == nil {
				return abs
			}
			return c
		}
	}
	for _, name := range []string{"linuxdeploy", "linuxdeploy-x86_64.AppImage"} {
		if path, err := exec.LookPath(name); err == nil {
			if abs, err := filepath.Abs(path); err == nil {
				return abs
			}
			return path
		}
	}
	return ""
}

// StageAppDir creates the complete standard AppDir filesystem structure.
func StageAppDir(cfg *config.Config, appDir string, binaries map[string]string) (*StagedAppDirInfo, error) {
	return stageAppDir(cfg, appDir, binaries, runtime.GOARCH)
}

func stageAppDir(cfg *config.Config, appDir string, binaries map[string]string, arch string) (*StagedAppDirInfo, error) {
	outDir := filepath.Join(cfg.RootDir, cfg.Package.OutputDir)
	appName := cfg.Package.Name

	// 1. Create required AppDir directories
	dirs := []string{
		filepath.Join(appDir, "usr", "bin"),
		filepath.Join(appDir, "usr", "lib"),
		filepath.Join(appDir, "usr", "share", "applications"),
		filepath.Join(appDir, "usr", "share", "icons", "hicolor", "128x128", "apps"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", d, err)
		}
	}

	info := &StagedAppDirInfo{
		AppDir:     appDir,
		BinaryName: appName,
	}

	// 2. Stage GUI Binary -> usr/bin/<name>
	guiSrc := findBinary(appName, binaries, outDir)
	if guiSrc == "" {
		return nil, fmt.Errorf("no GUI binary found for %q", appName)
	}
	guiDst := filepath.Join(appDir, "usr", "bin", appName)
	if err := fsutil.CopyExecutable(guiSrc, guiDst); err != nil {
		return nil, fmt.Errorf("failed to copy GUI binary: %w", err)
	}

	// 3. Stage Companion CLI Binary -> usr/bin/<cli-name>
	cliName, cliSrc := selectCLIBinary(cfg, binaries, outDir)
	if cliSrc != "" {
		cliDst := filepath.Join(appDir, "usr", "bin", cliName)
		if err := fsutil.CopyExecutable(cliSrc, cliDst); err != nil {
			return nil, fmt.Errorf("failed to copy CLI binary: %w", err)
		}
		info.CLIBinary = cliDst
	}

	// 4. Desktop Entry -> <name>.desktop & usr/share/applications/<name>.desktop
	if _, err := stageDesktopEntry(cfg, appDir, appName); err != nil {
		return nil, err
	}

	// 5. Icon -> <name>.png, .DirIcon, & usr/share/icons/hicolor/128x128/apps/<name>.png
	iconPath, err := stageAppIcon(cfg, appDir, appName)
	if err != nil {
		return nil, err
	}
	info.Icon = iconPath

	// 6. AppRun wrapper -> AppRun
	if err := CreateAppRunWrapper(appDir, appName); err != nil {
		return nil, fmt.Errorf("failed to create AppRun wrapper: %w", err)
	}

	// 7. WebKit helper processes (required for WebKitGTK out-of-process architecture)
	if err := stageWebKitHelpers(appDir, cfg.RootDir, arch); err != nil {
		return nil, fmt.Errorf("failed to stage WebKit helpers: %w", err)
	}

	return info, nil
}

// stageDesktopEntry generates/copies the desktop entry into the AppDir root and
// the standard share/applications directory. It returns the root entry path.
func stageDesktopEntry(cfg *config.Config, appDir, appName string) (string, error) {
	desktopDstRoot := filepath.Join(appDir, fmt.Sprintf("%s.desktop", appName))
	desktopDstShare := filepath.Join(appDir, "usr", "share", "applications", fmt.Sprintf("%s.desktop", appName))
	_ = os.Remove(desktopDstRoot)
	_ = os.Remove(desktopDstShare)
	if _, err := EnsureDesktopFile(cfg, desktopDstRoot); err != nil {
		return "", fmt.Errorf("failed to generate desktop entry: %w", err)
	}
	if err := fsutil.Copy(desktopDstRoot, desktopDstShare, 0644); err != nil {
		return "", fmt.Errorf("failed to copy desktop entry to share: %w", err)
	}
	return desktopDstRoot, nil
}

// stageAppIcon stages the icon as <name>.png so it matches the desktop file's
// Icon= field. A configured non-PNG source cannot be converted here; warn and
// install a generated placeholder rather than renaming the wrong format.
func stageAppIcon(cfg *config.Config, appDir, appName string) (string, error) {
	iconDstRoot := filepath.Join(appDir, fmt.Sprintf("%s.png", appName))
	iconDstDirIcon := filepath.Join(appDir, ".DirIcon")
	iconDstShare := filepath.Join(appDir, "usr", "share", "icons", "hicolor", "128x128", "apps", fmt.Sprintf("%s.png", appName))
	_ = os.Remove(iconDstRoot)
	_ = os.Remove(iconDstDirIcon)
	_ = os.Remove(iconDstShare)

	iconSrc := resolveFile(cfg.Package.Icon, cfg.RootDir)
	switch {
	case iconSrc == "":
		if err := createFallbackPNG(iconDstRoot); err != nil {
			return "", fmt.Errorf("failed to create fallback icon: %w", err)
		}
	case !isPNGIcon(iconSrc):
		fmt.Fprintf(os.Stderr, "WARNING: icon %s is not a PNG; installing a generated placeholder as %s.png (no icon converter available).\n", iconSrc, appName)
		if err := createFallbackPNG(iconDstRoot); err != nil {
			return "", fmt.Errorf("failed to create fallback icon: %w", err)
		}
	default:
		if err := fsutil.Copy(iconSrc, iconDstRoot, 0644); err != nil {
			return "", fmt.Errorf("failed to copy icon: %w", err)
		}
	}
	if err := fsutil.Copy(iconDstRoot, iconDstDirIcon, 0644); err != nil {
		return "", fmt.Errorf("failed to copy .DirIcon: %w", err)
	}
	if err := fsutil.Copy(iconDstRoot, iconDstShare, 0644); err != nil {
		return "", fmt.Errorf("failed to copy icon to share directory: %w", err)
	}
	return iconDstRoot, nil
}

// AppRunTemplate is the standard wrapper script that executes the app binary in AppDir.
const AppRunTemplate = `#!/bin/sh
APPDIR="$(dirname "$(readlink -f "$0")")"
exec "$APPDIR/usr/bin/%s" "$@"
`

// CreateAppRunWrapper writes the AppRun wrapper into appDir.
func CreateAppRunWrapper(appDir, binaryName string) error {
	appRunPath := filepath.Join(appDir, "AppRun")
	content := fmt.Sprintf(AppRunTemplate, binaryName)
	if err := os.WriteFile(appRunPath, []byte(content), 0755); err != nil {
		return fmt.Errorf("failed to write AppRun wrapper to %s: %w", appRunPath, err)
	}
	return nil
}

// RepackAppImage calls appimagetool to produce the final .AppImage file from appDir.
func RepackAppImage(ctx context.Context, cfg *config.Config, appDir, outPath, arch string) error {
	toolPath, err := FindAppImageTool(cfg)
	if err != nil {
		return err
	}

	archName := GetLinuxArch(arch)

	var args []string
	if cfg.Linux.AppImage.UpdateInfo != "" {
		args = append(args, "-u", cfg.Linux.AppImage.UpdateInfo)
	}
	args = append(args, appDir, outPath)

	cmd := exec.CommandContext(ctx, toolPath, args...)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("ARCH=%s", archName),
		"APPIMAGE_EXTRACT_AND_RUN=1",
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("appimagetool failed (%s): %w", strings.TrimSpace(string(out)), err)
	}

	return nil
}

// GenerateSHA256 computes the SHA-256 digest of filePath and writes it to filePath.sha256.
func GenerateSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file for sha256 checksum: %w", err)
	}
	defer f.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", fmt.Errorf("failed to calculate sha256: %w", err)
	}

	hexDigest := hex.EncodeToString(hasher.Sum(nil))
	baseName := filepath.Base(filePath)
	content := fmt.Sprintf("%s  %s\n", hexDigest, baseName)

	shaPath := filePath + ".sha256"
	if err := os.WriteFile(shaPath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to write sha256 file: %w", err)
	}
	return shaPath, nil
}

// EnsureZsyncFile locates the .zsync descriptor written next to appImagePath.
// Only that sibling is considered (never a CWD-relative name), and a descriptor
// older than the AppImage it describes is stale and rejected.
func EnsureZsyncFile(appImagePath string) (string, error) {
	imgInfo, err := os.Stat(appImagePath)
	if err != nil {
		return "", fmt.Errorf("cannot stat AppImage for zsync: %w", err)
	}
	zsyncPath := appImagePath + ".zsync"
	fi, err := os.Stat(zsyncPath)
	if err != nil || fi.IsDir() || fi.ModTime().Before(imgInfo.ModTime()) {
		return "", fmt.Errorf("fresh zsync file not found for %s", appImagePath)
	}
	return zsyncPath, nil
}

// VerifyAppImage asserts that the repacked AppImage exists, is non-empty, executable, and has valid ELF header.
func VerifyAppImage(appImagePath string) error {
	fi, err := os.Stat(appImagePath)
	if err != nil {
		return fmt.Errorf("appimage does not exist: %w", err)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("appimage file is empty (0 bytes)")
	}
	if fi.Mode()&0111 == 0 {
		return fmt.Errorf("appimage file is not executable (mode %v)", fi.Mode())
	}

	f, err := os.Open(appImagePath)
	if err != nil {
		return fmt.Errorf("failed to open appimage for verification: %w", err)
	}
	defer f.Close()

	header := make([]byte, 16)
	n, err := f.Read(header)
	if err != nil || n < 4 {
		return fmt.Errorf("failed to read header from appimage: %w", err)
	}

	// Verify ELF magic: \x7f E L F
	if !bytes.Equal(header[:4], []byte{0x7f, 'E', 'L', 'F'}) {
		return fmt.Errorf("appimage header does not contain valid ELF magic (got %v)", header[:4])
	}

	return nil
}

// appImagePaths holds the per-run AppImage output locations.
type appImagePaths struct {
	OutDir   string
	Final    string
	StageDir string
}

// PackageAppImage executes the complete AppImage packaging pipeline:
// staging the AppDir, deploying dependencies/repacking, running the optional
// fix script, and collecting the generated artifacts.
func PackageAppImage(ctx context.Context, cfg *config.Config, arch string, binaries map[string]string) ([]string, error) {
	paths, err := prepareAppImagePaths(cfg, arch)
	if err != nil {
		return nil, err
	}

	stagedInfo, err := stageAppDir(cfg, paths.StageDir, binaries, arch)
	if err != nil {
		return nil, fmt.Errorf("failed to stage AppDir: %w", err)
	}

	if err := deployAppImage(ctx, cfg, arch, paths); err != nil {
		return nil, err
	}

	if err := verifyProducedAppImage(paths.Final); err != nil {
		return nil, err
	}

	if err := runAppImageFixScript(ctx, cfg, paths, stagedInfo, binaries); err != nil {
		return nil, err
	}

	if err := VerifyAppImage(paths.Final); err != nil {
		return nil, fmt.Errorf("verification of repacked AppImage failed: %w", err)
	}

	return collectAppImageArtifacts(cfg, paths.Final)
}

// prepareAppImagePaths creates the output directory and removes stale artifacts
// from previous runs so neither the AppDir nor an old AppImage/.zsync can be
// mistaken for this run's output.
func prepareAppImagePaths(cfg *config.Config, arch string) (*appImagePaths, error) {
	outDir := filepath.Join(cfg.RootDir, cfg.Package.OutputDir)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory %s: %w", outDir, err)
	}

	archName := GetLinuxArch(arch)
	appName := cfg.Package.Name
	paths := &appImagePaths{
		OutDir:   outDir,
		Final:    filepath.Join(outDir, fmt.Sprintf("%s-%s.AppImage", appName, archName)),
		StageDir: filepath.Join(outDir, fmt.Sprintf("%s-%s.AppDir", appName, archName)),
	}

	if err := os.RemoveAll(paths.StageDir); err != nil {
		return nil, fmt.Errorf("failed to clean staged AppDir %s: %w", paths.StageDir, err)
	}
	if err := os.Remove(paths.Final); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to remove stale AppImage %s: %w", paths.Final, err)
	}
	if err := os.Remove(paths.Final + ".zsync"); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to remove stale zsync %s: %w", paths.Final+".zsync", err)
	}

	return paths, nil
}

// deployAppImage bundles dependencies with linuxdeploy when available, falling
// back to a plain appimagetool repack (with a prominent warning) otherwise.
func deployAppImage(ctx context.Context, cfg *config.Config, arch string, paths *appImagePaths) error {
	archName := GetLinuxArch(arch)
	ldPath := FindLinuxDeploy(cfg)
	if ldPath != "" {
		fmt.Printf("==> Deploying dependencies with linuxdeploy: %s\n", ldPath)
		absStageAppDir, err := filepath.Abs(paths.StageDir)
		if err != nil {
			absStageAppDir = paths.StageDir
		}
		cmd := exec.CommandContext(ctx, ldPath, "--appdir", absStageAppDir, "--output", "appimage")
		cmd.Dir = paths.OutDir
		cmd.Env = append(os.Environ(),
			"APPIMAGE_EXTRACT_AND_RUN=1",
			fmt.Sprintf("ARCH=%s", archName),
			fmt.Sprintf("UPDATE_INFORMATION=%s", cfg.Linux.AppImage.UpdateInfo),
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("linuxdeploy failed (%s): %w", strings.TrimSpace(string(out)), err)
		}
		return nil
	}

	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
	fmt.Fprintln(os.Stderr, "WARNING: linuxdeploy was not found.")
	fmt.Fprintln(os.Stderr, "WARNING: The AppImage will be repacked with appimagetool only, so WebKitGTK/GTK")
	fmt.Fprintln(os.Stderr, "WARNING: runtime libraries were NOT bundled. The resulting AppImage may only")
	fmt.Fprintln(os.Stderr, "WARNING: run on this development machine or other hosts with those libraries.")
	fmt.Fprintln(os.Stderr, "WARNING: Install linuxdeploy or set linux.appimage.tools_dir to bundle them.")
	fmt.Fprintln(os.Stderr, "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
	fmt.Fprintln(os.Stderr, "")

	if err := RepackAppImage(ctx, cfg, paths.StageDir, paths.Final, arch); err != nil {
		return fmt.Errorf("repacking AppImage failed: %w", err)
	}
	return nil
}

// verifyProducedAppImage only accepts the artifact produced by this run at the
// expected path.
func verifyProducedAppImage(finalAppImagePath string) error {
	if fi, err := os.Stat(finalAppImagePath); err != nil || fi.IsDir() {
		if err == nil {
			err = fmt.Errorf("path is a directory")
		}
		return fmt.Errorf("expected freshly built AppImage %s was not produced: %w", finalAppImagePath, err)
	}
	return nil
}

// runAppImageFixScript invokes the configured fix script through the shared
// HookManager so hooks.fix_scripts and direct paths behave identically.
func runAppImageFixScript(ctx context.Context, cfg *config.Config, paths *appImagePaths, stagedInfo *StagedAppDirInfo, binaries map[string]string) error {
	if cfg.Linux.AppImage.FixScript == "" {
		return nil
	}

	scriptNameOrPath := cfg.Linux.AppImage.FixScript
	scriptPath := scriptNameOrPath
	if mapped, ok := cfg.Hooks.FixScripts[scriptNameOrPath]; ok {
		scriptPath = mapped
	}
	if !filepath.IsAbs(scriptPath) && cfg.RootDir != "" {
		scriptPath = config.ResolvePath(cfg.RootDir, scriptPath)
	}
	if _, err := os.Stat(scriptPath); err != nil {
		return fmt.Errorf("fix script %s not found: %w", scriptPath, err)
	}

	cliHostPath := findBinary(cfg.Linux.BundledCLI, binaries, paths.OutDir)
	if cliHostPath == "" {
		cliHostPath = stagedInfo.CLIBinary
	}

	fmt.Printf("==> Invoking external fix script: %s\n", scriptPath)
	args := []string{paths.Final, stagedInfo.BinaryName}
	if cliHostPath != "" {
		args = append(args, cliHostPath)
	}

	extraEnv := map[string]string{
		"APPIMAGE_PATH":      paths.Final,
		"APPDIR":             paths.StageDir,
		"CLI_BINARY":         cliHostPath,
		"UPDATE_INFORMATION": cfg.Linux.AppImage.UpdateInfo,
		"APPIMAGETOOL_DIR":   resolveToolsDir(cfg),
	}
	if err := hookexec.NewHookManager(cfg).RunFixScript(ctx, scriptNameOrPath, args, extraEnv); err != nil {
		return fmt.Errorf("fix script %s failed: %w", scriptPath, err)
	}
	return nil
}

// collectAppImageArtifacts returns the AppImage plus its optional .zsync and
// .sha256 sidecars. Any sidecar failure aborts instead of returning a partial
// artifact list as success.
func collectAppImageArtifacts(cfg *config.Config, finalAppImagePath string) ([]string, error) {
	artifacts := []string{finalAppImagePath}

	if cfg.Linux.AppImage.ZsyncEnabled() {
		zsyncPath, err := EnsureZsyncFile(finalAppImagePath)
		if err != nil {
			return nil, fmt.Errorf("failed to generate zsync for AppImage: %w", err)
		}
		artifacts = append(artifacts, zsyncPath)
	}

	if cfg.Linux.AppImage.SHA256Enabled() {
		shaPath, err := GenerateSHA256(finalAppImagePath)
		if err != nil {
			return nil, fmt.Errorf("failed to generate sha256 for AppImage: %w", err)
		}
		artifacts = append(artifacts, shaPath)
	}

	return artifacts, nil
}

func createFallbackPNG(path string) error {
	img := image.NewRGBA(image.Rect(0, 0, 128, 128))
	blue := color.RGBA{R: 50, G: 100, B: 200, A: 255}
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			img.Set(x, y, blue)
		}
	}
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func stageWebKitHelpers(appDir string, rootDir string, goarch string) error {
	dirs := []string{"/usr/libexec"}
	if triplet := linuxMultiarchTriplet(goarch); triplet != "" {
		dirs = append(dirs, filepath.Join("/usr/lib", triplet))
	}
	dirs = append(dirs, "/usr/lib64", "/usr/lib")

	var candidates []string
	for _, dir := range dirs {
		for _, name := range []string{"webkitgtk-6.0", "webkit2gtk-4.1"} {
			candidates = append(candidates, filepath.Join(dir, name))
		}
	}
	if rootDir != "" {
		matches, _ := filepath.Glob(filepath.Join(rootDir, "build", "linux", "appimage", "build", "*.AppDir", "usr", "libexec", "webkit*"))
		candidates = append(candidates, matches...)
	}

	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			base := filepath.Base(c)
			dst := filepath.Join(appDir, "usr", "libexec", base)
			if err := fsutil.CopyDir(c, dst); err != nil {
				return fmt.Errorf("failed to copy %s: %w", c, err)
			}
		}
	}
	return nil
}

// linuxMultiarchTriplet maps a Go architecture to the Debian-style multiarch
// directory name used under /usr/lib.
func linuxMultiarchTriplet(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64-linux-gnu"
	case "arm64":
		return "aarch64-linux-gnu"
	case "386":
		return "i386-linux-gnu"
	case "arm":
		return "arm-linux-gnueabihf"
	case "ppc64le":
		return "powerpc64le-linux-gnu"
	case "s390x":
		return "s390x-linux-gnu"
	case "riscv64":
		return "riscv64-linux-gnu"
	default:
		return ""
	}
}

// isPNGIcon reports whether path names a PNG icon. No image converter is
// available at packaging time, so non-PNG sources must not be renamed to .png.
func isPNGIcon(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".png")
}
