package builder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/simtxng/cook/pkg/buildcache"
	"github.com/simtxng/cook/pkg/config"
	hookexec "github.com/simtxng/cook/pkg/exec"
	"github.com/simtxng/cook/pkg/timing"
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
	// Parallelism emits -p N when positive, bounding concurrent compile actions.
	Parallelism int
}

// GoTags resolves the build tags for a binary after applying exclusions and
// extra tags.
func GoTags(b *config.BinaryConfig, o GoBuildOptions) []string {
	tags := make([]string, 0, len(b.Tags)+len(o.ExtraTags))
	for _, tag := range b.Tags {
		if slices.Contains(o.ExcludeTags, tag) {
			continue
		}
		tags = append(tags, tag)
	}
	return append(tags, o.ExtraTags...)
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
	if o.Parallelism > 0 {
		args = append(args, "-p", strconv.Itoa(o.Parallelism))
	}
	if o.Trimpath {
		args = append(args, "-trimpath")
	}
	if !o.BuildVCS {
		args = append(args, "-buildvcs=false")
	}
	if o.GCFlags != "" {
		args = append(args, "-gcflags="+o.GCFlags)
	}

	if tags := GoTags(b, o); len(tags) > 0 {
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

// GoBuildCacheArgs returns a copy of args with the output path masked so
// fingerprints are independent of temporary output file names.
func GoBuildCacheArgs(args []string) []string {
	out := append([]string(nil), args...)
	for i := 0; i+1 < len(out); i++ {
		if out[i] == "-o" {
			out[i+1] = "<out>"
		}
	}
	return out
}

// GoBuildEnv returns the environment overrides used to compile b for a target.
func GoBuildEnv(cfg *config.Config, bCfg *config.BinaryConfig, targetOS, targetArch string) map[string]string {
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
	return env
}

// GoInputFiles resolves every source file that can influence a Go build,
// including dependency packages and embedded assets.
func GoInputFiles(ctx context.Context, cfg *config.Config, bCfg *config.BinaryConfig, targetOS, targetArch string, tags []string) ([]string, error) {
	srcPath := bCfg.Path
	if srcPath == "" {
		srcPath = "."
	}

	args := []string{"list", "-deps", "-json"}
	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}
	args = append(args, srcPath)

	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = cfg.RootDir
	cmd.Env = hookexec.BuildEnv(GoBuildEnv(cfg, bCfg, targetOS, targetArch))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	type moduleInfo struct {
		GoMod string
		GoSum string
	}
	type packageInfo struct {
		Dir          string
		GoFiles      []string
		CgoFiles     []string
		CFiles       []string
		CXXFiles     []string
		MFiles       []string
		HFiles       []string
		FFiles       []string
		SFiles       []string
		SwigFiles    []string
		SwigCXXFiles []string
		SysoFiles    []string
		EmbedFiles   []string
		Module       *moduleInfo
	}

	var files []string
	add := func(path string) {
		if path != "" {
			files = append(files, path)
		}
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p packageInfo
		if err := dec.Decode(&p); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode go list output: %w", err)
		}
		for _, group := range [][]string{
			p.GoFiles, p.CgoFiles, p.CFiles, p.CXXFiles, p.MFiles,
			p.HFiles, p.FFiles, p.SFiles, p.SwigFiles, p.SwigCXXFiles,
			p.SysoFiles, p.EmbedFiles,
		} {
			for _, name := range group {
				add(filepath.Join(p.Dir, name))
			}
		}
		if p.Module != nil {
			add(p.Module.GoMod)
			add(p.Module.GoSum)
		}
	}

	add(filepath.Join(cfg.RootDir, "go.mod"))
	add(filepath.Join(cfg.RootDir, "go.sum"))
	for _, path := range goWorkFiles(cfg.RootDir) {
		add(path)
	}
	return files, nil
}

