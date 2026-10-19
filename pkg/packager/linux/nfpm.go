package linux

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/goreleaser/nfpm/v2"
	_ "github.com/goreleaser/nfpm/v2/arch"
	_ "github.com/goreleaser/nfpm/v2/deb"
	"github.com/goreleaser/nfpm/v2/files"
	_ "github.com/goreleaser/nfpm/v2/rpm"
	"github.com/simtxng/cook/pkg/config"
	"github.com/simtxng/cook/pkg/packager"
)

// defaultMaintainerScript is installed as the deb postinstall and postremove
// hook: both refresh the desktop, MIME and icon caches after files change.
const defaultMaintainerScript = `#!/bin/sh
set -e

# Update desktop database for .desktop file changes
if command -v update-desktop-database >/dev/null 2>&1; then
  update-desktop-database -q /usr/share/applications || true
fi

# Update MIME database for custom URL schemes
if command -v update-mime-database >/dev/null 2>&1; then
  update-mime-database -n /usr/share/mime || true
fi

# Refresh the icon cache for installed hicolor icon
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
  gtk-update-icon-cache -q -t -f /usr/share/icons/hicolor || true
fi

exit 0
`

// nfpmTarget describes the per-ecosystem differences for nfpm: the resolved
// version field that is authoritative and the metadata/dependency overrides
// that deb, rpm and archlinux each require.
type nfpmTarget struct {
	version func(*config.VersionInfo) string
	apply   func(*nfpm.Info, *config.Config)
}

var nfpmTargets = map[string]nfpmTarget{
	"deb": {
		version: func(v *config.VersionInfo) string { return v.Debian },
		apply: func(info *nfpm.Info, cfg *config.Config) {
			// Debian-only metadata. RPM/archlinux must not inherit these fields.
			info.Section = cfg.Linux.Deb.Section
			info.Priority = firstNonEmptyString(cfg.Linux.Deb.Priority, "optional")
			info.Depends = firstNonEmptyList(cfg.Linux.Deb.Depends, []string{"libgtk-4-1", "libwebkitgtk-6.0-4"})
		},
	},
	"rpm": {
		version: func(v *config.VersionInfo) string { return v.RPM },
		apply: func(info *nfpm.Info, cfg *config.Config) {
			info.Depends = firstNonEmptyList(cfg.Linux.RPM.Depends, []string{"gtk4", "webkitgtk6.0"})
			info.RPM.Group = firstNonEmptyString(cfg.Linux.RPM.Group, "Applications/Communications")
			info.RPM.Summary = firstNonEmptyString(cfg.Package.ProductName, cfg.Package.Name)
			applyRPMSigning(info, cfg)
		},
	},
	"archlinux": {
		version: func(v *config.VersionInfo) string { return v.Arch },
		apply: func(info *nfpm.Info, cfg *config.Config) {
			info.Depends = firstNonEmptyList(cfg.Linux.Arch.Depends, []string{"gtk4", "webkitgtk-6.0", "hicolor-icon-theme"})
			info.ArchLinux.Pkgbase = firstNonEmptyString(cfg.Linux.Arch.PkgBase, cfg.Package.Name)
		},
	},
}

// nfpmVersion selects the resolved version for a target. An empty
// target-specific field falls back to Raw so a partially populated VersionInfo
// never produces an empty package version.
func nfpmVersion(spec nfpmTarget, v *config.VersionInfo) string {
	if ver := spec.version(v); ver != "" {
		return ver
	}
	return v.Raw
}

