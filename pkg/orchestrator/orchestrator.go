package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/simtxng/cook/pkg/builder"
	"github.com/simtxng/cook/pkg/config"
	"github.com/simtxng/cook/pkg/exec"
	"github.com/simtxng/cook/pkg/generator"
	"github.com/simtxng/cook/pkg/packager"
	"github.com/simtxng/cook/pkg/timing"
)

// Orchestrator coordinates the end-to-end build, hook, and packaging pipeline.
type Orchestrator struct {
	Packagers map[string]packager.Packager

	templatesDone map[*config.Config]bool
}

func NewOrchestrator() *Orchestrator {
	return &Orchestrator{
		Packagers:     make(map[string]packager.Packager),
		templatesDone: make(map[*config.Config]bool),
	}
}

// Register registers a platform packager.
func (o *Orchestrator) Register(p packager.Packager) {
	o.Packagers[p.Name()] = p
}

// Build compiles the frontend (if enabled) and specified binaries.
func (o *Orchestrator) Build(ctx context.Context, cfg *config.Config, binaryFilter []string, targetOS, targetArch string) (map[string]string, error) {
	return o.BuildWithOptions(ctx, cfg, binaryFilter, targetOS, targetArch, builder.BuildOptions{})
}

// BuildWithOptions compiles the frontend and specified binaries with options.
func (o *Orchestrator) BuildWithOptions(ctx context.Context, cfg *config.Config, binaryFilter []string, targetOS, targetArch string, opts builder.BuildOptions) (map[string]string, error) {
	if err := o.generateTemplatesOnce(ctx, cfg); err != nil {
		return nil, err
	}
	return builder.NewBuilder(cfg).BuildWithOptions(ctx, binaryFilter, targetOS, targetArch, opts)
}

// generateTemplatesOnce renders templates at most once per config for this
// orchestrator instance.
func (o *Orchestrator) generateTemplatesOnce(ctx context.Context, cfg *config.Config) error {
	if o.templatesDone == nil {
		o.templatesDone = make(map[*config.Config]bool)
	}
	if o.templatesDone[cfg] {
		return nil
	}
	if err := generateTemplates(ctx, cfg); err != nil {
		return err
	}
	o.templatesDone[cfg] = true
	return nil
}

// generateTemplates renders config templates, failing the run on any error.
func generateTemplates(ctx context.Context, cfg *config.Config) error {
	done := timing.Start("templates")
	defer done()
	gen := generator.NewGenerator()
	generated, err := gen.Generate(ctx, cfg, "", false)
	if err != nil {
		return fmt.Errorf("template generation failed: %w", err)
	}
	if len(generated) > 0 {
		fmt.Printf("==> Rendered %d file(s) from templates\n", len(generated))
	}
	return nil
}

// Package coordinates compilation, hook execution, and artifact packaging for the target OS.
func (o *Orchestrator) Package(ctx context.Context, cfg *config.Config, targetOS, targetArch string, targetFilters []string, skipBuild bool) ([]string, error) {
	return o.PackageWithOptions(ctx, cfg, targetOS, targetArch, targetFilters, skipBuild, builder.BuildOptions{})
}

