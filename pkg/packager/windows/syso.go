package windows

import (
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"

	"github.com/simtxng/cook/pkg/config"
)

// InfoJSON represents the structure of Windows info.json metadata file.
type InfoJSON struct {
	Fixed struct {
		FileVersion    string `json:"file_version"`
		ProductVersion string `json:"product_version"`
	} `json:"fixed"`
	Info map[string]map[string]string `json:"info"`
}

// ParseVersion4 parses a version string into a 4-part uint16 array for Windows PE / NSIS.
func ParseVersion4(v string) [4]uint16 {
	return config.VersionParts4(v)
}

// resolvedWindowsVersion returns the resolved version as a strict 4-part
// numeric string (X.X.X.X), falling back to Package.Version when the resolved
// Windows version is empty.
func resolvedWindowsVersion(cfg *config.Config) string {
	ver := cfg.ResolvedVersion()
	numVer := ver.Windows
	if numVer == "" {
		raw := ver.Raw
		if raw == "" {
			raw = cfg.Package.Version
		}
		numVer = config.FormatWindowsVersion(raw)
	}
	p := config.VersionParts4(numVer)
	return fmt.Sprintf("%d.%d.%d.%d", p[0], p[1], p[2], p[3])
}

// ParseArch converts a string architecture name to winres.Arch.
func ParseArch(arch string) winres.Arch {
	switch strings.ToLower(arch) {
	case "amd64", "x64", "x86_64":
		return winres.ArchAMD64
	case "arm64", "aarch64":
		return winres.ArchARM64
	case "386", "x86", "i386":
		return winres.ArchI386
	case "arm":
		return winres.ArchARM
	default:
		return winres.ArchAMD64
	}
}

// SysoOptions defines options for compiling a binary-specific syso object.
type SysoOptions struct {
	OriginalFilename string
	InternalName     string
	IsGUI            bool
	DestPath         string
}

// CompileSyso compiles Windows resources (icon, manifest, version info) into a .syso COFF object.
func CompileSyso(cfg *config.Config, arch string) (string, error) {
	origName := cfg.Package.Name + ".exe"
	if cfg.Package.ProductName != "" {
		origName = cfg.Package.ProductName + ".exe"
	}
	return CompileSysoForBinary(cfg, nil, arch, SysoOptions{
		OriginalFilename: origName,
		InternalName:     strings.TrimSuffix(origName, ".exe"),
		IsGUI:            true,
	})
}

// CompileSysoForBinary compiles Windows resources (icon, manifest, version info)
// into a .syso COFF object customized for a specific binary.
func CompileSysoForBinary(cfg *config.Config, bCfg *config.BinaryConfig, arch string, opts SysoOptions) (string, error) {
	if arch == "" {
		arch = "amd64"
	}
	winresArch := ParseArch(arch)

	// One resolved version drives the manifest identity and the file version.
	resolved := resolvedWindowsVersion(cfg)

	rs := &winres.ResourceSet{}

	// 1. Icon compilation: apply icon if GUI or if icon is configured
	isGUI := opts.IsGUI
	if bCfg != nil {
		isGUI = bCfg.GUI
	}
	iconPath, err := resolveIconPath(cfg)
	if err != nil {
		return "", err
	}
	if iconPath != "" && (isGUI || opts.IsGUI || cfg.Windows.Syso.Icon != "" || cfg.Package.Icon != "") {
		if err := applyIcon(rs, iconPath); err != nil {
			return "", fmt.Errorf("failed to load icon %s: %w", iconPath, err)
		}
	}

	// 2. Manifest compilation (permonitorv2, common controls v6, asInvoker)
	manifestPath, err := resolveManifestPath(cfg)
	if err != nil {
		return "", err
	}
	if manifestPath != "" {
		manifestData, err := os.ReadFile(manifestPath)
		if err != nil {
			return "", fmt.Errorf("failed to read manifest %s: %w", manifestPath, err)
		}
		// Windows strictly requires a 4-part numeric version (e.g. "0.1.0.16")
		// for <assemblyIdentity version="..."> in application manifests.
		// Prerelease strings like "0.1.0-alpha-12" trigger Side-by-Side (SxS) activation errors.
		manifestData = sanitizeManifestVersion(manifestData, resolved)
		if err := rs.Set(winres.RT_MANIFEST, winres.ID(1), winres.LCIDDefault, manifestData); err != nil {
			return "", fmt.Errorf("failed to embed manifest from %s: %w", manifestPath, err)
		}
	} else {
		// Generate standard manifest with permonitorv2, common controls v6, asInvoker
		v4 := config.VersionParts4(resolved)
		ident := winres.AssemblyIdentity{
			Version: v4,
		}
		if bCfg != nil && bCfg.Name != "" {
			ident.Name = bCfg.Name
		} else if cfg.Package.Identifier != "" {
			ident.Name = cfg.Package.Identifier
		} else if cfg.Package.Name != "" {
			ident.Name = cfg.Package.Name
		}

		appManifest := winres.AppManifest{
			Identity:       ident,
			Description:    cfg.Package.Description,
			ExecutionLevel: winres.AsInvoker,
		}
		if isGUI {
			appManifest.DPIAwareness = winres.DPIPerMonitorV2
			appManifest.UseCommonControlsV6 = true
		}
		rs.SetManifest(appManifest)
	}

	// 3. Resolve OriginalFilename and InternalName to match the executable
	origFilename := opts.OriginalFilename
	if origFilename == "" && bCfg != nil {
		origFilename = bCfg.Name
		if !strings.HasSuffix(strings.ToLower(origFilename), ".exe") {
			origFilename += ".exe"
		}
	}
	if origFilename == "" {
		baseName := cfg.Package.ProductName
		if baseName == "" {
			baseName = cfg.Package.Name
		}
		origFilename = baseName + ".exe"
	}
	internalName := opts.InternalName
	if internalName == "" {
		internalName = strings.TrimSuffix(origFilename, ".exe")
	}

	// 4. Version Info (info.json / config metadata)
	infoJSONPath, err := resolveInfoJSONPath(cfg)
	if err != nil {
		return "", err
	}
	vi, err := buildVersionInfo(cfg, resolved, infoJSONPath, origFilename, internalName)
	if err != nil {
		return "", err
	}
	rs.SetVersionInfo(vi)

	// 5. Output path determination
	outFile := opts.DestPath
	if outFile == "" {
		outFile = cfg.Windows.Syso.Out
		if outFile == "" {
			outFile = filepath.Join(cfg.RootDir, fmt.Sprintf("wails_windows_%s.syso", arch))
		} else if !filepath.IsAbs(outFile) {
			outFile = filepath.Join(cfg.RootDir, outFile)
		}
	}

	if err := os.MkdirAll(filepath.Dir(outFile), 0755); err != nil {
		return "", fmt.Errorf("failed to create directory for syso %s: %w", outFile, err)
	}

	f, err := os.Create(outFile)
	if err != nil {
		return "", fmt.Errorf("failed to create syso output file %s: %w", outFile, err)
	}
	defer f.Close()

	if err := rs.WriteObject(f, winresArch); err != nil {
		return "", fmt.Errorf("failed to write syso COFF object %s: %w", outFile, err)
	}

	return outFile, nil
}

