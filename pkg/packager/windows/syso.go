package windows

import (
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"path/filepath"
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

// CompileSyso compiles Windows resources (icon, manifest, version info) into a .syso COFF object.
func CompileSyso(cfg *config.Config, arch string) (string, error) {
	if arch == "" {
		arch = "amd64"
	}
	winresArch := ParseArch(arch)

	// One resolved version drives the manifest identity and the file version.
	resolved := resolvedWindowsVersion(cfg)

	rs := &winres.ResourceSet{}

	// 1. Icon compilation
	iconPath, err := resolveIconPath(cfg)
	if err != nil {
		return "", err
	}
	if iconPath != "" {
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
		if err := rs.Set(winres.RT_MANIFEST, winres.ID(1), winres.LCIDDefault, manifestData); err != nil {
			return "", fmt.Errorf("failed to embed manifest from %s: %w", manifestPath, err)
		}
	} else {
		// Generate standard manifest with permonitorv2, common controls v6, asInvoker
		v4 := config.VersionParts4(resolved)
		ident := winres.AssemblyIdentity{
			Version: v4,
		}
		if cfg.Package.Identifier != "" {
			ident.Name = cfg.Package.Identifier
		} else if cfg.Package.Name != "" {
			ident.Name = cfg.Package.Name
		}

		rs.SetManifest(winres.AppManifest{
			Identity:            ident,
			Description:         cfg.Package.Description,
			DPIAwareness:        winres.DPIPerMonitorV2,
			UseCommonControlsV6: true,
			ExecutionLevel:      winres.AsInvoker,
		})
	}

	// 3. Version Info (info.json / config metadata)
	infoJSONPath, err := resolveInfoJSONPath(cfg)
	if err != nil {
		return "", err
	}
	vi, err := buildVersionInfo(cfg, resolved, infoJSONPath)
	if err != nil {
		return "", err
	}
	rs.SetVersionInfo(vi)

	// 4. Output path determination
	outFile := cfg.Windows.Syso.Out
	if outFile == "" {
		outFile = filepath.Join(cfg.RootDir, fmt.Sprintf("wails_windows_%s.syso", arch))
	} else if !filepath.IsAbs(outFile) {
		outFile = filepath.Join(cfg.RootDir, outFile)
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

func buildVersionInfo(cfg *config.Config, resolved, infoJSONPath string) (version.Info, error) {
	vi := version.Info{}

	v4 := config.VersionParts4(resolved)
	vi.FileVersion = v4
	vi.ProductVersion = v4

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

	set := func(langID uint16, key, value string) error {
		if err := vi.Set(langID, key, value); err != nil {
			return fmt.Errorf("failed to set version resource %q: %w", key, err)
		}
		return nil
	}

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

	return vi, nil
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}
