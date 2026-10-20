package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Config represents the complete packaging and build configuration (cook.toml).
type Config struct {
	Package   PackageConfig  `toml:"package"`
	Frontend  FrontendConfig `toml:"frontend"`
	Binaries  []BinaryConfig `toml:"binaries"`
	Hooks     HooksConfig    `toml:"hooks"`
	Linux     LinuxConfig    `toml:"linux"`
	Windows   WindowsConfig  `toml:"windows"`
	Darwin    DarwinConfig   `toml:"darwin"`
	Wails     WailsConfig    `toml:"wails"`
	Templates TemplateConfig `toml:"templates"`

	// Resolved runtime info (not in TOML directly)
	Version *VersionInfo `toml:"-"`
	RootDir string       `toml:"-"`
}

type PackageConfig struct {
	Name         string `toml:"name"`
	ProductName  string `toml:"product_name"`
	Company      string `toml:"company"`
	Identifier   string `toml:"identifier"`
	Description  string `toml:"description"`
	Copyright    string `toml:"copyright"`
	License      string `toml:"license"`
	Version      string `toml:"version"`
	VersionFile  string `toml:"version_file"`
	VersionRegex string `toml:"version_regex"`
	Homepage     string `toml:"homepage"`
	Maintainer   string `toml:"maintainer"`
	OutputDir    string `toml:"output_dir"`
	Icon         string `toml:"icon"`
	RootDir      string `toml:"root_dir"`
}

type FrontendConfig struct {
	Enabled        bool   `toml:"enabled"`
	Dir            string `toml:"dir"`
	PackageManager string `toml:"package_manager"` // npm, bun, pnpm, yarn
	InstallCmd     string `toml:"install_cmd"`
	BuildCmd       string `toml:"build_cmd"`
	DevCmd         string `toml:"dev_cmd"`
	BindingsCmd    string `toml:"bindings_cmd"`
	DistDir        string `toml:"dist_dir"`
}

type BinaryConfig struct {
	Name      string   `toml:"name"`
	Path      string   `toml:"path"`
	GUI       bool     `toml:"gui"`
	CGO       bool     `toml:"cgo"`
	Tags      []string `toml:"tags"`
	Ldflags   string   `toml:"ldflags"`
	CommitVar string   `toml:"commit_var"`
	CleanSyso bool     `toml:"clean_syso"`
	UPX       bool     `toml:"upx"`
	UPXLevel  int      `toml:"upx_level"`
}

// EffectiveUPXLevel returns the configured UPX compression level (1-9),
// defaulting to 6 for fast builds.
func (b BinaryConfig) EffectiveUPXLevel() int {
	if b.UPXLevel >= 1 && b.UPXLevel <= 9 {
		return b.UPXLevel
	}
	return 6
}

type HooksConfig struct {
	PreBuild    []string          `toml:"pre_build"`
	PostBuild   []string          `toml:"post_build"`
	PrePackage  []string          `toml:"pre_package"`
	PostPackage []string          `toml:"post_package"`
	FixScripts  map[string]string `toml:"fix_scripts"`
}

type LinuxConfig struct {
	Targets     []string        `toml:"targets"` // deb, rpm, archlinux, appimage
	DesktopFile string          `toml:"desktop_file"`
	Categories  string          `toml:"categories"`
	BundledCLI  string          `toml:"bundled_cli"`
	Deb         LinuxDebConfig  `toml:"deb"`
	RPM         LinuxRPMConfig  `toml:"rpm"`
	Arch        LinuxArchConfig `toml:"archlinux"`
	Void        LinuxVoidConfig `toml:"void"`
	AppImage    AppImageConfig  `toml:"appimage"`
}

type LinuxDebConfig struct {
	Section     string   `toml:"section"`
	Priority    string   `toml:"priority"`
	Depends     []string `toml:"depends"`
	Postinstall string   `toml:"postinstall"`
	Prerm       string   `toml:"prerm"`
	Postrm      string   `toml:"postrm"`
}

type LinuxRPMConfig struct {
	Group   string   `toml:"group"`
	Depends []string `toml:"depends"`
	GPGKey  string   `toml:"gpg_key"`
	GPGName string   `toml:"gpg_name"`
}

type LinuxArchConfig struct {
	PkgBase string   `toml:"pkgbase"`
	Depends []string `toml:"depends"`
	Sign    bool     `toml:"sign"`
	GPGKey  string   `toml:"gpg_key"`
	GPGName string   `toml:"gpg_name"`
}