// applyRPMSigning configures RPM GPG signing when a key is configured.
func applyRPMSigning(info *nfpm.Info, cfg *config.Config) {
	if cfg.Linux.RPM.GPGKey == "" {
		return
	}
	info.RPM.Signature.KeyFile = config.ResolvePath(cfg.RootDir, cfg.Linux.RPM.GPGKey)
	if cfg.Linux.RPM.GPGName != "" {
		keyID := cfg.Linux.RPM.GPGName
		info.RPM.Signature.KeyID = &keyID
	}
	info.RPM.Signature.KeyPassphrase = firstNonEmptyString(
		os.Getenv("RPM_GPG_PASSPHRASE"), os.Getenv("GPG_PASSPHRASE"))
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNonEmptyList(values ...[]string) []string {
	for _, v := range values {
		if len(v) > 0 {
			return v
		}
	}
	return nil
}

// BuildNFPMInfo creates an nfpm.Info structure configured for the target format (deb, rpm, archlinux).
// Returns the Info object, a cleanup function to remove temporary staging files, and any error.
func BuildNFPMInfo(cfg *config.Config, target, arch string, binaries map[string]string) (*nfpm.Info, func(), error) {
	tempDir, err := os.MkdirTemp("", "cook-nfpm-*")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create temp dir for nfpm staging: %w", err)
	}
	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}

	info := &nfpm.Info{
		Name:        cfg.Package.Name,
		Arch:        arch,
		Platform:    "linux",
		Maintainer:  cfg.Package.Maintainer,
		Description: cfg.Package.Description,
		Vendor:      cfg.Package.Company,
		Homepage:    cfg.Package.Homepage,
		License:     cfg.Package.License,
		Release:     "1",
	}

	if info.Vendor == "" {
		info.Vendor = cfg.Package.Name
	}

	// EVR version translation and per-ecosystem metadata, resolved centrally so
	// the target rules live in one table instead of parallel switches.
	version := cfg.ResolvedVersion()
	if spec, ok := nfpmTargets[target]; ok {
		info.Version = nfpmVersion(spec, version)
		spec.apply(info, cfg)
	} else {
		info.Version = version.Raw
	}

	// Content mappings
	outDir := filepath.Join(cfg.RootDir, cfg.Package.OutputDir)

	// 1. GUI Binary -> /usr/bin/<name>
	guiBinPath := findBinary(cfg.Package.Name, binaries, outDir)
	if guiBinPath != "" {
		info.Contents = append(info.Contents, &files.Content{
			Source:      guiBinPath,
			Destination: fmt.Sprintf("/usr/bin/%s", cfg.Package.Name),
			FileInfo: &files.ContentFileInfo{
				Mode: 0755,
			},
		})
	}

	// 2. CLI Binary -> /usr/bin/<cli-name>
	cliName, cliBinPath := selectCLIBinary(cfg, binaries, outDir)
	if cliBinPath != "" {
		info.Contents = append(info.Contents, &files.Content{
			Source:      cliBinPath,
			Destination: fmt.Sprintf("/usr/bin/%s", cliName),
			FileInfo: &files.ContentFileInfo{
				Mode: 0755,
			},
		})
	}

	// 3. Desktop file -> /usr/share/applications/<name>.desktop
	desktopPath := filepath.Join(tempDir, fmt.Sprintf("%s.desktop", cfg.Package.Name))
	actualDesktop, err := EnsureDesktopFile(cfg, desktopPath)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("failed to prepare desktop file: %w", err)
	}
	if actualDesktop != "" {
		info.Contents = append(info.Contents, &files.Content{
			Source:      actualDesktop,
			Destination: fmt.Sprintf("/usr/share/applications/%s.desktop", cfg.Package.Name),
			FileInfo: &files.ContentFileInfo{
				Mode: 0644,
			},
		})
	}

	// 4. Icon -> /usr/share/icons/hicolor/128x128/apps/<name>.png
	iconSrc := resolveFile(cfg.Package.Icon, cfg.RootDir)
	if iconSrc != "" {
		if !isPNGIcon(iconSrc) {
			fmt.Fprintf(os.Stderr, "WARNING: icon %s is not a PNG; installing a generated placeholder as %s.png (no icon converter available).\n", iconSrc, cfg.Package.Name)
			iconSrc = filepath.Join(tempDir, fmt.Sprintf("%s.png", cfg.Package.Name))
			if err := createFallbackPNG(iconSrc); err != nil {
				cleanup()
				return nil, nil, fmt.Errorf("failed to create fallback icon: %w", err)
			}
		}
		info.Contents = append(info.Contents, &files.Content{
			Source:      iconSrc,
			Destination: fmt.Sprintf("/usr/share/icons/hicolor/128x128/apps/%s.png", cfg.Package.Name),
			FileInfo: &files.ContentFileInfo{
				Mode: 0644,
			},
		})
	}

	// Maintainer scripts are Debian-specific. They must never be attached to
	// rpm or archlinux packages, which have different hook semantics and paths.
	if target == "deb" {
		postinstallSrc := ""
		if cfg.Linux.Deb.Postinstall != "" {
			postinstallSrc = resolveFile(cfg.Linux.Deb.Postinstall, cfg.RootDir)
			if postinstallSrc == "" {
				cleanup()
				return nil, nil, fmt.Errorf("configured linux.deb.postinstall %q does not exist or is not a readable file", cfg.Linux.Deb.Postinstall)
			}
		} else {
			postinstallSrc = filepath.Join(tempDir, "postinstall.sh")
			if err := os.WriteFile(postinstallSrc, []byte(defaultMaintainerScript), 0755); err != nil {
				cleanup()
				return nil, nil, fmt.Errorf("failed to create default postinstall script: %w", err)
			}
		}
		info.Scripts.PostInstall = postinstallSrc

		if cfg.Linux.Deb.Prerm != "" {
			prermSrc := resolveFile(cfg.Linux.Deb.Prerm, cfg.RootDir)
			if prermSrc == "" {
				cleanup()
				return nil, nil, fmt.Errorf("configured linux.deb.prerm %q does not exist or is not a readable file", cfg.Linux.Deb.Prerm)
			}
			info.Scripts.PreRemove = prermSrc
		}

		postrmSrc := ""
		if cfg.Linux.Deb.Postrm != "" {
			postrmSrc = resolveFile(cfg.Linux.Deb.Postrm, cfg.RootDir)
			if postrmSrc == "" {
				cleanup()
				return nil, nil, fmt.Errorf("configured linux.deb.postrm %q does not exist or is not a readable file", cfg.Linux.Deb.Postrm)
			}
		} else {
			postrmSrc = filepath.Join(tempDir, "postremove.sh")
			if err := os.WriteFile(postrmSrc, []byte(defaultMaintainerScript), 0755); err != nil {
				cleanup()
				return nil, nil, fmt.Errorf("failed to create default postremove script: %w", err)
			}
		}
		info.Scripts.PostRemove = postrmSrc
	}

	return info, cleanup, nil
}

