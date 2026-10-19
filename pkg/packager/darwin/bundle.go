package darwin

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"howett.net/plist"

	"github.com/simtxng/cook/pkg/config"
	"github.com/simtxng/cook/pkg/fsutil"
	"github.com/simtxng/cook/pkg/packager"
)

// AppBundleOptions configures the creation of a macOS .app bundle.
type AppBundleOptions struct {
	DevMode bool
}

// AssembleAppBundle creates the complete macOS .app bundle directory structure and copies all assets.
func AssembleAppBundle(cfg *config.Config, binaries map[string]string, opts AppBundleOptions) (string, error) {
	appName := cfg.Package.ProductName
	if appName == "" {
		appName = cfg.Package.Name
	}
	if opts.DevMode {
		appName += ".dev"
	}
	bundleName := appName + ".app"

	outDir := filepath.Join(cfg.RootDir, cfg.Package.OutputDir)
	appBundlePath := filepath.Join(outDir, bundleName)

	macosDir := filepath.Join(appBundlePath, "Contents", "MacOS")
	resourcesDir := filepath.Join(appBundlePath, "Contents", "Resources")

	if err := os.MkdirAll(macosDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create Contents/MacOS: %w", err)
	}
	if err := os.MkdirAll(resourcesDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create Contents/Resources: %w", err)
	}

	// 1. Copy GUI binary
	guiBinPath := findDarwinGUIBinary(cfg, binaries)
	if guiBinPath == "" {
		return "", fmt.Errorf("no GUI binary found for macOS .app bundle")
	}

	execName := cfg.Package.Name
	if execName == "" {
		execName = filepath.Base(guiBinPath)
	}
	destGUI := filepath.Join(macosDir, execName)
	if err := fsutil.CopyExecutable(guiBinPath, destGUI); err != nil {
		return "", fmt.Errorf("failed to copy GUI binary: %w", err)
	}

	// 2. Copy companion CLI binary (e.g. simtx-cli)
	cliBinPath := findDarwinCLIBinary(cfg, binaries, guiBinPath)
	if cliBinPath != "" {
		cliDestName := filepath.Base(cliBinPath)
		destCLI := filepath.Join(macosDir, cliDestName)
		if err := fsutil.CopyExecutable(cliBinPath, destCLI); err != nil {
			return "", fmt.Errorf("failed to copy companion CLI binary: %w", err)
		}
		fmt.Printf("==> Bundled companion CLI -> %s\n", destCLI)
	}

	// 3. Copy icons (icons.icns)
	iconPath, err := resolveDarwinIconPath(cfg)
	if err != nil {
		return "", err
	}
	if iconPath != "" {
		destIcon := filepath.Join(resourcesDir, "icons.icns")
		iconInfo, err := os.Stat(iconPath)
		if err != nil {
			return "", fmt.Errorf("failed to stat icon file: %w", err)
		}
		if err := fsutil.Copy(iconPath, destIcon, iconInfo.Mode().Perm()); err != nil {
			return "", fmt.Errorf("failed to copy icon file: %w", err)
		}
	}

	// 4. Copy Assets.car (if present)
	assetsCarPath, err := resolveAssetsCarPath(cfg)
	if err != nil {
		return "", err
	}
	if assetsCarPath != "" {
		destAssets := filepath.Join(resourcesDir, "Assets.car")
		assetsInfo, err := os.Stat(assetsCarPath)
		if err != nil {
			return "", fmt.Errorf("failed to stat Assets.car: %w", err)
		}
		if err := fsutil.Copy(assetsCarPath, destAssets, assetsInfo.Mode().Perm()); err != nil {
			return "", fmt.Errorf("failed to copy Assets.car: %w", err)
		}
	}

	// 5. Template and write Info.plist
	infoPlistDest := filepath.Join(appBundlePath, "Contents", "Info.plist")
	if err := writeInfoPlist(cfg, infoPlistDest, execName, opts.DevMode); err != nil {
		return "", fmt.Errorf("failed to write Info.plist: %w", err)
	}

	fmt.Printf("==> Assembled macOS .app bundle -> %s\n", appBundlePath)
	return appBundlePath, nil
}

