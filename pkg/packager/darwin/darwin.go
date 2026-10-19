package darwin

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/simtxng/cook/pkg/config"
	"github.com/simtxng/cook/pkg/packager"
)

var _ packager.Packager = (*Packager)(nil)

// Packager implements the packager.Packager interface for macOS / Darwin targets.
type Packager struct{}

// New creates a new Darwin packager.
func New() *Packager {
	return &Packager{}
}

func (p *Packager) Name() string {
	return "darwin"
}

func (p *Packager) SupportedTargets() []string {
	return []string{"app", "dmg"}
}

func (p *Packager) Package(ctx context.Context, cfg *config.Config, target, arch string, binaries map[string]string) ([]string, error) {
	// The arch is selected upstream when building the binaries; the Darwin
	// packager bundles whatever was produced and does not need it directly.
	_ = arch

	switch strings.ToLower(target) {
	case "app":
		appBundlePath, err := prepareAndSignApp(ctx, cfg, binaries)
		if err != nil {
			return nil, err
		}

		// Apple Notarization (if enabled)
		if err := notarize(ctx, cfg, appBundlePath, "app bundle"); err != nil {
			return nil, err
		}

		return []string{appBundlePath}, nil

	case "dmg":
		// 1. First assemble and sign the .app bundle
		appBundlePath, err := prepareAndSignApp(ctx, cfg, binaries)
		if err != nil {
			return nil, err
		}

		// 2. Synthesize styled disk image (.dmg)
		dmgPath, err := BuildDMG(ctx, cfg, appBundlePath)
		if err != nil {
			return nil, fmt.Errorf("failed to build DMG: %w", err)
		}

		// 3. Apple Notarization (if enabled)
		if err := notarize(ctx, cfg, dmgPath, "DMG"); err != nil {
			return nil, err
		}

		return []string{dmgPath, appBundlePath}, nil

	default:
		return nil, fmt.Errorf("unsupported Darwin packaging target %q (supported: %v)", target, p.SupportedTargets())
	}
}

func prepareAndSignApp(ctx context.Context, cfg *config.Config, binaries map[string]string) (string, error) {
	appBundlePath, err := AssembleAppBundle(cfg, binaries, AppBundleOptions{DevMode: false})
	if err != nil {
		return "", err
	}

	entitlementsPath, err := resolveEntitlementsPath(cfg, appBundlePath)
	if err != nil {
		return "", err
	}

	// Apply code signing according to configuration
	if err := SignAppBundle(ctx, cfg, appBundlePath, entitlementsPath); err != nil {
		return "", fmt.Errorf("failed to code sign app bundle: %w", err)
	}

	return appBundlePath, nil
}

func notarize(ctx context.Context, cfg *config.Config, artifactPath, label string) error {
	if !cfg.Darwin.Notarize {
		return nil
	}
	if err := NotarizeArtifact(ctx, cfg, artifactPath); err != nil {
		return fmt.Errorf("failed to notarize %s: %w", label, err)
	}
	return nil
}

func resolveEntitlementsPath(cfg *config.Config, appBundlePath string) (string, error) {
	if cfg.Darwin.Entitlements != "" {
		p := resolvePath(cfg.RootDir, cfg.Darwin.Entitlements)
		if !fileExists(p) {
			return "", fmt.Errorf("configured darwin.entitlements file not found: %s", p)
		}
		return p, nil
	}
	entitlementsPath := filepath.Join(filepath.Dir(appBundlePath), "entitlements.plist")
	if err := GenerateEntitlements(entitlementsPath); err != nil {
		return "", fmt.Errorf("failed to generate entitlements: %w", err)
	}
	return entitlementsPath, nil
}
