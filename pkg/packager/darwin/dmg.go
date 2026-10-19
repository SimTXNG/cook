package darwin

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/simtxng/cook/pkg/config"
	cookExec "github.com/simtxng/cook/pkg/exec"
	"github.com/simtxng/cook/pkg/fsutil"
)

// DMGOptions stores the resolved DMG styling parameters.
type DMGOptions struct {
	VolumeName           string
	WindowWidth          int
	WindowHeight         int
	AppPosition          []int
	ApplicationsPosition []int
	Background           string
	VolumeIcon           string
}

// ResolveDMGOptions extracts styling options from config. Window sizing,
// icon positions and min-OS defaults are applied once by config.applyDefaults;
// only the volume name and image paths are resolved here.
func ResolveDMGOptions(cfg *config.Config) DMGOptions {
	volName := cfg.Package.ProductName
	if volName == "" {
		volName = cfg.Package.Name
	}

	opts := DMGOptions{
		VolumeName:           volName,
		WindowWidth:          cfg.Darwin.DMG.WindowWidth,
		WindowHeight:         cfg.Darwin.DMG.WindowHeight,
		AppPosition:          cfg.Darwin.DMG.AppPosition,
		ApplicationsPosition: cfg.Darwin.DMG.ApplicationsPosition,
	}

	// Resolve background image
	bg := cfg.Darwin.DMG.Background
	if bg == "" {
		candidate := filepath.Join(cfg.RootDir, "build", "darwin", "dmg-background.png")
		if fileExists(candidate) {
			bg = candidate
		}
	} else if !filepath.IsAbs(bg) {
		bg = filepath.Join(cfg.RootDir, bg)
	}
	opts.Background = bg

	// Resolve volume icon
	volIcon := cfg.Darwin.DMG.VolumeIcon
	if volIcon == "" {
		candidate := filepath.Join(cfg.RootDir, "build", "darwin", "icons.icns")
		if fileExists(candidate) {
			volIcon = candidate
		}
	} else if !filepath.IsAbs(volIcon) {
		volIcon = filepath.Join(cfg.RootDir, volIcon)
	}
	opts.VolumeIcon = volIcon

	return opts
}

// dmgPosition returns the first two coordinates of pos, falling back to the
// default when the slice is absent or too short.
func dmgPosition(pos []int, defX, defY int) (int, int) {
	if len(pos) < 2 {
		return defX, defY
	}
	return pos[0], pos[1]
}

// BuildCreateDMGArgs constructs the argument slice for the create-dmg CLI tool.
func BuildCreateDMGArgs(opts DMGOptions, appBundlePath, dmgPath string) []string {
	appBase := filepath.Base(appBundlePath)
	appX, appY := dmgPosition(opts.AppPosition, 135, 190)
	appsX, appsY := dmgPosition(opts.ApplicationsPosition, 405, 190)
	args := []string{
		"--volname", opts.VolumeName,
		"--window-pos", "200", "120",
		"--window-size", fmt.Sprintf("%d", opts.WindowWidth), fmt.Sprintf("%d", opts.WindowHeight),
		"--icon-size", "100",
		"--icon", appBase, fmt.Sprintf("%d", appX), fmt.Sprintf("%d", appY),
		"--hide-extension", appBase,
		"--app-drop-link", fmt.Sprintf("%d", appsX), fmt.Sprintf("%d", appsY),
	}

	if opts.Background != "" && fileExists(opts.Background) {
		args = append(args, "--background", opts.Background)
	}
	if opts.VolumeIcon != "" && fileExists(opts.VolumeIcon) {
		args = append(args, "--volicon", opts.VolumeIcon)
	}

	// Output dmg and source directory
	sourceDir := filepath.Dir(appBundlePath)
	args = append(args, dmgPath, sourceDir)
	return args
}

// stageAppForCreateDMG copies appBundlePath into a fresh temporary directory
// that contains nothing but the .app bundle. create-dmg uses the source
// directory wholesale, so staging prevents sibling artifacts such as
// entitlements.plist from being dragged into the disk image.
func stageAppForCreateDMG(appBundlePath string) (stagingDir, stagedApp string, err error) {
	stagingDir, err = os.MkdirTemp("", "cook-dmg-staging-*")
	if err != nil {
		return "", "", err
	}
	stagedApp = filepath.Join(stagingDir, filepath.Base(appBundlePath))
	if err := fsutil.CopyDir(appBundlePath, stagedApp); err != nil {
		os.RemoveAll(stagingDir)
		return "", "", fmt.Errorf("failed to stage .app bundle for create-dmg: %w", err)
	}
	return stagingDir, stagedApp, nil
}