// PackageNFPM builds a deb, rpm, or archlinux package using nfpm in pure Go.
func PackageNFPM(ctx context.Context, cfg *config.Config, target, arch string, binaries map[string]string) ([]string, error) {
	pkgr, err := nfpm.Get(target)
	if err != nil {
		return nil, fmt.Errorf("failed to get packager for %s: %w", target, err)
	}

	info, cleanup, err := BuildNFPMInfo(cfg, target, arch, binaries)
	if err != nil {
		return nil, fmt.Errorf("failed to configure %s package: %w", target, err)
	}
	defer cleanup()

	info = nfpm.WithDefaults(info)

	fileName := pkgr.ConventionalFileName(info)
	outDir := filepath.Join(cfg.RootDir, cfg.Package.OutputDir)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory %s: %w", outDir, err)
	}

	pkgPath := filepath.Join(outDir, fileName)
	f, err := os.Create(pkgPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create package file %s: %w", pkgPath, err)
	}
	defer f.Close()

	if err := pkgr.Package(info, f); err != nil {
		return nil, fmt.Errorf("failed to pack %s package %s: %w", target, pkgPath, err)
	}

	artifacts := []string{pkgPath}

	// Arch Linux detached GPG signing (.sig sidecar)
	if target == "archlinux" && cfg.Linux.Arch.Sign {
		keyPath, keyID := archSigningKey(cfg)
		sigPath, err := SignArchPackage(pkgPath, keyPath, keyID)
		if err != nil {
			// A package with a missing/failed signature is not a usable
			// artifact; do not hand callers a partial success.
			return nil, fmt.Errorf("failed to sign archlinux package %s: %w", pkgPath, err)
		}
		artifacts = append(artifacts, sigPath)
	}

	return artifacts, nil
}

