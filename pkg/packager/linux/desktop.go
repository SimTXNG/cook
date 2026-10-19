package linux

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/simtxng/cook/pkg/config"
)

// GenerateDesktopContent creates standard FreeDesktop .desktop content from Config and overrides.
func GenerateDesktopContent(cfg *config.Config, execName, iconName string) string {
	name := cfg.Package.ProductName
	if name == "" {
		name = cfg.Package.Name
	}

	exec := execName
	if exec == "" {
		exec = cfg.Package.Name
	}

	icon := iconName
	if icon == "" {
		// Packagers install the icon as <Package.Name>.png regardless of the
		// source filename, so Icon= must reference that installed name.
		icon = cfg.Package.Name
	}

	categories := cfg.Linux.Categories
	if categories == "" {
		categories = "Development;Utility;"
	}
	if !strings.HasSuffix(categories, ";") {
		categories += ";"
	}

	comment := cfg.Package.Description

	var sb strings.Builder
	sb.WriteString("[Desktop Entry]\n")
	sb.WriteString("Type=Application\n")
	sb.WriteString(fmt.Sprintf("Name=%s\n", name))
	if comment != "" {
		sb.WriteString(fmt.Sprintf("Comment=%s\n", comment))
	}
	sb.WriteString(fmt.Sprintf("Exec=%s\n", exec))
	sb.WriteString(fmt.Sprintf("Icon=%s\n", icon))
	sb.WriteString(fmt.Sprintf("Categories=%s\n", categories))
	sb.WriteString("Terminal=false\n")
	sb.WriteString("StartupNotify=false\n")
	if cfg.Package.Name != "" {
		sb.WriteString(fmt.Sprintf("StartupWMClass=%s\n", cfg.Package.Name))
	}

	return sb.String()
}

// WriteDesktopFile writes the .desktop file content to destPath, creating parent directories if needed.
func WriteDesktopFile(destPath string, content string) error {
	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory for desktop file %s: %w", dir, err)
	}
	if err := os.WriteFile(destPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write desktop file %s: %w", destPath, err)
	}
	return nil
}

// EnsureDesktopFile locates an existing desktop file from cfg.Linux.DesktopFile or generates a standard one at destPath.
// A configured desktop_file that cannot be read is an error: silently generating
// a replacement would mask a typo and ship a different menu entry than requested.
func EnsureDesktopFile(cfg *config.Config, destPath string) (string, error) {
	if cfg.Linux.DesktopFile != "" {
		src := resolvePath(cfg.Linux.DesktopFile, cfg.RootDir)

		fi, err := os.Stat(src)
		if err != nil {
			return "", fmt.Errorf("configured linux.desktop_file %q is not readable: %w", cfg.Linux.DesktopFile, err)
		}
		if fi.IsDir() {
			return "", fmt.Errorf("configured linux.desktop_file %q is a directory, not a file", cfg.Linux.DesktopFile)
		}

		// Source desktop file exists, copy to destPath if different
		if destPath != "" && filepath.Clean(src) != filepath.Clean(destPath) {
			data, err := os.ReadFile(src)
			if err != nil {
				return "", fmt.Errorf("failed to read desktop file %s: %w", src, err)
			}
			if err := WriteDesktopFile(destPath, string(data)); err != nil {
				return "", err
			}
			return destPath, nil
		}
		return src, nil
	}

	// Generate desktop content and write to destPath
	content := GenerateDesktopContent(cfg, "", "")
	if destPath == "" {
		destDir := filepath.Join(cfg.RootDir, cfg.Package.OutputDir)
		destPath = filepath.Join(destDir, fmt.Sprintf("%s.desktop", cfg.Package.Name))
	}
	if err := WriteDesktopFile(destPath, content); err != nil {
		return "", err
	}
	return destPath, nil
}
