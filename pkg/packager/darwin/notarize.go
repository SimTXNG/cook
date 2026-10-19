package darwin

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"

	"github.com/simtxng/cook/pkg/config"
	cookExec "github.com/simtxng/cook/pkg/exec"
)

// BuildNotarySubmitArgs constructs CLI arguments for xcrun notarytool submit.
func BuildNotarySubmitArgs(artifactPath, keychainProfile string) []string {
	args := []string{"notarytool", "submit", artifactPath}
	if keychainProfile != "" {
		args = append(args, "--keychain-profile", keychainProfile)
	}
	args = append(args, "--wait")
	return args
}

// BuildStaplerArgs constructs CLI arguments for xcrun stapler.
func BuildStaplerArgs(artifactPath string) []string {
	return []string{"stapler", "staple", artifactPath}
}

// NotarizeArtifact submits a DMG or .app bundle to Apple Notary Service via xcrun notarytool and staples the ticket.
func NotarizeArtifact(ctx context.Context, cfg *config.Config, artifactPath string) error {
	if !cfg.Darwin.Notarize {
		return nil
	}

	// Fail early on missing credentials rather than silently skipping: a
	// notarization request with no profile cannot succeed anywhere.
	profile := cfg.Darwin.NotarizeProfile
	if profile == "" {
		return fmt.Errorf("notarization is enabled but darwin.notarize_profile is empty: create a keychain profile first with `xcrun notarytool store-credentials` and set darwin.notarize_profile to its name")
	}

	xcrunPath, err := exec.LookPath("xcrun")
	if err != nil {
		if runtime.GOOS != "darwin" {
			fmt.Printf("==> Skipping Apple notarization: 'xcrun' tool not available on %s. Notarize on macOS before distribution.\n", runtime.GOOS)
			return nil
		}
		return fmt.Errorf("xcrun binary not found in PATH: %w", err)
	}

	runner := cookExec.NewRunner("notary")

	// 1. Submit for notarization
	submitArgs := BuildNotarySubmitArgs(artifactPath, profile)
	fmt.Printf("==> Submitting %s to Apple Notary Service...\n", artifactPath)
	if err := runner.RunArgs(ctx, xcrunPath, submitArgs, cfg.RootDir, nil); err != nil {
		return fmt.Errorf("notarization submission failed: %w", err)
	}

	// 2. Staple notarization ticket
	stapleArgs := BuildStaplerArgs(artifactPath)
	fmt.Printf("==> Stapling notarization ticket to %s...\n", artifactPath)
	if err := runner.RunArgs(ctx, xcrunPath, stapleArgs, cfg.RootDir, nil); err != nil {
		return fmt.Errorf("stapling notarization ticket failed: %w", err)
	}

	fmt.Printf("==> Successfully notarized and stapled %s\n", artifactPath)
	return nil
}