// BuildDMG generates a styled macOS disk image (.dmg).
func BuildDMG(ctx context.Context, cfg *config.Config, appBundlePath string) (string, error) {
	outDir := filepath.Join(cfg.RootDir, cfg.Package.OutputDir)
	dmgName := fmt.Sprintf("%s.dmg", cfg.Package.Name)
	dmgPath := filepath.Join(outDir, dmgName)

	_ = os.Remove(dmgPath) // Clean previous dmg if any

	opts := ResolveDMGOptions(cfg)
	runner := cookExec.NewRunner("dmg")

	// 1. Check for `create-dmg` tool
	if createDmgPath, err := exec.LookPath("create-dmg"); err == nil {
		fmt.Printf("==> Building styled DMG using create-dmg -> %s\n", dmgPath)
		// create-dmg expects a clean source folder containing the app
		stagingDir, stagedApp, err := stageAppForCreateDMG(appBundlePath)
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(stagingDir)

		args := BuildCreateDMGArgs(opts, stagedApp, dmgPath)

		if err := runner.RunArgs(ctx, createDmgPath, args, cfg.RootDir, nil); err != nil {
			return "", fmt.Errorf("create-dmg failed: %w", err)
		}
		return dmgPath, nil
	}

	// 2. Check for native macOS `hdiutil`
	if hdiutilPath, err := exec.LookPath("hdiutil"); err == nil && runtime.GOOS == "darwin" {
		fmt.Printf("==> Building DMG using native hdiutil fallback -> %s\n", dmgPath)
		fmt.Fprintln(os.Stderr, "==> WARNING: create-dmg was not found; the hdiutil fallback cannot apply Finder window styling (background image, icon positions). Install create-dmg for a styled DMG.")
		return buildDMGWithHdiutil(ctx, cfg, opts, appBundlePath, dmgPath, hdiutilPath, runner)
	}

	// 3. Fallback when cross-compiling on Linux / non-macOS without create-dmg
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("DMG generation on %s requires 'create-dmg' installed in PATH (or run cook on macOS with hdiutil)", runtime.GOOS)
	}

	return "", fmt.Errorf("neither 'create-dmg' nor 'hdiutil' was found in PATH to create DMG")
}

const dmgHeadroomBytes int64 = 64 << 20

// calculateDMGSize returns the disk image size required to hold the app bundle
// plus generous headroom, rounded up to a whole MiB.
func calculateDMGSize(appBundlePath string) (int64, error) {
	var total int64
	err := filepath.Walk(appBundlePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("failed to measure app bundle %s: %w", appBundlePath, err)
	}

	const maxInt64 = int64(^uint64(0) >> 1)
	if total > maxInt64-dmgHeadroomBytes {
		return 0, fmt.Errorf("app bundle %s is too large to size a DMG", appBundlePath)
	}

	size := total + dmgHeadroomBytes
	const mib = int64(1 << 20)
	size = ((size + mib - 1) / mib) * mib
	if size <= total {
		return 0, fmt.Errorf("computed DMG size %d bytes cannot fit app bundle of %d bytes", size, total)
	}
	return size, nil
}

func buildDMGWithHdiutil(ctx context.Context, cfg *config.Config, opts DMGOptions, appBundlePath, dmgPath, hdiutilPath string, runner *cookExec.Runner) (string, error) {
	tempDMG := dmgPath + ".temp.dmg"
	defer os.Remove(tempDMG)

	imageSize, err := calculateDMGSize(appBundlePath)
	if err != nil {
		return "", err
	}

	// Create temporary disk image large enough for the app bundle.
	createArgs := []string{
		"create",
		"-size", fmt.Sprintf("%dm", imageSize>>20),
		"-fs", "HFS+",
		"-volname", opts.VolumeName,
		"-ov",
		tempDMG,
	}
	if err := runner.RunArgs(ctx, hdiutilPath, createArgs, cfg.RootDir, nil); err != nil {
		return "", fmt.Errorf("hdiutil create failed: %w", err)
	}

	// Mount the image
	mountPoint, err := os.MkdirTemp("", "cook-hdiutil-mount-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(mountPoint)

	attachArgs := []string{
		"attach",
		tempDMG,
		"-mountpoint", mountPoint,
		"-nobrowse",
		"-noverify",
	}
	if err := runner.RunArgs(ctx, hdiutilPath, attachArgs, cfg.RootDir, nil); err != nil {
		return "", fmt.Errorf("hdiutil attach failed: %w", err)
	}

	// Stage the image contents. The deferred detach runs exactly once on every
	// path (including errors) and deliberately ignores caller cancellation so
	// an aborted build cannot leak a mounted volume.
	if err := func() (err error) {
		defer func() {
			detachCtx := context.WithoutCancel(ctx)
			detachErr := runner.RunArgs(detachCtx, hdiutilPath, []string{"detach", mountPoint, "-force"}, cfg.RootDir, nil)
			if detachErr != nil && err == nil {
				err = fmt.Errorf("hdiutil detach failed: %w", detachErr)
			}
		}()

		// Copy .app bundle into mount point
		destApp := filepath.Join(mountPoint, filepath.Base(appBundlePath))
		if err := fsutil.CopyDir(appBundlePath, destApp); err != nil {
			return fmt.Errorf("failed to copy app into DMG mount: %w", err)
		}

		// Create /Applications symlink
		appsSymlink := filepath.Join(mountPoint, "Applications")
		if err := os.Symlink("/Applications", appsSymlink); err != nil && !os.IsExist(err) {
			return fmt.Errorf("failed to create Applications symlink in DMG: %w", err)
		}

		// Copy background if provided. Note: hdiutil alone cannot create the
		// Finder layout that references it; create-dmg is required for styling.
		if opts.Background != "" && fileExists(opts.Background) {
			bgDir := filepath.Join(mountPoint, ".background")
			_ = os.MkdirAll(bgDir, 0755)
			if bgInfo, err := os.Stat(opts.Background); err == nil {
				_ = fsutil.Copy(opts.Background, filepath.Join(bgDir, filepath.Base(opts.Background)), bgInfo.Mode().Perm())
			}
		}

		return nil
	}(); err != nil {
		return "", err
	}

	// Convert to compressed, read-only UDZO image
	convertArgs := []string{
		"convert",
		tempDMG,
		"-format", "UDZO",
		"-imagekey", "zlib-level=9",
		"-o", dmgPath,
	}
	if err := runner.RunArgs(ctx, hdiutilPath, convertArgs, cfg.RootDir, nil); err != nil {
		return "", fmt.Errorf("hdiutil convert failed: %w", err)
	}

	return dmgPath, nil
}
