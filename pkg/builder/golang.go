package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/simtxng/cook/pkg/config"
	"github.com/simtxng/cook/pkg/exec"
)

// GoBuildOptions controls the optional parts of a Go build command line.
type GoBuildOptions struct {
	// ExtraTags are appended after the binary's configured tags.
	ExtraTags []string
	// ExcludeTags are removed from the binary's configured tags.
	ExcludeTags []string
	// GCFlags, when non-empty, emits -gcflags=<GCFlags>, e.g. "all=-l".
	GCFlags string
	// Trimpath emits -trimpath.
	Trimpath bool
	// BuildVCS keeps VCS stamping enabled by omitting -buildvcs=false.
	BuildVCS bool
	// Ldflags includes the binary's configured ldflags and, for windows GUI
	// binaries, -H windowsgui. The commit ldflag is always included.
	Ldflags bool
	// OutFile overrides the -o target; defaults to GoOutputPath.
	OutFile string
	// SourcePath overrides the built package; defaults to the binary path or ".".
	SourcePath string
}

// GoOutputPath returns the output file for b, appending the .exe suffix for
// windows targets.
func GoOutputPath(cfg *config.Config, b *config.BinaryConfig, targetOS string) string {
	name := b.Name
	if targetOS == "windows" && !strings.HasSuffix(name, ".exe") {
		name += ".exe"
	}
	return filepath.Join(cfg.RootDir, cfg.Package.OutputDir, name)
}

// GoBuildArgs assembles the `go build` argument list shared by the release
// builder and the development supervisor.
func GoBuildArgs(cfg *config.Config, b *config.BinaryConfig, targetOS string, o GoBuildOptions) []string {
	targetOS = config.NormalizeOS(targetOS)

	args := []string{"build"}
	if o.Trimpath {
		args = append(args, "-trimpath")
	}
	if !o.BuildVCS {
		args = append(args, "-buildvcs=false")
	}
	if o.GCFlags != "" {
		args = append(args, "-gcflags="+o.GCFlags)
	}

	tags := make([]string, 0, len(b.Tags)+len(o.ExtraTags))
	for _, tag := range b.Tags {
		if slices.Contains(o.ExcludeTags, tag) {
			continue
		}
		tags = append(tags, tag)
	}
	tags = append(tags, o.ExtraTags...)
	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}

	var ldflags []string
	if o.Ldflags {
		if b.Ldflags != "" {
			ldflags = append(ldflags, b.Ldflags)
		}
		if targetOS == "windows" && b.GUI {
			ldflags = append(ldflags, "-H", "windowsgui")
		}
	}
	if b.CommitVar != "" && cfg.Version != nil && cfg.Version.Commit != "" {
		ldflags = append(ldflags, fmt.Sprintf("-X %s=%s", b.CommitVar, cfg.Version.Commit))
	}
	if len(ldflags) > 0 {
		args = append(args, fmt.Sprintf("-ldflags=%s", strings.Join(ldflags, " ")))
	}

	outFile := o.OutFile
	if outFile == "" {
		outFile = GoOutputPath(cfg, b, targetOS)
	}
	args = append(args, "-o", outFile)

	srcPath := o.SourcePath
	if srcPath == "" {
		srcPath = b.Path
	}
	if srcPath == "" {
		srcPath = "."
	}
	args = append(args, srcPath)

	return args
}

// BuildBinary builds a single Go binary target according to its BinaryConfig.
func BuildBinary(ctx context.Context, cfg *config.Config, bCfg *config.BinaryConfig, targetOS string, targetArch string) (string, error) {
	targetOS = config.NormalizeOS(targetOS)
	targetArch = config.NormalizeArch(targetArch)

	outDir := filepath.Join(cfg.RootDir, cfg.Package.OutputDir)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create output directory %s: %w", outDir, err)
	}

	outFile := GoOutputPath(cfg, bCfg, targetOS)
	args := GoBuildArgs(cfg, bCfg, targetOS, GoBuildOptions{
		Trimpath: true,
		Ldflags:  true,
		OutFile:  outFile,
	})

	env := map[string]string{
		"GOOS":   targetOS,
		"GOARCH": targetArch,
	}

	if bCfg.CGO {
		env["CGO_ENABLED"] = "1"
	} else {
		env["CGO_ENABLED"] = "0"
	}

	if targetOS == "darwin" {
		if cfg.Darwin.MinOSVersion != "" {
			env["MACOSX_DEPLOYMENT_TARGET"] = cfg.Darwin.MinOSVersion
			if bCfg.CGO {
				env["CGO_CFLAGS"] = fmt.Sprintf("-mmacosx-version-min=%s", cfg.Darwin.MinOSVersion)
				env["CGO_LDFLAGS"] = fmt.Sprintf("-mmacosx-version-min=%s", cfg.Darwin.MinOSVersion)
			}
		}
	}

	// Remove stale syso resources before compiling so they are not linked in.
	// Clean both the repository root and the binary's source/package directory.
	if bCfg.CleanSyso {
		if err := cleanSysoFiles(cfg.RootDir, bCfg.Path); err != nil {
			return "", fmt.Errorf("failed to clean syso files for binary %s: %w", bCfg.Name, err)
		}
	}

	runner := exec.NewRunner(fmt.Sprintf("go:%s", bCfg.Name))
	fmt.Printf("==> Compiling binary [%s] for %s/%s -> %s\n", bCfg.Name, targetOS, targetArch, outFile)
	if err := runner.RunArgs(ctx, "go", args, cfg.RootDir, env); err != nil {
		return "", fmt.Errorf("compilation of binary %s failed: %w", bCfg.Name, err)
	}

	// Optional UPX compression (unless on macOS where UPX breaks code signing)
	if bCfg.UPX && targetOS != "darwin" {
		if _, err := os.Stat(outFile); err == nil {
			fmt.Printf("==> Running UPX compression on %s\n", outFile)
			if err := runner.RunArgs(ctx, "upx", []string{"-9", outFile}, cfg.RootDir, nil); err != nil {
				return "", fmt.Errorf("UPX compression of binary %s failed: %w", bCfg.Name, err)
			}
		}
	}

	return outFile, nil
}

func cleanSysoFiles(rootDir, binaryPath string) error {
	dirs := []string{rootDir}

	srcPath := binaryPath
	if srcPath == "" {
		srcPath = "."
	}
	if !filepath.IsAbs(srcPath) {
		srcPath = filepath.Join(rootDir, srcPath)
	}
	srcPath = filepath.Clean(srcPath)
	if info, err := os.Stat(srcPath); err == nil && !info.IsDir() {
		srcPath = filepath.Dir(srcPath)
	}
	dirs = append(dirs, srcPath)

	seen := make(map[string]bool)
	for _, dir := range dirs {
		dir = filepath.Clean(dir)
		if seen[dir] {
			continue
		}
		seen[dir] = true

		matches, err := filepath.Glob(filepath.Join(dir, "*.syso"))
		if err != nil {
			return fmt.Errorf("invalid syso glob for %s: %w", dir, err)
		}
		for _, match := range matches {
			if err := os.Remove(match); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("failed to remove %s: %w", match, err)
			}
		}
	}
	return nil
}