// LinuxVoidConfig configures Void Linux (XBPS) packaging.
type LinuxVoidConfig struct {
	Depends       []string `toml:"depends"`
	Conflicts     []string `toml:"conflicts"`
	Provides      []string `toml:"provides"`
	Replaces      []string `toml:"replaces"`
	ConfFiles     []string `toml:"conf_files"`
	MutableFiles  []string `toml:"mutable_files"`
	Tags          string   `toml:"tags"`
	Revision      int      `toml:"revision"`
	InstallScript string   `toml:"install_script"`
	RemoveScript  string   `toml:"remove_script"`
}

// EffectiveRevision returns the XBPS package revision, defaulting to 1.
func (v LinuxVoidConfig) EffectiveRevision() int {
	if v.Revision > 0 {
		return v.Revision
	}
	return 1
}

type AppImageConfig struct {
	FixScript      string `toml:"fix_script"`
	UpdateInfo     string `toml:"update_info"`
	GenerateZsync  *bool  `toml:"generate_zsync"`
	GenerateSHA256 *bool  `toml:"generate_sha256"`
	ToolsDir       string `toml:"tools_dir"`
}

// SHA256Enabled reports whether a SHA-256 checksum should be written (default true).
func (a AppImageConfig) SHA256Enabled() bool {
	return a.GenerateSHA256 == nil || *a.GenerateSHA256
}

// ZsyncEnabled reports whether a zsync delta file should be generated.
// It defaults to true when update_info is set.
func (a AppImageConfig) ZsyncEnabled() bool {
	if a.GenerateZsync != nil {
		return *a.GenerateZsync
	}
	return a.UpdateInfo != ""
}

type WindowsConfig struct {
	Targets    []string          `toml:"targets"` // syso, nsis, msix
	BundledCLI string            `toml:"bundled_cli"`
	Syso       WindowsSysoConfig `toml:"syso"`
	NSIS       WindowsNSISConfig `toml:"nsis"`
	Signing    WindowsSignConfig `toml:"signing"`
}

type WindowsSysoConfig struct {
	Icon     string `toml:"icon"`
	Manifest string `toml:"manifest"`
	InfoJSON string `toml:"info_json"`
	Out      string `toml:"out"`
}

type WindowsNSISConfig struct {
	Script               string            `toml:"script"`
	InstallScope         string            `toml:"install_scope"` // machine or user
	Webview2Bootstrapper string            `toml:"webview2_bootstrapper"`
	AutoDownloadWebview2 bool              `toml:"auto_download_webview2"`
	Defines              map[string]string `toml:"defines"`
}

type WindowsSignConfig struct {
	Certificate     string `toml:"certificate"`
	Thumbprint      string `toml:"thumbprint"`
	TimestampServer string `toml:"timestamp_server"`
	Password        string `toml:"password"`
	Publisher       string `toml:"publisher"`
}

type DarwinConfig struct {
	Targets                    []string        `toml:"targets"` // app, dmg
	BundleID                   string          `toml:"bundle_id"`
	MinOSVersion               string          `toml:"min_os_version"`
	InfoPlist                  string          `toml:"info_plist"`
	Entitlements               string          `toml:"entitlements"`
	Icons                      string          `toml:"icons"`
	AssetsCar                  string          `toml:"assets_car"`
	BundledCLI                 string          `toml:"bundled_cli"`
	MicrophoneUsageDescription string          `toml:"microphone_usage_description"`
	Codesign                   string          `toml:"codesign"` // adhoc, developer_id, skip
	CodesignIdentity           string          `toml:"codesign_identity"`
	Notarize                   bool            `toml:"notarize"`
	NotarizeProfile            string          `toml:"notarize_profile"`
	DMG                        DarwinDMGConfig `toml:"dmg"`
}

type DarwinDMGConfig struct {
	Background           string `toml:"background"`
	VolumeIcon           string `toml:"volume_icon"`
	WindowWidth          int    `toml:"window_width"`
	WindowHeight         int    `toml:"window_height"`
	AppPosition          []int  `toml:"app_position"`
	ApplicationsPosition []int  `toml:"applications_position"`
}