// PackageWithOptions coordinates compilation, hooks, and packaging with build options.
func (o *Orchestrator) PackageWithOptions(ctx context.Context, cfg *config.Config, targetOS, targetArch string, targetFilters []string, skipBuild bool, buildOpts builder.BuildOptions) ([]string, error) {
	targetOS = config.NormalizeOS(targetOS)
	targetArch = config.NormalizeArch(targetArch)

	// Render templates exactly once per operation.
	if err := o.generateTemplatesOnce(ctx, cfg); err != nil {
		return nil, err
	}

	// Resolve the target OS from the filters before building so cross-OS
	// targets (e.g. `cook package deb` on macOS) compile the right binaries.
	if len(targetFilters) > 0 {
		osSet := map[string]bool{}
		for _, t := range targetFilters {
			if os, _ := ParseTarget(t); os != "" {
				osSet[os] = true
			}
		}
		if len(osSet) > 1 {
			return nil, fmt.Errorf("cannot package multiple operating systems in one run: %v", sortedKeys(osSet))
		}
		for os := range osSet {
			targetOS = os
		}
	}

	// Determine the targets to package.
	var targetsToRun []string
	if len(targetFilters) > 0 {
		targetsToRun = targetFilters
	} else {
		switch targetOS {
		case "linux":
			targetsToRun = cfg.Linux.Targets
		case "windows":
			targetsToRun = cfg.Windows.Targets
		case "darwin":
			targetsToRun = cfg.Darwin.Targets
		}
	}

	if len(targetsToRun) == 0 {
		return nil, fmt.Errorf("no packaging targets specified for OS %s in config or command line", targetOS)
	}

	needsBinaries := false
	for _, rawTarget := range targetsToRun {
		_, target := ParseTarget(rawTarget)
		if targetNeedsBinaries(target) {
			needsBinaries = true
			break
		}
	}

	hooks := exec.NewHookManager(cfg)

	// Phase 1/3 (build): compile binaries, or discover existing ones. Targets
	// such as syso only need configuration metadata, so skip compilation and
	// frontend work while still honoring build hooks that may prepare resources.
	var binaries map[string]string
	var err error
	if needsBinaries {
		if !skipBuild {
			fmt.Printf("==> [1/3 build] Compiling application binaries for %s/%s...\n", targetOS, targetArch)
			buildDone := timing.Start("build phase")
			binaries, err = builder.NewBuilder(cfg).BuildWithOptions(ctx, nil, targetOS, targetArch, buildOpts)
			buildDone()
			if err != nil {
				return nil, fmt.Errorf("build phase failed: %w", err)
			}
		} else {
			binaries, err = discoverBinaries(cfg, targetOS)
			if err != nil {
				return nil, err
			}
		}
	} else {
		binaries = map[string]string{}
		if !skipBuild {
			if err := hooks.RunPreBuild(ctx); err != nil {
				return nil, err
			}
			if err := hooks.RunPostBuild(ctx); err != nil {
				return nil, err
			}
		}
	}

	var allArtifacts []string

	// Package each target: pre-package hooks, packaging, then post-package hooks.
	for _, rawTarget := range targetsToRun {
		parsedOS, target := ParseTarget(rawTarget)
		osToUse := targetOS
		if parsedOS != "" {
			osToUse = parsedOS
		}

		pkgEngine, ok := o.Packagers[osToUse]
		if !ok {
			return nil, fmt.Errorf("no packager registered for OS %q (available: %v)", osToUse, o.registeredPackagers())
		}

		// Phase 2/3 (hooks): run the target's pre-package hooks.
		fmt.Printf("\n==> [2/3 hooks] Running pre-package hooks for [%s]...\n", target)
		if err := hooks.RunPrePackage(ctx, target); err != nil {
			return nil, err
		}

		// Phase 3/3 (package): build the artifact(s) for this target.
		fmt.Printf("\n==> [3/3 package] Packaging target [%s] for %s/%s...\n", target, osToUse, targetArch)

		packageDone := timing.Start("package " + target)
		artifacts, err := pkgEngine.Package(ctx, cfg, target, targetArch, binaries)
		packageDone()
		if err != nil {
			return nil, fmt.Errorf("packaging target %s failed: %w", target, err)
		}
		allArtifacts = append(allArtifacts, artifacts...)

		fmt.Printf("==> [2/3 hooks] Running post-package hooks for [%s]...\n", target)
		if err := hooks.RunPostPackage(ctx, target); err != nil {
			return nil, err
		}
	}

	fmt.Printf("\n==> Packaging complete! Generated %d artifact(s):\n", len(allArtifacts))
	for _, a := range allArtifacts {
		info, err := os.Stat(a)
		if err == nil {
			sizeMB := float64(info.Size()) / (1024 * 1024)
			fmt.Printf("    * %s (%.2f MB)\n", a, sizeMB)
		} else {
			fmt.Printf("    * %s\n", a)
		}
	}

	return allArtifacts, nil
}

// targetNeedsBinaries reports whether a packaging target requires compiled
// binaries. Resource-only targets such as syso do not.
func targetNeedsBinaries(target string) bool {
	switch target {
	case "syso":
		return false
	default:
		return true
	}
}

