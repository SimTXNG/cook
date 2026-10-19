package windows

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/simtxng/cook/pkg/config"
	cookExec "github.com/simtxng/cook/pkg/exec"
	"github.com/simtxng/cook/pkg/packager"
)

const (
	// DefaultWebView2BootstrapperURL is Microsoft's permanent redirect for the WebView2 Evergreen Bootstrapper.
	DefaultWebView2BootstrapperURL = "https://go.microsoft.com/fwlink/p/?LinkId=2124703"
	WebView2BootstrapperFileName   = "MicrosoftEdgeWebview2Setup.exe"
)

// EnsureWebView2Bootstrapper verifies that MicrosoftEdgeWebview2Setup.exe is present in targetDir,
// downloading it automatically from Microsoft if AutoDownloadWebview2 is enabled.
func EnsureWebView2Bootstrapper(cfg *config.Config, targetDir string) (string, error) {
	// If custom path is set in config it must exist; never silently fall back.
	if cfg.Windows.NSIS.Webview2Bootstrapper != "" {
		p := cfg.Windows.NSIS.Webview2Bootstrapper
		if !filepath.IsAbs(p) {
			p = filepath.Join(cfg.RootDir, p)
		}
		if !fileExists(p) {
			return "", fmt.Errorf("windows.nsis.webview2_bootstrapper is set to %q but no file exists at %s", cfg.Windows.NSIS.Webview2Bootstrapper, p)
		}
		return p, nil
	}

	targetFile := filepath.Join(targetDir, WebView2BootstrapperFileName)
	if fileExists(targetFile) {
		return targetFile, nil
	}

	// Auto-download if enabled
	if cfg.Windows.NSIS.AutoDownloadWebview2 {
		fmt.Printf("==> Downloading WebView2 Evergreen Bootstrapper to %s...\n", targetFile)
		client := &http.Client{Timeout: 60 * time.Second}
		resp, err := client.Get(DefaultWebView2BootstrapperURL)
		if err != nil {
			return "", fmt.Errorf("failed to download WebView2 bootstrapper: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("downloading WebView2 bootstrapper returned HTTP %d", resp.StatusCode)
		}

		if err := os.MkdirAll(targetDir, 0755); err != nil {
			return "", fmt.Errorf("failed to create directory for bootstrapper: %w", err)
		}

		// Download to a temp file and rename only after a complete write so an
		// interrupted download can never be cached as a valid bootstrapper.
		tmp, err := os.CreateTemp(targetDir, "."+WebView2BootstrapperFileName+".*.tmp")
		if err != nil {
			return "", fmt.Errorf("failed to create temp file for WebView2 bootstrapper: %w", err)
		}
		tmpName := tmp.Name()
		committed := false
		defer func() {
			if !committed {
				tmp.Close()
				os.Remove(tmpName)
			}
		}()

		written, err := io.Copy(tmp, resp.Body)
		if err != nil {
			return "", fmt.Errorf("failed to write WebView2 bootstrapper: %w", err)
		}
		if resp.ContentLength > 0 && written != resp.ContentLength {
			return "", fmt.Errorf("incomplete WebView2 bootstrapper download: wrote %d of %d bytes", written, resp.ContentLength)
		}
		if err := tmp.Sync(); err != nil {
			return "", fmt.Errorf("failed to flush WebView2 bootstrapper: %w", err)
		}
		if err := tmp.Close(); err != nil {
			return "", fmt.Errorf("failed to close WebView2 bootstrapper: %w", err)
		}
		if err := os.Chmod(tmpName, 0755); err != nil {
			return "", fmt.Errorf("failed to set permissions on WebView2 bootstrapper: %w", err)
		}
		if err := os.Rename(tmpName, targetFile); err != nil {
			return "", fmt.Errorf("failed to move WebView2 bootstrapper into place: %w", err)
		}
		committed = true
		return targetFile, nil
	}

	return "", nil
}

// BuildNSISDefines constructs all -D defines passed to makensis.
func BuildNSISDefines(cfg *config.Config, binaries map[string]string, arch string) ([]string, error) {
	if arch == "" {
		arch = "amd64"
	}

	// Numeric version suitable for NSIS VIProductVersion (rejects letters/hyphens).
	// NSIS requires exactly four components.
	numVer := resolvedWindowsVersion(cfg)

	projectName := cfg.Package.Name
	productName := cfg.Package.ProductName
	if productName == "" {
		productName = projectName
	}
	companyName := cfg.Package.Company
	if companyName == "" {
		companyName = "SimTX"
	}
	copyright := cfg.Package.Copyright
	if copyright == "" {
		copyright = fmt.Sprintf("© %s", companyName)
	}

	// Locate GUI binary
	guiBinPath := findGUIBinary(cfg, binaries)
	if guiBinPath == "" {
		return nil, fmt.Errorf("no GUI binary found for Windows NSIS packaging")
	}

	guiExeName := filepath.Base(guiBinPath)
	if !strings.HasSuffix(guiExeName, ".exe") {
		guiExeName += ".exe"
	}

	var defines []string
	def := func(key, value string) {
		defines = append(defines, fmt.Sprintf("-D%s=%s", key, value))
	}
	def("INFO_PROJECTNAME", projectName)
	def("INFO_COMPANYNAME", companyName)
	def("INFO_PRODUCTNAME", productName)
	def("INFO_PRODUCTVERSION", numVer)
	def("INFO_COPYRIGHT", copyright)
	def("PRODUCT_EXECUTABLE", guiExeName)

	// Scope handling: machine vs user
	installScope := strings.ToLower(cfg.Windows.NSIS.InstallScope)
	if installScope == "user" {
		def("WAILS_INSTALL_SCOPE", "user")
		def("REQUEST_EXECUTION_LEVEL", "user")
	} else {
		def("WAILS_INSTALL_SCOPE", "machine")
		def("REQUEST_EXECUTION_LEVEL", "admin")
	}

	// Architecture binary define: -DARG_WAILS_AMD64_BINARY or -DARG_WAILS_ARM64_BINARY
	if strings.ToLower(arch) == "arm64" {
		def("ARG_WAILS_ARM64_BINARY", guiBinPath)
	} else {
		def("ARG_WAILS_AMD64_BINARY", guiBinPath)
	}

	// Companion CLI binary: -DARG_WAILS_CLI_BINARY
	cliBinPath := findCLIBinary(cfg, binaries)
	if cliBinPath != "" {
		defines = append(defines, fmt.Sprintf("-DARG_WAILS_CLI_BINARY=%s", cliBinPath))
	}

	// WebView2 bootstrapper: expose the configured path so the NSIS script can
	// locate it even when it lives outside the script directory.
	if cfg.Windows.NSIS.Webview2Bootstrapper != "" {
		p := cfg.Windows.NSIS.Webview2Bootstrapper
		if !filepath.IsAbs(p) {
			p = filepath.Join(cfg.RootDir, p)
		}
		if !fileExists(p) {
			return nil, fmt.Errorf("windows.nsis.webview2_bootstrapper is set to %q but no file exists at %s", cfg.Windows.NSIS.Webview2Bootstrapper, p)
		}
		defines = append(defines,
			fmt.Sprintf("-DARG_WEBVIEW2_BOOTSTRAPPER=%s", p),
			fmt.Sprintf("-DWEBVIEW2_BOOTSTRAPPER=%s", p))
	}

	// User-defined extra defines from cook.toml
	for k, v := range cfg.Windows.NSIS.Defines {
		defines = append(defines, fmt.Sprintf("-D%s=%s", k, v))
	}

	return defines, nil
}

// BuildNSIS runs makensis with all parameters to generate the Windows installer.
func BuildNSIS(ctx context.Context, cfg *config.Config, arch string, binaries map[string]string) ([]string, error) {
	if arch == "" {
		arch = "amd64"
	}

	scriptPath := cfg.Windows.NSIS.Script
	if scriptPath == "" {
		scriptPath = filepath.Join(cfg.RootDir, "build", "windows", "nsis", "project.nsi")
	} else if !filepath.IsAbs(scriptPath) {
		scriptPath = filepath.Join(cfg.RootDir, scriptPath)
	}

	scriptDir := filepath.Dir(scriptPath)

	// Ensure the WebView2 bootstrapper is available and tell the script where
	// the configured (possibly external) bootstrapper lives.
	bootstrapperPath, err := EnsureWebView2Bootstrapper(cfg, scriptDir)
	if err != nil {
		return nil, err
	}

	defines, err := BuildNSISDefines(cfg, binaries, arch)
	if err != nil {
		return nil, err
	}
	if bootstrapperPath != "" {
		defines = append(defines,
			fmt.Sprintf("-DARG_WEBVIEW2_BOOTSTRAPPER=%s", bootstrapperPath),
			fmt.Sprintf("-DWEBVIEW2_BOOTSTRAPPER=%s", bootstrapperPath))
	}

	makensisPath, err := exec.LookPath("makensis")
	if err != nil {
		return nil, fmt.Errorf("makensis executable not found in PATH; required for NSIS packaging: %w", err)
	}

	outDir := filepath.Join(cfg.RootDir, cfg.Package.OutputDir)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create NSIS output dir %s: %w", outDir, err)
	}

	args := append(defines, scriptPath)
	runner := cookExec.NewRunner("nsis")
	fmt.Printf("==> Running makensis to build Windows installer...\n")
	start := time.Now()
	if err := runner.RunArgs(ctx, makensisPath, args, scriptDir, nil); err != nil {
		return nil, fmt.Errorf("NSIS compilation failed: %w", err)
	}

	installerName := fmt.Sprintf("%s-%s-installer.exe", cfg.Package.Name, arch)
	expectedOut := filepath.Join(outDir, installerName)

	// Verify a fresh installer was actually produced instead of returning a
	// guessed path (which may be stale or nonexistent).
	since := start.Add(-2 * time.Second)
	if isFreshFile(expectedOut, since) {
		return []string{expectedOut}, nil
	}

	var candidates []string
	for _, dir := range []string{outDir, scriptDir} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".exe") {
				continue
			}
			candidate := filepath.Join(dir, entry.Name())
			if candidate == expectedOut || !isFreshFile(candidate, since) {
				continue
			}
			candidates = append(candidates, candidate)
		}
	}

	if len(candidates) == 1 {
		return []string{candidates[0]}, nil
	}
	if len(candidates) > 1 {
		return nil, fmt.Errorf("NSIS build produced multiple new executables (expected %s): %s", expectedOut, strings.Join(candidates, ", "))
	}
	return nil, fmt.Errorf("NSIS build completed but no fresh installer was produced at %s", expectedOut)
}