func writeInfoPlist(cfg *config.Config, destPath, execName string, devMode bool) error {
	var m map[string]interface{}

	templatePath := cfg.Darwin.InfoPlist
	userConfigured := templatePath != ""
	if devMode {
		devCandidate := filepath.Join(cfg.RootDir, "build", "darwin", "Info.dev.plist")
		if fileExists(devCandidate) {
			templatePath = devCandidate
			userConfigured = false
		}
	}
	if templatePath == "" {
		candidate := filepath.Join(cfg.RootDir, "build", "darwin", "Info.plist")
		if fileExists(candidate) {
			templatePath = candidate
		}
	} else if !filepath.IsAbs(templatePath) {
		templatePath = filepath.Join(cfg.RootDir, templatePath)
	}

	if templatePath != "" && fileExists(templatePath) {
		data, err := os.ReadFile(templatePath)
		if err != nil {
			if userConfigured {
				return fmt.Errorf("failed to read configured Info.plist template %s: %w", templatePath, err)
			}
		} else {
			decoder := plist.NewDecoder(bytes.NewReader(data))
			if err := decoder.Decode(&m); err != nil && userConfigured {
				return fmt.Errorf("failed to decode configured Info.plist template %s: %w", templatePath, err)
			}
		}
	} else if userConfigured {
		return fmt.Errorf("configured Info.plist template not found: %s", templatePath)
	}

	if m == nil {
		m = make(map[string]interface{})
	}

	// Update / ensure required fields
	m["CFBundlePackageType"] = "APPL"
	m["CFBundleExecutable"] = execName

	bundleID := cfg.Darwin.BundleID
	if bundleID == "" {
		bundleID = cfg.Package.Identifier
	}
	if bundleID != "" {
		m["CFBundleIdentifier"] = bundleID
	}

	prodName := cfg.Package.ProductName
	if prodName == "" {
		prodName = cfg.Package.Name
	}
	m["CFBundleName"] = prodName
	m["CFBundleDisplayName"] = prodName

	rv := cfg.ResolvedVersion()
	if rv.Raw != "" {
		m["CFBundleShortVersionString"] = rv.Raw
	}
	// CFBundleVersion must be a numeric version; Apple rejects prerelease
	// suffixes like "0.1.0-alpha-12".
	bundleVersion := rv.Windows
	if bundleVersion == "" {
		bundleVersion = config.FormatWindowsVersion(rv.Raw)
	}
	if bundleVersion != "" {
		m["CFBundleVersion"] = bundleVersion
	}

	m["LSMinimumSystemVersion"] = cfg.Darwin.MinOSVersion
	m["NSHighResolutionCapable"] = true

	if cfg.Package.Copyright != "" {
		m["NSHumanReadableCopyright"] = cfg.Package.Copyright
	}
	if cfg.Package.Description != "" {
		m["CFBundleGetInfoString"] = cfg.Package.Description
	}
	if _, ok := m["CFBundleIconFile"]; !ok {
		m["CFBundleIconFile"] = "icons"
	}

	// Microphone usage description
	if _, ok := m["NSMicrophoneUsageDescription"]; !ok {
		desc := cfg.Darwin.MicrophoneUsageDescription
		if desc == "" {
			desc = DefaultMicrophoneUsageDescription
		}
		m["NSMicrophoneUsageDescription"] = desc
	}

	data, err := plist.MarshalIndent(m, plist.XMLFormat, "\t")
	if err != nil {
		return err
	}

	return os.WriteFile(destPath, data, 0644)
}

func resolveDarwinIconPath(cfg *config.Config) (string, error) {
	if cfg.Darwin.Icons != "" {
		p := config.ResolvePath(cfg.RootDir, cfg.Darwin.Icons)
		if !fileExists(p) {
			return "", fmt.Errorf("configured darwin.icons file not found: %s", p)
		}
		return p, nil
	}
	candidate := filepath.Join(cfg.RootDir, "build", "darwin", "icons.icns")
	if fileExists(candidate) {
		return candidate, nil
	}
	if cfg.Package.Icon != "" {
		p := config.ResolvePath(cfg.RootDir, cfg.Package.Icon)
		if fileExists(p) && strings.HasSuffix(strings.ToLower(p), ".icns") {
			return p, nil
		}
	}
	return "", nil
}

func resolveAssetsCarPath(cfg *config.Config) (string, error) {
	if cfg.Darwin.AssetsCar != "" {
		p := config.ResolvePath(cfg.RootDir, cfg.Darwin.AssetsCar)
		if !fileExists(p) {
			return "", fmt.Errorf("configured darwin.assets_car file not found: %s", p)
		}
		return p, nil
	}
	candidate := filepath.Join(cfg.RootDir, "build", "darwin", "Assets.car")
	if fileExists(candidate) {
		return candidate, nil
	}
	return "", nil
}

// sortedBinaryNames returns the keys of binaries in a stable order so fallback
// selection never depends on Go's randomized map iteration.
func sortedBinaryNames(binaries map[string]string) []string {
	names := make([]string, 0, len(binaries))
	for name := range binaries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func findDarwinGUIBinary(cfg *config.Config, binaries map[string]string) string {
	// Look for an explicitly configured GUI binary, in configuration order.
	for _, b := range cfg.Binaries {
		if b.GUI {
			if path := packager.LookupBinary(binaries, "", b.Name, false); path != "" {
				return path
			}
		}
	}

	// Look for package name.
	if path := packager.LookupBinary(binaries, "", cfg.Package.Name, false); path != "" {
		return path
	}

	// Fall back to a configured binary that does not look like a CLI, in
	// configuration order.
	for _, b := range cfg.Binaries {
		if b.GUI || strings.Contains(strings.ToLower(b.Name), "cli") {
			continue
		}
		if path := packager.LookupBinary(binaries, "", b.Name, false); path != "" {
			return path
		}
	}

	// Any remaining configured binary, still in deterministic order.
	for _, b := range cfg.Binaries {
		if path := packager.LookupBinary(binaries, "", b.Name, false); path != "" {
			return path
		}
	}

	// Binaries that are not declared in the config: use sorted names.
	for _, name := range sortedBinaryNames(binaries) {
		if path := packager.LookupBinary(binaries, "", name, false); path != "" {
			return path
		}
	}

	return ""
}

func findDarwinCLIBinary(cfg *config.Config, binaries map[string]string, guiPath string) string {
	outDir := filepath.Join(cfg.RootDir, cfg.Package.OutputDir)
	if cfg.Darwin.BundledCLI != "" {
		if path := packager.LookupBinary(binaries, "", cfg.Darwin.BundledCLI, false); path != "" && path != guiPath {
			return path
		}
		if path := packager.LookupBinary(nil, outDir, cfg.Darwin.BundledCLI, false); path != "" && path != guiPath {
			return path
		}
		return ""
	}

	// Select only configured non-GUI binaries, in cfg.Binaries order, and never
	// re-bundle the GUI binary as the companion CLI.
	for _, b := range cfg.Binaries {
		if b.GUI {
			continue
		}
		if path := packager.LookupBinary(binaries, outDir, b.Name, false); path != "" && path != guiPath {
			return path
		}
	}

	return ""
}

func resolvePath(baseDir, path string) string {
	return config.ResolvePath(baseDir, path)
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}