type WailsConfig struct {
	ConfigFile        string             `toml:"config_file"`
	Version           string             `toml:"version"`
	CompanyName       string             `toml:"company_name"`
	ProductName       string             `toml:"product_name"`
	ProductIdentifier string             `toml:"product_identifier"`
	Description       string             `toml:"description"`
	Copyright         string             `toml:"copyright"`
	Comments          string             `toml:"comments"`
	Dev               WailsDevTomlConfig `toml:"dev"`
	FileAssociations  []WailsFileAssoc   `toml:"file_associations"`
}

type WailsDevTomlConfig struct {
	RootPath         string            `toml:"root_path"`
	LogLevel         string            `toml:"log_level"`
	Debounce         int               `toml:"debounce"`
	IgnoreDirs       []string          `toml:"ignore_dirs"`
	IgnoreFiles      []string          `toml:"ignore_files"`
	WatchedExtension []string          `toml:"watched_extension"`
	GitIgnore        *bool             `toml:"git_ignore"`
	Executes         []WailsDevExecute `toml:"executes"`
}

type WailsDevExecute struct {
	Name            string `toml:"name" yaml:"name,omitempty"`
	Cmd             string `toml:"cmd" yaml:"cmd"`
	Type            string `toml:"type" yaml:"type"`
	Dir             string `toml:"dir" yaml:"dir,omitempty"`
	ExitPolicy      string `toml:"exit_policy" yaml:"exit_policy,omitempty"`
	ShutdownTimeout string `toml:"shutdown_timeout" yaml:"shutdown_timeout,omitempty"`
}

type WailsFileAssoc struct {
	Ext         string `toml:"ext" yaml:"ext"`
	Name        string `toml:"name" yaml:"name"`
	Description string `toml:"description" yaml:"description"`
	IconName    string `toml:"icon_name" yaml:"iconName"`
	Role        string `toml:"role" yaml:"role"`
	MimeType    string `toml:"mime_type,omitempty" yaml:"mimeType,omitempty"`
}

type TemplateConfig struct {
	Dir      string                  `toml:"dir"`
	Mappings []TemplateMappingConfig `toml:"mappings"`
}

type TemplateMappingConfig struct {
	Src  string `toml:"src"`
	Dest string `toml:"dest"`
}

// LoadConfig reads and parses a TOML configuration file, applying defaults and resolving version info.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	cfg := &Config{}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse TOML configuration in %s: %w", path, err)
	}

	configDir := filepath.Dir(path)
	if cfg.Package.RootDir != "" {
		if filepath.IsAbs(cfg.Package.RootDir) {
			cfg.RootDir = cfg.Package.RootDir
		} else {
			cfg.RootDir = filepath.Clean(filepath.Join(configDir, cfg.Package.RootDir))
		}
	} else {
		cfg.RootDir = configDir
	}
	if absRootDir, err := filepath.Abs(cfg.RootDir); err == nil {
		cfg.RootDir = absRootDir
	}

	applyDefaults(cfg)

	// Resolve version information
	ver, err := ResolveVersion(cfg, cfg.RootDir)
	if err != nil {
		return nil, fmt.Errorf("version resolution failed: %w", err)
	}
	cfg.Version = ver

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// Validate checks user-configurable values that would otherwise fail late in
// packaging or, worse, produce silently empty artifact sets.
func (c *Config) Validate() error {
	if c.Package.Name == "" {
		return fmt.Errorf("package.name is required")
	}
	if len(c.Package.Name) > 64 {
		return fmt.Errorf("package.name %q is too long (max 64)", c.Package.Name)
	}
	if !packageNamePattern.MatchString(c.Package.Name) {
		return fmt.Errorf("package.name %q is invalid: use lowercase letters, digits, and +._- (starting with a letter or digit)", c.Package.Name)
	}

	rawVersion := c.Package.Version
	if c.Version != nil {
		rawVersion = c.Version.Raw
	}
	if rawVersion != "" && !versionPattern.MatchString(rawVersion) {
		return fmt.Errorf("version %q is invalid: use digits, letters, and .+~_- (starting with a digit)", rawVersion)
	}

	if len(c.Binaries) == 0 {
		return fmt.Errorf("at least one [[binaries]] entry is required")
	}
	seen := make(map[string]bool, len(c.Binaries))
	for i, b := range c.Binaries {
		if b.Name == "" {
			return fmt.Errorf("binaries[%d].name is required", i)
		}
		if seen[b.Name] {
			return fmt.Errorf("duplicate binary name %q", b.Name)
		}
		seen[b.Name] = true
	}

	targetSets := []struct {
		os      string
		targets []string
	}{
		{"linux", c.Linux.Targets},
		{"windows", c.Windows.Targets},
		{"darwin", c.Darwin.Targets},
	}
	for _, set := range targetSets {
		for _, target := range set.targets {
			t := strings.ToLower(strings.TrimSpace(target))
			if !knownTargets[set.os][t] {
				return fmt.Errorf("invalid %s target %q (allowed: %s)", set.os, target, strings.Join(allowedTargets(set.os), ", "))
			}
		}
	}

	for _, pos := range []struct {
		name   string
		values []int
	}{
		{"app_position", c.Darwin.DMG.AppPosition},
		{"applications_position", c.Darwin.DMG.ApplicationsPosition},
	} {
		if len(pos.values) != 0 && len(pos.values) != 2 {
			return fmt.Errorf("darwin.dmg.%s must contain exactly two integers", pos.name)
		}
	}

	return nil
}