// isFreshFile reports whether path is a regular file modified at or after since.
func isFreshFile(path string, since time.Time) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir() && !fi.ModTime().Before(since)
}

func findGUIBinary(cfg *config.Config, binaries map[string]string) string {
	// 1. Look for binary marked GUI in config
	for _, b := range cfg.Binaries {
		if b.GUI {
			if path := packager.LookupBinary(binaries, "", b.Name, true); path != "" {
				return path
			}
		}
	}

	// 2. Look for package name
	if path := packager.LookupBinary(binaries, "", cfg.Package.Name, true); path != "" {
		return path
	}

	// 3. Fallback to any binary that is not CLI, in deterministic order
	for _, name := range sortedBinaryNames(binaries) {
		if !strings.Contains(strings.ToLower(name), "cli") {
			if path := packager.LookupBinary(binaries, "", name, false); path != "" {
				return path
			}
		}
	}

	// 4. Fallback to first available binary, in deterministic order
	for _, name := range sortedBinaryNames(binaries) {
		if path := packager.LookupBinary(binaries, "", name, false); path != "" {
			return path
		}
	}

	return ""
}

func findCLIBinary(cfg *config.Config, binaries map[string]string) string {
	outDir := filepath.Join(cfg.RootDir, cfg.Package.OutputDir)

	// 1. Configured bundled CLI
	if cfg.Windows.BundledCLI != "" {
		return packager.LookupBinary(binaries, outDir, cfg.Windows.BundledCLI, true)
	}

	// 2. Look for any configured binary marked non-GUI
	for _, b := range cfg.Binaries {
		if !b.GUI {
			if path := packager.LookupBinary(binaries, outDir, b.Name, true); path != "" {
				return path
			}
		}
	}

	// 3. Fallback to any binary whose name contains "cli" (case-insensitive),
	// in deterministic order
	for _, name := range sortedBinaryNames(binaries) {
		if strings.Contains(strings.ToLower(name), "cli") {
			if path := packager.LookupBinary(binaries, "", name, false); path != "" {
				return path
			}
		}
	}

	// 4. Fallback to "cli"
	return packager.LookupBinary(binaries, outDir, "cli", true)
}

// sortedBinaryNames returns binary map keys in a stable order so fallback
// selection never depends on Go's randomized map iteration.
func sortedBinaryNames(binaries map[string]string) []string {
	names := make([]string, 0, len(binaries))
	for name := range binaries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