// discoverBinaries locates previously-built binaries for --skip-build, failing
// when any configured binary is missing rather than silently packaging an
// incomplete set.
func discoverBinaries(cfg *config.Config, targetOS string) (map[string]string, error) {
	binaries := make(map[string]string)
	outDir := filepath.Join(cfg.RootDir, cfg.Package.OutputDir)
	var missing []string
	for _, bCfg := range cfg.Binaries {
		path, ok := discoverBinary(outDir, bCfg, targetOS)
		if ok {
			binaries[bCfg.Name] = path
			continue
		}
		exeName := bCfg.Name
		if targetOS == "windows" && !strings.HasSuffix(exeName, ".exe") {
			exeName += ".exe"
		}
		missing = append(missing, exeName)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("--skip-build: missing binaries in %s: %s", outDir, strings.Join(missing, ", "))
	}
	return binaries, nil
}

// discoverBinary resolves one configured binary in outDir. Windows artifacts
// prefer the exact <name>.exe; an extensionless <name> is only accepted when
// it cannot be confused with a different file.
func discoverBinary(outDir string, bCfg config.BinaryConfig, targetOS string) (string, bool) {
	name := bCfg.Name
	if targetOS != "windows" {
		return regularFile(filepath.Join(outDir, name))
	}

	// A name that already carries the Windows suffix only ever matches itself.
	if strings.HasSuffix(name, ".exe") {
		return regularFile(filepath.Join(outDir, name))
	}

	exePath := filepath.Join(outDir, name+".exe")
	if path, ok := regularFile(exePath); ok {
		return path, true
	}

	plainPath := filepath.Join(outDir, name)
	plain, ok := regularFile(plainPath)
	if !ok {
		return "", false
	}
	if !bCfg.GUI {
		return plain, true
	}

	// GUI binaries are expected to have an .exe. Accept the extensionless file
	// only when it is the sole match; if an .exe entry exists but is not a
	// usable file, the candidates are ambiguous and guessing is unsafe.
	if _, err := os.Lstat(exePath); os.IsNotExist(err) {
		return plain, true
	}
	return "", false
}

func regularFile(path string) (string, bool) {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return "", false
	}
	return path, true
}

// sortedKeys returns the keys of m in deterministic order.
func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// packagingTargetOS maps canonical packaging target names to their OS.
var packagingTargetOS = map[string]string{
	"appimage":  "linux",
	"deb":       "linux",
	"rpm":       "linux",
	"archlinux": "linux",
	"void":      "linux",
	"xbps":      "linux",
	"nsis":      "windows",
	"msix":      "windows",
	"syso":      "windows",
	"app":       "darwin",
	"dmg":       "darwin",
}

// ParseTarget normalizes target strings like "linux.appimage", "linux:appimage", "appimage", "windows.nsis", "nsis".
// It returns the detected OS (if any) and the normalized target name.
func ParseTarget(raw string) (string, string) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "", ""
	}

	// Check for OS prefixes: linux.appimage, linux:appimage, linux/appimage
	for _, sep := range []string{".", ":", "/"} {
		prefix, subTarget, ok := strings.Cut(raw, sep)
		if !ok {
			continue
		}
		switch prefix {
		case "linux", "windows", "darwin":
			return prefix, normalizeTargetName(subTarget)
		}
	}

	target := normalizeTargetName(raw)
	if os, ok := packagingTargetOS[target]; ok {
		return os, target
	}
	return "", target
}

// normalizeTargetName resolves aliases after the input has been lowercased.
func normalizeTargetName(t string) string {
	if t == "aur" {
		return "archlinux"
	}
	if t == "xbps" {
		return "void"
	}
	return t
}

// IsKnownPackagingTarget reports whether the string represents a distribution packaging target.
func IsKnownPackagingTarget(raw string) bool {
	_, target := ParseTarget(raw)
	_, ok := packagingTargetOS[target]
	return ok
}

func (o *Orchestrator) registeredPackagers() []string {
	names := make([]string, 0, len(o.Packagers))
	for k := range o.Packagers {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