func resolveConfiguredPath(cfg *config.Config, configured, defaultPath, label string) (string, error) {
	if configured != "" {
		p := config.ResolvePath(cfg.RootDir, configured)
		if !fileExists(p) {
			return "", fmt.Errorf("%s is set to %q but no file exists at %s", label, configured, p)
		}
		return p, nil
	}
	if defaultPath != "" && fileExists(defaultPath) {
		return defaultPath, nil
	}
	return "", nil
}

func resolveIconPath(cfg *config.Config) (string, error) {
	if p, err := resolveConfiguredPath(cfg, cfg.Windows.Syso.Icon, "", "windows.syso.icon"); err != nil || p != "" {
		return p, err
	}
	if p, err := resolveConfiguredPath(cfg, cfg.Package.Icon, "", "package.icon"); err != nil || p != "" {
		return p, err
	}
	return resolveConfiguredPath(cfg, "", filepath.Join(cfg.RootDir, "build", "windows", "icon.ico"), "")
}

func resolveManifestPath(cfg *config.Config) (string, error) {
	return resolveConfiguredPath(cfg, cfg.Windows.Syso.Manifest, filepath.Join(cfg.RootDir, "build", "windows", "wails.exe.manifest"), "windows.syso.manifest")
}

func resolveInfoJSONPath(cfg *config.Config) (string, error) {
	return resolveConfiguredPath(cfg, cfg.Windows.Syso.InfoJSON, filepath.Join(cfg.RootDir, "build", "windows", "info.json"), "windows.syso.info_json")
}

func applyIcon(rs *winres.ResourceSet, iconPath string) error {
	f, err := os.Open(iconPath)
	if err != nil {
		return err
	}
	defer f.Close()

	ext := strings.ToLower(filepath.Ext(iconPath))
	if ext == ".ico" {
		ico, err := winres.LoadICO(f)
		if err != nil {
			return err
		}
		return rs.SetIcon(winres.ID(1), ico)
	}

	// Try loading as standard image (e.g. PNG)
	img, _, err := image.Decode(f)
	if err != nil {
		return fmt.Errorf("unsupported icon format for %s: %w", iconPath, err)
	}
	ico, err := winres.NewIconFromResizedImage(img, nil)
	if err != nil {
		return err
	}
	return rs.SetIcon(winres.ID(1), ico)
}