// archSigningKey returns the key file and key ID used to sign archlinux
// packages. The arch-specific [linux.archlinux] gpg_key/gpg_name settings take
// precedence. For backward compatibility, an unset arch field falls back to
// the corresponding [linux.rpm] setting, because older cook versions silently
// reused the RPM signing key for Arch packages.
func archSigningKey(cfg *config.Config) (string, string) {
	keyPath := cfg.Linux.Arch.GPGKey
	keyID := cfg.Linux.Arch.GPGName
	if keyPath == "" {
		keyPath = cfg.Linux.RPM.GPGKey
	}
	if keyID == "" {
		keyID = cfg.Linux.RPM.GPGName
	}
	keyPath = config.ResolvePath(cfg.RootDir, keyPath)
	return keyPath, keyID
}

// archGPGPassphrase returns the passphrase for Arch package signing. The
// arch-specific variable wins, followed by the historical RPM variable and
// finally the generic GPG variable.
func archGPGPassphrase() string {
	if pass := os.Getenv("ARCH_GPG_PASSPHRASE"); pass != "" {
		return pass
	}
	if pass := os.Getenv("RPM_GPG_PASSPHRASE"); pass != "" {
		return pass
	}
	return os.Getenv("GPG_PASSPHRASE")
}

// SignArchPackage creates a detached PGP signature (.sig sidecar) for an Arch Linux package.
func SignArchPackage(packagePath string, keyFile string, keyID string) (string, error) {
	sigPath := packagePath + ".sig"

	// 1. Sign with the configured key file. A configured key is authoritative:
	// if it cannot be read, parsed, or decrypted we fail rather than silently
	// falling through to the user's default gpg key.
	if keyFile != "" {
		data, err := os.ReadFile(keyFile)
		if err != nil {
			return "", fmt.Errorf("cannot read configured GPG key %s: %w", keyFile, err)
		}

		entityList, pErr := openpgp.ReadArmoredKeyRing(bytes.NewReader(data))
		if pErr != nil {
			entityList, pErr = openpgp.ReadKeyRing(bytes.NewReader(data))
		}
		if pErr != nil {
			return "", fmt.Errorf("cannot parse configured GPG key %s: %w", keyFile, pErr)
		}
		if len(entityList) == 0 {
			return "", fmt.Errorf("configured GPG key %s contains no keys", keyFile)
		}

		signer, err := selectSigningEntity(entityList, keyID)
		if err != nil {
			return "", fmt.Errorf("configured GPG key %s: %w", keyFile, err)
		}

		if err := decryptSigningEntity(signer, archGPGPassphrase()); err != nil {
			return "", fmt.Errorf("configured GPG key %s: %w", keyFile, err)
		}

		pkgFile, err := os.Open(packagePath)
		if err != nil {
			return "", err
		}
		defer pkgFile.Close()

		sigFile, err := os.Create(sigPath)
		if err != nil {
			return "", err
		}
		defer sigFile.Close()

		if err := openpgp.DetachSign(sigFile, signer, pkgFile, nil); err != nil {
			_ = os.Remove(sigPath)
			return "", fmt.Errorf("failed to sign package with configured key: %w", err)
		}
		return sigPath, nil
	}

	// 2. Fall back to the gpg command line when no key file is configured. If
	// keyID is set, gpg is pinned to that key; otherwise it may use the user's
	// default key.
	if gpgPath, err := exec.LookPath("gpg"); err == nil {
		args := []string{"--batch", "--yes", "--detach-sign", "--no-armor", "--output", sigPath}
		if keyID != "" {
			args = append(args, "--default-key", keyID)
		}
		passphrase := archGPGPassphrase()
		if passphrase != "" {
			args = append(args, "--pinentry-mode", "loopback", "--passphrase-fd", "0")
		}
		args = append(args, packagePath)

		cmd := exec.Command(gpgPath, args...)
		if passphrase != "" {
			cmd.Stdin = strings.NewReader(passphrase + "\n")
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("gpg command failed: %s: %w", strings.TrimSpace(string(out)), err)
		}
		return sigPath, nil
	}

	return "", fmt.Errorf("cannot sign package: neither valid GPG key file nor gpg executable available")
}

