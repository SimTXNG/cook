package darwin

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/simtxng/cook/pkg/config"
	cookExec "github.com/simtxng/cook/pkg/exec"
)

// BuildAdHocCodesignArgs generates the argument slice for ad-hoc deep code signing.
func BuildAdHocCodesignArgs(appBundlePath string) []string {
	return []string{"--force", "--deep", "--sign", "-", appBundlePath}
}

// BuildDeveloperIDCodesignArgs generates the argument slice for hardened runtime Developer ID code signing.
func BuildDeveloperIDCodesignArgs(identity, entitlementsPath, appBundlePath string) []string {
	args := []string{"--force", "--deep", "--options", "runtime"}
	if entitlementsPath != "" {
		args = append(args, "--entitlements", entitlementsPath)
	}
	args = append(args, "--sign", identity, appBundlePath)
	return args
}

// BuildVerifyCodesignArgs generates the argument slice to strictly verify code signing.
func BuildVerifyCodesignArgs(appBundlePath string) []string {
	return []string{"--verify", "--deep", "--strict", "--verbose=2", appBundlePath}
}

// SignAppBundle applies code signing to the .app bundle according to config.Darwin.Codesign.
func SignAppBundle(ctx context.Context, cfg *config.Config, appBundlePath, entitlementsPath string) error {
	mode := strings.ToLower(cfg.Darwin.Codesign)
	if mode == "" {
		mode = "adhoc"
	}
	if mode == "skip" {
		fmt.Printf("==> Codesign skipped for %s (mode=skip)\n", appBundlePath)
		return nil
	}

	// Validate the mode and its required configuration before probing for the
	// tool, so misconfiguration fails loudly even when cross-compiling on a
	// host without codesign.
	switch mode {
	case "adhoc", "developer_id":
	default:
		return fmt.Errorf("unknown codesign mode %q (must be adhoc, developer_id, or skip)", mode)
	}
	identity := cfg.Darwin.CodesignIdentity
	if mode == "developer_id" && identity == "" {
		return fmt.Errorf("codesign mode %q requires darwin.codesign_identity to be set (e.g. \"Developer ID Application: Your Name (TEAMID)\")", mode)
	}

	codesignPath, err := exec.LookPath("codesign")
	if err != nil {
		if runtime.GOOS != "darwin" {
			fmt.Printf("==> Skipping codesign: 'codesign' tool not available on %s. Sign the .app on macOS before distribution.\n", runtime.GOOS)
			return nil
		}
		return fmt.Errorf("codesign binary not found in PATH: %w", err)
	}

	runner := cookExec.NewRunner("codesign")

	var signArgs []string
	switch mode {
	case "adhoc":
		fmt.Printf("==> Ad-hoc signing %s with codesign...\n", appBundlePath)
		signArgs = BuildAdHocCodesignArgs(appBundlePath)
	case "developer_id":
		fmt.Printf("==> Signing %s with Developer ID (%s) and Hardened Runtime...\n", appBundlePath, identity)
		signArgs = BuildDeveloperIDCodesignArgs(identity, entitlementsPath, appBundlePath)
	}

	if err := runner.RunArgs(ctx, codesignPath, signArgs, cfg.RootDir, nil); err != nil {
		return fmt.Errorf("codesign execution failed: %w", err)
	}

	// Verify code signature
	verifyArgs := BuildVerifyCodesignArgs(appBundlePath)
	if err := runner.RunArgs(ctx, codesignPath, verifyArgs, cfg.RootDir, nil); err != nil {
		return fmt.Errorf("codesign verification failed: %w", err)
	}

	fmt.Printf("==> Code signature verified for %s\n", appBundlePath)
	return nil
}
