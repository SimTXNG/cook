package windows

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/simtxng/cook/pkg/config"
	cookExec "github.com/simtxng/cook/pkg/exec"
)

const (
	DefaultTimestampServer = "http://timestamp.digicert.com"
)

// IsSigningConfigured returns true if certificate or thumbprint is specified in cook.toml.
func IsSigningConfigured(cfg *config.Config) bool {
	return cfg.Windows.Signing.Certificate != "" || cfg.Windows.Signing.Thumbprint != ""
}

// BuildSignToolArgs constructs CLI arguments for Microsoft signtool.exe.
// /f (certificate file) and /sha1 (certificate thumbprint) are mutually
// exclusive selectors: prefer the certificate file, fall back to the
// thumbprint, and fail if neither is configured.
func BuildSignToolArgs(cfg *config.Config, targetFile string) ([]string, error) {
	args := []string{"sign", "/fd", "SHA256"}

	ts := cfg.Windows.Signing.TimestampServer
	if ts == "" {
		ts = DefaultTimestampServer
	}
	args = append(args, "/tr", ts, "/td", "SHA256")

	switch {
	case cfg.Windows.Signing.Certificate != "":
		certPath := config.ResolvePath(cfg.RootDir, cfg.Windows.Signing.Certificate)
		args = append(args, "/f", certPath)

		// Supply the PFX passphrase non-interactively so CI never blocks on a
		// signtool prompt.
		if cfg.Windows.Signing.Password != "" {
			args = append(args, "/p", cfg.Windows.Signing.Password)
		}
	case cfg.Windows.Signing.Thumbprint != "":
		args = append(args, "/sha1", cfg.Windows.Signing.Thumbprint)
	default:
		return nil, fmt.Errorf("authenticode signing configured for %s but neither certificate nor thumbprint is set", targetFile)
	}

	args = append(args, targetFile)
	return args, nil
}

// BuildOsslsigncodeArgs constructs CLI arguments for cross-platform osslsigncode.
func BuildOsslsigncodeArgs(cfg *config.Config, targetFile, outFile string) []string {
	args := []string{"sign", "-h", "sha256"}

	if cfg.Windows.Signing.Certificate != "" {
		certPath := config.ResolvePath(cfg.RootDir, cfg.Windows.Signing.Certificate)
		args = append(args, "-pkcs12", certPath)
		// Always pass the (possibly empty) passphrase so osslsigncode never
		// falls back to an interactive prompt on stdin/tty.
		args = append(args, "-pass", cfg.Windows.Signing.Password)
	}

	ts := cfg.Windows.Signing.TimestampServer
	if ts == "" {
		ts = DefaultTimestampServer
	}
	args = append(args, "-ts", ts)

	args = append(args, "-in", targetFile, "-out", outFile)
	return args
}

// SignBinary signs an executable or installer using signtool or osslsigncode.
func SignBinary(ctx context.Context, cfg *config.Config, targetFile string) error {
	if !IsSigningConfigured(cfg) {
		return nil
	}

	runner := cookExec.NewRunner("authenticode")

	// 1. Try native Windows signtool
	if signtoolPath, err := exec.LookPath("signtool"); err == nil {
		args, err := BuildSignToolArgs(cfg, targetFile)
		if err != nil {
			return err
		}
		fmt.Printf("==> Signing %s with signtool...\n", targetFile)
		return runner.RunArgs(ctx, signtoolPath, args, cfg.RootDir, nil)
	}

	// 2. Try cross-platform osslsigncode (available on Linux / macOS)
	if osslsigncodePath, err := exec.LookPath("osslsigncode"); err == nil {
		if cfg.Windows.Signing.Certificate == "" {
			return fmt.Errorf("cannot sign %s: signtool is required for thumbprint-based signing, and osslsigncode needs a certificate file", targetFile)
		}
		tmpSigned := targetFile + ".signed"
		// Never trust a stale .signed file from an earlier run.
		_ = os.Remove(tmpSigned)
		committed := false
		defer func() {
			if !committed {
				_ = os.Remove(tmpSigned)
			}
		}()

		args := BuildOsslsigncodeArgs(cfg, targetFile, tmpSigned)
		fmt.Printf("==> Signing %s with osslsigncode...\n", targetFile)
		if err := runner.RunArgs(ctx, osslsigncodePath, args, cfg.RootDir, nil); err != nil {
			return err
		}
		if err := replaceFile(tmpSigned, targetFile); err != nil {
			return err
		}
		committed = true
		return nil
	}

	return fmt.Errorf("authenticode signing configured for %s but neither signtool nor osslsigncode was found in PATH", targetFile)
}

// replaceFile moves tmp over target. os.Rename cannot replace an existing
// file on Windows, so remove the target first when the direct rename fails.
func replaceFile(tmp, target string) error {
	if err := os.Rename(tmp, target); err == nil {
		return nil
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove existing file %s: %w", target, err)
	}
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("failed to replace %s with signed copy: %w", target, err)
	}
	return nil
}

// SignMSIX signs an MSIX package. osslsigncode cannot sign OPC/MSIX containers,
// so this path intentionally requires signtool and never falls back to it.
func SignMSIX(ctx context.Context, cfg *config.Config, targetFile string) error {
	if !IsSigningConfigured(cfg) {
		return nil
	}

	signtoolPath, err := exec.LookPath("signtool")
	if err != nil {
		return fmt.Errorf("cannot sign MSIX package %s: signtool is required (osslsigncode cannot sign MSIX containers): %w", targetFile, err)
	}

	args, err := BuildSignToolArgs(cfg, targetFile)
	if err != nil {
		return err
	}

	runner := cookExec.NewRunner("authenticode")
	fmt.Printf("==> Signing MSIX %s with signtool...\n", targetFile)
	return runner.RunArgs(ctx, signtoolPath, args, cfg.RootDir, nil)
}