// selectSigningEntity picks the private key (primary or subkey) matching keyID.
// An empty keyID selects the first entity that has a private key.
func selectSigningEntity(entityList openpgp.EntityList, keyID string) (*openpgp.Entity, error) {
	wanted := strings.ToLower(strings.TrimPrefix(keyID, "0x"))
	for _, entity := range entityList {
		if entity.PrivateKey == nil || entity.PrivateKey.Dummy() {
			continue
		}
		if wanted == "" || entityMatchesKeyID(entity, wanted) {
			return entity, nil
		}
	}
	if keyID == "" {
		return nil, fmt.Errorf("contains no private key (a public-only key cannot sign)")
	}
	return nil, fmt.Errorf("no private key matching %q", keyID)
}

func entityMatchesKeyID(entity *openpgp.Entity, wanted string) bool {
	if entity.PrimaryKey != nil {
		if strings.HasSuffix(strings.ToLower(entity.PrimaryKey.KeyIdString()), wanted) {
			return true
		}
	}
	for _, subkey := range entity.Subkeys {
		if subkey.PublicKey != nil && strings.HasSuffix(strings.ToLower(subkey.PublicKey.KeyIdString()), wanted) {
			return true
		}
	}
	return false
}

// decryptSigningEntity decrypts the primary key and any encrypted signing
// subkeys. It returns a clear error if a passphrase is required but absent, or
// if decryption fails.
func decryptSigningEntity(entity *openpgp.Entity, passphrase string) error {
	encrypted := func() bool {
		if entity.PrivateKey != nil && entity.PrivateKey.Encrypted {
			return true
		}
		for _, subkey := range entity.Subkeys {
			if subkey.PrivateKey != nil && subkey.PrivateKey.Encrypted {
				return true
			}
		}
		return false
	}()

	if !encrypted {
		return nil
	}
	if passphrase == "" {
		return fmt.Errorf("private key is encrypted but ARCH_GPG_PASSPHRASE (or RPM_GPG_PASSPHRASE/GPG_PASSPHRASE) is not set")
	}
	if entity.PrivateKey != nil && entity.PrivateKey.Encrypted {
		if err := entity.PrivateKey.Decrypt([]byte(passphrase)); err != nil {
			return fmt.Errorf("failed to decrypt private key: %w", err)
		}
	}
	for i := range entity.Subkeys {
		if entity.Subkeys[i].PrivateKey != nil && entity.Subkeys[i].PrivateKey.Encrypted {
			if err := entity.Subkeys[i].PrivateKey.Decrypt([]byte(passphrase)); err != nil {
				return fmt.Errorf("failed to decrypt signing subkey: %w", err)
			}
		}
	}
	return nil
}

func findBinary(name string, binaries map[string]string, outDir string) string {
	return packager.LookupBinary(binaries, outDir, name, false)
}

func resolvePath(path, rootDir string) string {
	return config.ResolvePath(rootDir, path)
}

func resolveFile(path, rootDir string) string {
	resolved := config.ResolvePath(rootDir, path)
	if resolved == "" {
		return ""
	}
	if fi, err := os.Stat(resolved); err == nil && !fi.IsDir() {
		return resolved
	}
	return ""
}
