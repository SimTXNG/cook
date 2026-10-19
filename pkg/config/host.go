package config

import (
	"path/filepath"
	"runtime"
	"strings"
)

// NormalizeOS returns a canonical GOOS value. Empty input resolves to the host
// OS; common aliases are accepted so CLI flags and config values agree.
func NormalizeOS(value string) string {
	switch v := strings.ToLower(strings.TrimSpace(value)); v {
	case "":
		return runtime.GOOS
	case "mac", "macos", "osx", "darwin":
		return "darwin"
	case "win", "win32", "win64", "windows":
		return "windows"
	case "linux":
		return "linux"
	default:
		return v
	}
}

// ResolvePath returns "" for an empty path, cleans absolute paths, and joins
// relative paths under root. With an empty root it returns the cleaned path.
func ResolvePath(root, path string) string {
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if root == "" {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(root, path))
}

// NormalizeArch returns a canonical GOARCH value. Empty input resolves to the
// host architecture; common aliases are accepted.
func NormalizeArch(value string) string {
	switch v := strings.ToLower(strings.TrimSpace(value)); v {
	case "":
		return runtime.GOARCH
	case "x86_64", "x64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	case "i386", "i686", "386", "x86":
		return "386"
	default:
		return v
	}
}
