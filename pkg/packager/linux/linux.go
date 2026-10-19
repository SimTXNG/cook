package linux

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strings"

	"github.com/simtxng/cook/pkg/config"
	"github.com/simtxng/cook/pkg/packager"
)

// Ensure Packager implements packager.Packager interface at compile time.
var _ packager.Packager = (*Packager)(nil)

// Packager implements the packager.Packager interface for Linux.
type Packager struct{}

// NewPackager creates and returns a new Linux Packager.
func NewPackager() packager.Packager {
	return &Packager{}
}

// Name returns the platform identifier "linux".
func (p *Packager) Name() string {
	return "linux"
}

// SupportedTargets returns the list of supported packaging formats for Linux.
func (p *Packager) SupportedTargets() []string {
	return []string{"deb", "rpm", "archlinux", "appimage"}
}

// Package executes packaging for the requested target ("deb", "rpm", "archlinux", "appimage").
func (p *Packager) Package(ctx context.Context, cfg *config.Config, target, arch string, binaries map[string]string) ([]string, error) {
	if arch == "" {
		arch = runtime.GOARCH
	}

	t := strings.ToLower(target)
	t = strings.TrimPrefix(t, "linux.")
	t = strings.TrimPrefix(t, "linux:")
	if t == "aur" {
		t = "archlinux"
	}

	switch t {
	case "deb", "rpm", "archlinux":
		return PackageNFPM(ctx, cfg, t, arch, binaries)
	case "appimage":
		return PackageAppImage(ctx, cfg, arch, binaries)
	default:
		return nil, fmt.Errorf("unsupported linux target %q (supported targets: %v)", target, p.SupportedTargets())
	}
}

// selectCLIBinary chooses the companion CLI deterministically: the configured
// bundled_cli first, then non-GUI entries in cfg.Binaries order, then a
// name-sorted lookup for names containing "cli", and finally the "cli" fallback.
func selectCLIBinary(cfg *config.Config, binaries map[string]string, outDir string) (string, string) {
	if cliName := cfg.Linux.BundledCLI; cliName != "" {
		return cliName, findBinary(cliName, binaries, outDir)
	}

	for _, b := range cfg.Binaries {
		if b.GUI {
			continue
		}
		if path := findBinary(b.Name, binaries, outDir); path != "" {
			return b.Name, path
		}
	}

	names := make([]string, 0, len(binaries))
	for name := range binaries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.Contains(strings.ToLower(name), "cli") {
			if path := findBinary(name, binaries, outDir); path != "" {
				return name, path
			}
		}
	}

	return "cli", findBinary("cli", binaries, outDir)
}