func buildVersionInfo(cfg *config.Config, resolved, infoJSONPath, originalFilename, internalName string) (version.Info, error) {
	vi := version.Info{}

	v4 := config.VersionParts4(resolved)
	vi.FileVersion = v4
	vi.ProductVersion = v4

	set := func(langID uint16, key, value string) error {
		if err := vi.Set(langID, key, value); err != nil {
			return fmt.Errorf("failed to set version resource %q: %w", key, err)
		}
		return nil
	}

	if infoJSONPath != "" {
		data, err := os.ReadFile(infoJSONPath)
		if err != nil {
			return version.Info{}, fmt.Errorf("failed to read info.json %s: %w", infoJSONPath, err)
		}
		var ij InfoJSON
		if err := json.Unmarshal(data, &ij); err != nil {
			return version.Info{}, fmt.Errorf("failed to parse info.json %s: %w", infoJSONPath, err)
		}
		if ij.Fixed.FileVersion != "" {
			vi.FileVersion = ParseVersion4(ij.Fixed.FileVersion)
		}
		if ij.Fixed.ProductVersion != "" {
			vi.ProductVersion = ParseVersion4(ij.Fixed.ProductVersion)
		}
		for langHex, table := range ij.Info {
			langID := uint16(version.LangNeutral)
			if u, err := strconv.ParseUint(langHex, 16, 16); err == nil {
				langID = uint16(u)
			}
			for k, v := range table {
				if err := vi.Set(langID, k, v); err != nil {
					return version.Info{}, fmt.Errorf("invalid info.json entry %q (language %s): %w", k, langHex, err)
				}
			}
			// Ensure OriginalFilename and InternalName are always populated to match the binary
			if originalFilename != "" {
				if err := vi.Set(langID, version.OriginalFilename, originalFilename); err != nil {
					return version.Info{}, err
				}
			}
			if internalName != "" {
				if err := vi.Set(langID, version.InternalName, internalName); err != nil {
					return version.Info{}, err
				}
			}
		}
		if len(ij.Info) == 0 {
			if originalFilename != "" {
				_ = vi.Set(version.LangNeutral, version.OriginalFilename, originalFilename)
			}
			if internalName != "" {
				_ = vi.Set(version.LangNeutral, version.InternalName, internalName)
			}
		}
		return vi, nil
	}

	// Fallback to config metadata
	prodName := cfg.Package.ProductName
	if prodName == "" {
		prodName = cfg.Package.Name
	}
	desc := cfg.Package.Description
	if desc == "" {
		desc = prodName
	}
	company := cfg.Package.Company
	copyright := cfg.Package.Copyright

	if err := set(version.LangNeutral, version.ProductName, prodName); err != nil {
		return version.Info{}, err
	}
	if err := set(version.LangNeutral, version.FileDescription, desc); err != nil {
		return version.Info{}, err
	}
	if company != "" {
		if err := set(version.LangNeutral, version.CompanyName, company); err != nil {
			return version.Info{}, err
		}
	}
	if copyright != "" {
		if err := set(version.LangNeutral, version.LegalCopyright, copyright); err != nil {
			return version.Info{}, err
		}
	}
	if err := set(version.LangNeutral, version.ProductVersion, resolved); err != nil {
		return version.Info{}, err
	}
	if err := set(version.LangNeutral, version.FileVersion, resolved); err != nil {
		return version.Info{}, err
	}
	if originalFilename != "" {
		if err := set(version.LangNeutral, version.OriginalFilename, originalFilename); err != nil {
			return version.Info{}, err
		}
	}
	if internalName != "" {
		if err := set(version.LangNeutral, version.InternalName, internalName); err != nil {
			return version.Info{}, err
		}
	}

	return vi, nil
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

var (
	assemblyIdentTagRe = regexp.MustCompile(`(?s)<assemblyIdentity\b([^>]*)>`)
	versionAttrRe      = regexp.MustCompile(`\bversion\s*=\s*["']([^"']*)["']`)
	valid4PartVerRe    = regexp.MustCompile(`^\d+(?:\.\d+){3}$`)
)

// sanitizeManifestVersion inspects manifest XML data and ensures that the
// application's <assemblyIdentity> has a valid 4-part numeric Windows version
// (e.g. "0.1.0.16"). Windows strictly requires 4 dotted numbers for assembly
// identities; prerelease strings like "0.1.0-alpha-12" fail Side-by-Side (SxS)
// loading with error 0xc0150002.
func sanitizeManifestVersion(data []byte, resolvedVer string) []byte {
	if len(data) == 0 || resolvedVer == "" {
		return data
	}

	manifestStr := string(data)
	isFirst := true

	sanitized := assemblyIdentTagRe.ReplaceAllStringFunc(manifestStr, func(tag string) string {
		matches := versionAttrRe.FindStringSubmatch(tag)
		// The first <assemblyIdentity> in a Windows manifest is the application's own identity.
		if isFirst {
			isFirst = false
			if len(matches) > 0 {
				return versionAttrRe.ReplaceAllString(tag, fmt.Sprintf(`version=%q`, resolvedVer))
			}
			if strings.HasSuffix(tag, "/>") {
				return tag[:len(tag)-2] + fmt.Sprintf(` version=%q/>`, resolvedVer)
			}
			if strings.HasSuffix(tag, ">") {
				return tag[:len(tag)-1] + fmt.Sprintf(` version=%q>`, resolvedVer)
			}
			return tag
		}

		// For dependency identities (e.g. Common-Controls), only sanitize if invalid
		if len(matches) > 1 && !valid4PartVerRe.MatchString(matches[1]) {
			return versionAttrRe.ReplaceAllString(tag, fmt.Sprintf(`version=%q`, resolvedVer))
		}
		return tag
	})

	return []byte(sanitized)
}
