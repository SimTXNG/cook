package config

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// VersionInfo holds the resolved version strings across different packaging formats.
type VersionInfo struct {
	Raw     string // e.g. "0.1.0-alpha-12"
	Commit  string // e.g. "a1b2c3d"
	Debian  string // e.g. "0.1.0~alpha12"
	RPM     string // e.g. "0.1.0~alpha12"
	Arch    string // e.g. "0.1.0alpha12"
	Void    string // e.g. "0.1.0alpha12" (no hyphen; XBPS uses '_' for the revision)
	Windows string // e.g. "0.1.0.0" or "0.1.0"
}

// FullVersion returns the version formatted with the commit if present, e.g. "0.1.0-alpha-12+a1b2c3d"
func (v *VersionInfo) FullVersion() string {
	if v.Commit != "" && v.Commit != "dev" {
		return fmt.Sprintf("%s+%s", v.Raw, v.Commit)
	}
	return v.Raw
}

// ResolveVersion resolves the version from config, version file, or git, and applies packaging translation rules.
func ResolveVersion(cfg *Config, baseDir string) (*VersionInfo, error) {
	rawVersion := cfg.Package.Version
	if rawVersion == "" && cfg.Package.VersionFile != "" {
		filePath := ResolvePath(baseDir, cfg.Package.VersionFile)
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read version file %s: %w", filePath, err)
		}

		pattern := cfg.Package.VersionRegex
		if pattern == "" {
			pattern = `(?:AppVersion|Version)\s*=\s*"([^"]+)"`
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid version regex %q: %w", pattern, err)
		}
		matches := re.FindStringSubmatch(string(data))
		if len(matches) < 2 {
			return nil, fmt.Errorf("could not find version matching %q in %s", pattern, filePath)
		}
		rawVersion = matches[1]
	}

	if rawVersion == "" {
		rawVersion = "0.0.1"
	}

	commit, _ := getGitCommit(baseDir)

	return &VersionInfo{
		Raw:     rawVersion,
		Commit:  commit,
		Debian:  FormatDebianVersion(rawVersion),
		RPM:     FormatRPMVersion(rawVersion),
		Arch:    FormatArchVersion(rawVersion),
		Void:    FormatVoidVersion(rawVersion),
		Windows: FormatWindowsVersion(rawVersion),
	}, nil
}

// getGitCommit returns the short commit for dir. A missing git binary, a
// non-repository directory, or any other git failure yields an empty commit
// rather than a sentinel that could be confused with a real revision.
func getGitCommit(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(string(out))
	if commit == "" {
		return "", fmt.Errorf("git rev-parse returned an empty commit")
	}
	return commit, nil
}

// FormatDebianVersion translates upstream version with hyphen prerelease to Debian tilde notation:
// e.g. "0.1.0-alpha-12" -> "0.1.0~alpha12"
// Debian's dpkg treats '~' as sorting before the empty string, ensuring 0.1.0~alpha12 < 0.1.0.
func FormatDebianVersion(v string) string {
	if !strings.Contains(v, "-") {
		return v
	}
	parts := strings.SplitN(v, "-", 2)
	base := parts[0]
	suffix := strings.ReplaceAll(parts[1], "-", "")
	return base + "~" + suffix
}

// FormatRPMVersion applies RPM EVR tilde translation (RPM 4.10+):
// e.g. "0.1.0-alpha-12" -> "0.1.0~alpha12"
func FormatRPMVersion(v string) string {
	return FormatDebianVersion(v)
}

// FormatArchVersion removes hyphens entirely because pacman rejects hyphens in pkgver:
// e.g. "0.1.0-alpha-12" -> "0.1.0alpha12"
func FormatArchVersion(v string) string {
	return strings.ReplaceAll(v, "-", "")
}

// FormatVoidVersion removes hyphens because XBPS reserves the hyphen as the
// name/version separator, and '_' as the revision separator:
// e.g. "0.1.0-alpha-12" -> "0.1.0alpha12"
func FormatVoidVersion(v string) string {
	return strings.ReplaceAll(v, "-", "")
}

// FormatWindowsVersion returns a strict 4-component numeric version (X.X.X.X),
// as required by NSIS VIProductVersion and PE version resources:
// e.g. "0.1.0-alpha-12" -> "0.1.0.0"
func FormatWindowsVersion(v string) string {
	p := VersionParts4(v)
	return fmt.Sprintf("%d.%d.%d.%d", p[0], p[1], p[2], p[3])
}

// VersionParts4 parses the numeric prefix of v into up to four 16-bit components,
// padding with zeros and clamping each to 65535. Non-numeric input yields 1.0.0.0.
func VersionParts4(v string) [4]uint16 {
	var out [4]uint16
	re := regexp.MustCompile(`^(\d+(?:\.\d+)*)`)
	matches := re.FindStringSubmatch(v)
	if len(matches) < 2 {
		return [4]uint16{1, 0, 0, 0}
	}
	parts := strings.Split(matches[1], ".")
	for i := 0; i < 4 && i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 {
			return [4]uint16{1, 0, 0, 0}
		}
		if n > 65535 {
			n = 65535
		}
		out[i] = uint16(n)
	}
	return out
}