// goWorkFiles returns the go.work and go.work.sum files that apply to root,
// walking up the directory tree like the Go toolchain does. GOWORK overrides
// the search when set.
func goWorkFiles(root string) []string {
	if work := os.Getenv("GOWORK"); work != "" && work != "off" {
		files := []string{work}
		if _, err := os.Stat(work + ".sum"); err == nil {
			files = append(files, work+".sum")
		}
		return files
	}

	for dir := root; ; dir = filepath.Dir(dir) {
		work := filepath.Join(dir, "go.work")
		if _, err := os.Stat(work); err == nil {
			files := []string{work}
			if _, err := os.Stat(work + ".sum"); err == nil {
				files = append(files, work+".sum")
			}
			return files
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
	}
}

// BuildBinary builds a single Go binary target according to its BinaryConfig.
func BuildBinary(ctx context.Context, cfg *config.Config, bCfg *config.BinaryConfig, targetOS string, targetArch string) (string, error) {
	return buildBinaryWithOptions(ctx, cfg, bCfg, targetOS, targetArch, BuildOptions{}, 0, nil)
}

func buildBinaryWithOptions(ctx context.Context, cfg *config.Config, bCfg *config.BinaryConfig, targetOS, targetArch string, opts BuildOptions, parallelism int, store *buildcache.Store) (string, error) {
	targetOS = config.NormalizeOS(targetOS)
	targetArch = config.NormalizeArch(targetArch)

	outDir := filepath.Join(cfg.RootDir, cfg.Package.OutputDir)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create output directory %s: %w", outDir, err)
	}

	outFile := GoOutputPath(cfg, bCfg, targetOS)
	args := GoBuildArgs(cfg, bCfg, targetOS, GoBuildOptions{
		Trimpath:    true,
		Ldflags:     true,
		OutFile:     outFile,
		Parallelism: parallelism,
	})
	env := GoBuildEnv(cfg, bCfg, targetOS, targetArch)

	cacheKey := fmt.Sprintf("go:%s:%s:%s", bCfg.Name, targetOS, targetArch)
	var fingerprint string
	if opts.SkipUnchanged && !opts.Force && store != nil {
		inputs, inputErr := GoInputFiles(ctx, cfg, bCfg, targetOS, targetArch, GoTags(bCfg, GoBuildOptions{}))
		if inputErr == nil {
			extras := []string{
				"go:" + runtime.Version(),
				fmt.Sprintf("upx=%t:%d", bCfg.UPX, bCfg.EffectiveUPXLevel()),
				fmt.Sprintf("clean_syso=%t", bCfg.CleanSyso),
			}
			extras = append(extras, GoBuildCacheArgs(args)...)
			keys := make([]string, 0, len(env))
			for k := range env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				extras = append(extras, k+"="+env[k])
			}
			if fp, fpErr := buildcache.Fingerprint(inputs, extras...); fpErr == nil {
				fingerprint = fp
				if store.Fresh(cacheKey, fp, outFile) {
					fmt.Printf("==> Skipping unchanged binary [%s] -> %s\n", bCfg.Name, outFile)
					return outFile, nil
				}
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

	runner := hookexec.NewRunner(fmt.Sprintf("go:%s", bCfg.Name))
	done := timing.Start("go build " + bCfg.Name)
	fmt.Printf("==> Compiling binary [%s] for %s/%s -> %s\n", bCfg.Name, targetOS, targetArch, outFile)
	err := runner.RunArgs(ctx, "go", args, cfg.RootDir, env)
	done()
	if err != nil {
		return "", fmt.Errorf("compilation of binary %s failed: %w", bCfg.Name, err)
	}

	// Optional UPX compression (unless on macOS where UPX breaks code signing)
	if bCfg.UPX && targetOS != "darwin" {
		if _, err := os.Stat(outFile); err == nil {
			if isUPXPacked(ctx, runner, outFile, cfg.RootDir) {
				fmt.Printf("==> %s is already UPX-compressed, skipping\n", outFile)
			} else {
				level := bCfg.EffectiveUPXLevel()
				fmt.Printf("==> Running UPX compression (-%d) on %s\n", level, outFile)
				doneUPX := timing.Start("upx " + bCfg.Name)
				err := runner.RunArgs(ctx, "upx", []string{fmt.Sprintf("-%d", level), outFile}, cfg.RootDir, nil)
				doneUPX()
				if err != nil {
					return "", fmt.Errorf("UPX compression of binary %s failed: %w", bCfg.Name, err)
				}
			}
		}
	}

	if fingerprint != "" && store != nil {
		_ = store.Mark(cacheKey, fingerprint)
	}
	return outFile, nil
}

// isUPXPacked reports whether path is already a UPX-compressed executable.
// `upx -l` only succeeds for packed files, so it doubles as a state probe.
func isUPXPacked(ctx context.Context, runner *hookexec.Runner, path, dir string) bool {
	probe := hookexec.NewRunner(runner.Prefix)
	probe.Stdout = io.Discard
	probe.Stderr = io.Discard
	return probe.RunArgs(ctx, "upx", []string{"-l", path}, dir, nil) == nil
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
