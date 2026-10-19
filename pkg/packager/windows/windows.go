package windows

import (
	"context"
	"fmt"
	"strings"

	"github.com/simtxng/cook/pkg/config"
	"github.com/simtxng/cook/pkg/packager"
)

var _ packager.Packager = (*Packager)(nil)

// Packager implements the packager.Packager interface for Windows targets.
type Packager struct{}

// New creates a new Windows packager.
func New() *Packager {
	return &Packager{}
}

func (p *Packager) Name() string {
	return "windows"
}

func (p *Packager) SupportedTargets() []string {
	return []string{"syso", "nsis", "msix"}
}

func (p *Packager) Package(ctx context.Context, cfg *config.Config, target, arch string, binaries map[string]string) ([]string, error) {
	if arch == "" {
		arch = "amd64"
	}

	switch strings.ToLower(target) {
	case "syso":
		sysoPath, err := CompileSyso(cfg, arch)
		if err != nil {
			return nil, err
		}
		return []string{sysoPath}, nil

	case "nsis":
		return buildAndSign(ctx, cfg, "NSIS installer", func() ([]string, error) {
			return BuildNSIS(ctx, cfg, arch, binaries)
		})

	case "msix":
		// MSIX is an OPC container, not a PE/MSI/CAB file: osslsigncode would
		// corrupt it. Only signtool may touch MSIX packages.
		return buildAndSign(ctx, cfg, "MSIX package", func() ([]string, error) {
			return BuildMSIX(ctx, cfg, arch, binaries)
		})

	default:
		return nil, fmt.Errorf("unsupported Windows packaging target %q (supported: %v)", target, p.SupportedTargets())
	}
}

func buildAndSign(ctx context.Context, cfg *config.Config, label string, buildFn func() ([]string, error)) ([]string, error) {
	artifacts, err := buildFn()
	if err != nil {
		return nil, err
	}
	if !IsSigningConfigured(cfg) {
		return artifacts, nil
	}
	sign := SignBinary
	if label == "MSIX package" {
		sign = SignMSIX
	}
	for _, artifact := range artifacts {
		if err := sign(ctx, cfg, artifact); err != nil {
			return nil, fmt.Errorf("failed to sign %s %s: %w", label, artifact, err)
		}
	}
	return artifacts, nil
}