var (
	packageNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9+._-]*$`)
	versionPattern     = regexp.MustCompile(`^[0-9][A-Za-z0-9.+~_-]*$`)
	knownTargets       = map[string]map[string]bool{
		"linux":   {"appimage": true, "deb": true, "rpm": true, "archlinux": true, "aur": true, "void": true, "xbps": true},
		"windows": {"nsis": true, "msix": true, "syso": true},
		"darwin":  {"app": true, "dmg": true},
	}
)

func allowedTargets(targetOS string) []string {
	targets := make([]string, 0, len(knownTargets[targetOS]))
	for target := range knownTargets[targetOS] {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	return targets
}

// ResolvedVersion returns the resolved VersionInfo. When the config was built
// programmatically (Version == nil), it synthesizes a minimal version from
// Package.Version so packagers never nil-deref.
func (c *Config) ResolvedVersion() *VersionInfo {
	if c.Version != nil {
		return c.Version
	}
	raw := c.Package.Version
	if raw == "" {
		raw = "0.0.1"
	}
	return &VersionInfo{
		Raw:     raw,
		Debian:  FormatDebianVersion(raw),
		RPM:     FormatRPMVersion(raw),
		Arch:    FormatArchVersion(raw),
		Void:    FormatVoidVersion(raw),
		Windows: FormatWindowsVersion(raw),
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func applyDefaults(cfg *Config) {
	cfg.Package.OutputDir = firstNonEmpty(cfg.Package.OutputDir, "bin")
	cfg.Frontend.PackageManager = firstNonEmpty(cfg.Frontend.PackageManager, "npm")
	cfg.Frontend.Dir = firstNonEmpty(cfg.Frontend.Dir, "frontend")
	cfg.Frontend.DistDir = firstNonEmpty(cfg.Frontend.DistDir, filepath.Join(cfg.Frontend.Dir, "dist"))
	cfg.Frontend.BuildCmd = firstNonEmpty(cfg.Frontend.BuildCmd, "npm run build")
	cfg.Templates.Dir = firstNonEmpty(cfg.Templates.Dir, "build/templates")

	// Linux defaults: AppImage SHA256/zsync defaults are resolved by
	// (*AppImageConfig).SHA256Enabled / ZsyncEnabled so an explicit false is honoured.

	// Windows defaults
	cfg.Windows.NSIS.InstallScope = firstNonEmpty(cfg.Windows.NSIS.InstallScope, "machine")

	// Darwin defaults
	cfg.Darwin.MinOSVersion = firstNonEmpty(cfg.Darwin.MinOSVersion, "12.0")
	cfg.Darwin.Codesign = firstNonEmpty(cfg.Darwin.Codesign, "adhoc")
	if cfg.Darwin.DMG.WindowWidth == 0 {
		cfg.Darwin.DMG.WindowWidth = 540
	}
	if cfg.Darwin.DMG.WindowHeight == 0 {
		cfg.Darwin.DMG.WindowHeight = 380
	}
	if len(cfg.Darwin.DMG.AppPosition) == 0 {
		cfg.Darwin.DMG.AppPosition = []int{135, 190}
	}
	if len(cfg.Darwin.DMG.ApplicationsPosition) == 0 {
		cfg.Darwin.DMG.ApplicationsPosition = []int{405, 190}
	}
}
