package linux

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/simtxng/cook/pkg/config"
	"howett.net/plist"
)

// defaultVoidDepends are the run-time dependencies used for a GTK4/WebKitGTK
// Wails application when [linux.void].depends is not configured.
var defaultVoidDepends = []string{"gtk4", "libwebkitgtk60", "hicolor-icon-theme"}

// voidPkgverRe matches a fully qualified XBPS pkgver ("name-version_revision").
var voidPkgverRe = regexp.MustCompile(`^[^<>*?\[\]]+-[0-9][^-]*_[0-9]+$`)

// normalizeVoidPkgSpec makes a dependency/conflict/replaces entry parseable by
// libxbps. libxbps rejects a bare package name ("can't guess pkgname") and only
// accepts either a fully qualified pkgver ("foo-1.2_1") or a pkgpattern with an
// operator ("foo>=1.2"). Bare names therefore get an unconstrained ">=0".
func normalizeVoidPkgSpec(spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" || strings.ContainsAny(spec, "><*?[]") || voidPkgverRe.MatchString(spec) {
		return spec
	}
	return spec + ">=0"
}

// normalizeVoidPkgSpecs normalizes a list of entries, dropping empty ones.
func normalizeVoidPkgSpecs(specs []string) []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, normalizeVoidPkgSpec(s))
	}
	return out
}

// xbpsProps mirrors the props.plist metadata written into every binary package.
// Field order is fixed so the generated plist is deterministic.
type xbpsProps struct {
	Architecture  string   `plist:"architecture"`
	Pkgname       string   `plist:"pkgname"`
	Version       string   `plist:"version"`
	Pkgver        string   `plist:"pkgver"`
	ShortDesc     string   `plist:"short_desc"`
	Homepage      string   `plist:"homepage,omitempty"`
	License       string   `plist:"license,omitempty"`
	Maintainer    string   `plist:"maintainer,omitempty"`
	LongDesc      string   `plist:"long_desc,omitempty"`
	SourcePkg     string   `plist:"sourcepkg,omitempty"`
	Tags          string   `plist:"tags,omitempty"`
	RunDepends    []string `plist:"run_depends,omitempty"`
	ConfFiles     []string `plist:"conf_files,omitempty"`
	Conflicts     []string `plist:"conflicts,omitempty"`
	Provides      []string `plist:"provides,omitempty"`
	Replaces      []string `plist:"replaces,omitempty"`
	InstalledSize uint64   `plist:"installed_size"`
}

// xbpsFileEntry is a single entry in one of the files.plist arrays.
type xbpsFileEntry struct {
	File    string `plist:"file"`
	SHA256  string `plist:"sha256,omitempty"`
	Target  string `plist:"target,omitempty"`
	Size    uint64 `plist:"size,omitempty"`
	Mutable *bool  `plist:"mutable,omitempty"`
}

// xbpsFiles mirrors files.plist: the four arrays libxbps consults when
// installing, upgrading and removing a package.
type xbpsFiles struct {
	Files     []xbpsFileEntry `plist:"files,omitempty"`
	ConfFiles []xbpsFileEntry `plist:"conf_files,omitempty"`
	Links     []xbpsFileEntry `plist:"links,omitempty"`
	Dirs      []xbpsFileEntry `plist:"dirs,omitempty"`
}

// stagedFile is a payload file ready to be written into the archive.
type stagedFile struct {
	dest   string // absolute install path, e.g. /usr/bin/foo
	source string // staged source path on disk
	mode   int64
}

// voidArch maps Go architecture names to the XBPS architecture names.
func voidArch(goArch string) string {
	switch goArch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	case "386":
		return "i686"
	case "arm":
		return "armv7l"
	case "riscv64":
		return "riscv64"
	default:
		return goArch
	}
}

// voidPkgver builds the XBPS "<name>-<version>_<revision>" tuple. The version
// must not contain a hyphen (name/version separator) or underscore (revision
// separator); such a version cannot round-trip through xbps_pkg_version, so it
// is rejected rather than silently emitted.
func voidPkgver(cfg *config.Config, version string, revision int) (string, error) {
	if strings.ContainsAny(version, "-_") {
		return "", fmt.Errorf("version %q is invalid for Void: it must not contain '-' or '_'", version)
	}
	if version == "" {
		return "", fmt.Errorf("version is empty")
	}
	return fmt.Sprintf("%s-%s_%d", cfg.Package.Name, version, revision), nil
}

// PackageXBPS builds a Void Linux (.xbps) binary package in pure Go: a
// zstd-compressed, pax-restricted tar archive containing ./props.plist,
// ./files.plist and the staged payload, matching the layout produced by
// xbps-create(1). Package signing and repository indexing (xbps-rindex) are
// deliberately out of scope.
func PackageXBPS(ctx context.Context, cfg *config.Config, arch string, binaries map[string]string) ([]string, error) {
	if arch == "" {
		arch = runtime.GOARCH
	}

	version := cfg.ResolvedVersion().Void
	if version == "" {
		version = config.FormatVoidVersion(cfg.ResolvedVersion().Raw)
	}
	pkgver, err := voidPkgver(cfg, version, cfg.Linux.Void.EffectiveRevision())
	if err != nil {
		return nil, err
	}

	outDir := filepath.Join(cfg.RootDir, cfg.Package.OutputDir)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory %s: %w", outDir, err)
	}
	pkgPath := filepath.Join(outDir, fmt.Sprintf("%s.%s.xbps", pkgver, voidArch(arch)))

	tempDir, err := os.MkdirTemp("", "cook-xbps-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir for xbps staging: %w", err)
	}
	defer os.RemoveAll(tempDir)

	staged, err := stageVoidPayload(cfg, binaries, tempDir)
	if err != nil {
		return nil, err
	}

	props, filesPlist, err := buildVoidMetadata(cfg, pkgver, version, voidArch(arch), staged)
	if err != nil {
		return nil, err
	}

	// INSTALL/REMOVE maintainer scripts are optional; unlike Debian, Void
	// handles desktop/mime/icon cache refreshes through xbps triggers.
	installScript, err := voidScript(cfg.Linux.Void.InstallScript, cfg.RootDir)
	if err != nil {
		return nil, err
	}
	removeScript, err := voidScript(cfg.Linux.Void.RemoveScript, cfg.RootDir)
	if err != nil {
		return nil, err
	}

	if err := writeVoidArchive(pkgPath, props, filesPlist, staged, installScript, removeScript); err != nil {
		return nil, err
	}

	return []string{pkgPath}, nil
}

// voidScript resolves an optional INSTALL/REMOVE script. A configured script
// that cannot be read is an error, not a silent omission.
func voidScript(path, rootDir string) (string, error) {
	if path == "" {
		return "", nil
	}
	resolved := resolveFile(path, rootDir)
	if resolved == "" {
		return "", fmt.Errorf("configured Void maintainer script %q does not exist or is not a readable file", path)
	}
	return resolved, nil
}

// stageVoidPayload resolves the GUI binary, companion CLI, desktop file and
// icon into their install locations, mirroring the deb/rpm/arch payload.
func stageVoidPayload(cfg *config.Config, binaries map[string]string, tempDir string) ([]stagedFile, error) {
	outDir := filepath.Join(cfg.RootDir, cfg.Package.OutputDir)
	var staged []stagedFile

	if guiBinPath := findBinary(cfg.Package.Name, binaries, outDir); guiBinPath != "" {
		staged = append(staged, stagedFile{
			dest:   fmt.Sprintf("/usr/bin/%s", cfg.Package.Name),
			source: guiBinPath,
			mode:   0755,
		})
	}

	if cliName, cliBinPath := selectCLIBinary(cfg, binaries, outDir); cliBinPath != "" {
		staged = append(staged, stagedFile{
			dest:   fmt.Sprintf("/usr/bin/%s", cliName),
			source: cliBinPath,
			mode:   0755,
		})
	}

	desktopPath := filepath.Join(tempDir, fmt.Sprintf("%s.desktop", cfg.Package.Name))
	actualDesktop, err := EnsureDesktopFile(cfg, desktopPath)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare desktop file: %w", err)
	}
	if actualDesktop != "" {
		staged = append(staged, stagedFile{
			dest:   fmt.Sprintf("/usr/share/applications/%s.desktop", cfg.Package.Name),
			source: actualDesktop,
			mode:   0644,
		})
	}

	iconSrc := resolveFile(cfg.Package.Icon, cfg.RootDir)
	if iconSrc != "" {
		if !isPNGIcon(iconSrc) {
			fmt.Fprintf(os.Stderr, "WARNING: icon %s is not a PNG; installing a generated placeholder as %s.png (no icon converter available).\n", iconSrc, cfg.Package.Name)
			iconSrc = filepath.Join(tempDir, fmt.Sprintf("%s.png", cfg.Package.Name))
			if err := createFallbackPNG(iconSrc); err != nil {
				return nil, fmt.Errorf("failed to create fallback icon: %w", err)
			}
		}
		staged = append(staged, stagedFile{
			dest:   fmt.Sprintf("/usr/share/icons/hicolor/128x128/apps/%s.png", cfg.Package.Name),
			source: iconSrc,
			mode:   0644,
		})
	}

	return staged, nil
}

// buildVoidMetadata produces props.plist and files.plist for staged files.
func buildVoidMetadata(cfg *config.Config, pkgver, version, arch string, staged []stagedFile) (*xbpsProps, *xbpsFiles, error) {
	depends := firstNonEmptyList(cfg.Linux.Void.Depends, defaultVoidDepends)

	props := &xbpsProps{
		Architecture: arch,
		Pkgname:      cfg.Package.Name,
		Version:      version,
		Pkgver:       pkgver,
		ShortDesc:    cfg.Package.Description,
		Homepage:     cfg.Package.Homepage,
		License:      cfg.Package.License,
		Maintainer:   cfg.Package.Maintainer,
		Tags:         cfg.Linux.Void.Tags,
		RunDepends:   normalizeVoidPkgSpecs(depends),
		Conflicts:    normalizeVoidPkgSpecs(cfg.Linux.Void.Conflicts),
		Provides:     cfg.Linux.Void.Provides,
		Replaces:     normalizeVoidPkgSpecs(cfg.Linux.Void.Replaces),
	}
	if props.Maintainer == "" {
		props.Maintainer = cfg.Package.Company
	}
	if props.ShortDesc == "" {
		props.ShortDesc = cfg.Package.Name
	}

	files := &xbpsFiles{}
	dirSet := map[string]bool{}
	mutableSet := normalizePathSet(cfg.Linux.Void.MutableFiles)
	confSet := normalizePathSet(cfg.Linux.Void.ConfFiles)

	for _, sf := range staged {
		sum, size, err := fileSHA256(sf.source)
		if err != nil {
			return nil, nil, err
		}
		props.InstalledSize += size

		entry := xbpsFileEntry{File: sf.dest, SHA256: sum, Size: size}
		if mutableSet[sf.dest] {
			mutable := true
			entry.Mutable = &mutable
		}

		if confSet[sf.dest] {
			files.ConfFiles = append(files.ConfFiles, entry)
			props.ConfFiles = append(props.ConfFiles, sf.dest)
		} else {
			files.Files = append(files.Files, entry)
		}

		for _, dir := range parentDirs(sf.dest) {
			dirSet[dir] = true
		}
	}

	// Include ordered parent directories the same way xbps-create records the
	// staged destdir tree.
	dirs := make([]string, 0, len(dirSet))
	for dir := range dirSet {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		files.Dirs = append(files.Dirs, xbpsFileEntry{File: dir})
	}

	return props, files, nil
}

// normalizePathSet returns a set of absolute paths, accepting configured
// entries written with or without a leading slash.
func normalizePathSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if !strings.HasPrefix(v, "/") {
			v = "/" + v
		}
		set[filepath.Clean(v)] = true
	}
	return set
}

// parentDirs returns every ancestor directory of an absolute file path down to
// (but excluding) "/".
func parentDirs(path string) []string {
	var dirs []string
	for dir := filepath.Dir(path); dir != "/" && dir != "." && dir != ""; dir = filepath.Dir(dir) {
		dirs = append(dirs, dir)
	}
	return dirs
}

func fileSHA256(path string) (string, uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("failed to open payload file %s: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, fmt.Errorf("failed to hash payload file %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), uint64(n), nil
}

// writeVoidArchive writes the zstd-compressed pax-restricted tar archive.
func writeVoidArchive(path string, props *xbpsProps, filesPlist *xbpsFiles, staged []stagedFile, installScript, removeScript string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create package file %s: %w", path, err)
	}
	defer f.Close()

	zw, err := zstd.NewWriter(f, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	if err != nil {
		return fmt.Errorf("failed to create zstd writer: %w", err)
	}
	tw := tar.NewWriter(zw)

	writeErr := func() error {
		// Maintainer scripts first, when configured.
		for _, s := range []struct{ name, source string }{
			{"./INSTALL", installScript},
			{"./REMOVE", removeScript},
		} {
			if s.source == "" {
				continue
			}
			if err := writeMetadataFile(tw, s.name, s.source, 0755); err != nil {
				return err
			}
		}

		propsXML, err := plist.Marshal(props, plist.XMLFormat)
		if err != nil {
			return fmt.Errorf("failed to encode props.plist: %w", err)
		}
		if err := writeArchiveBytes(tw, "./props.plist", propsXML, 0644); err != nil {
			return err
		}

		filesXML, err := plist.Marshal(filesPlist, plist.XMLFormat)
		if err != nil {
			return fmt.Errorf("failed to encode files.plist: %w", err)
		}
		if err := writeArchiveBytes(tw, "./files.plist", filesXML, 0644); err != nil {
			return err
		}

		for _, sf := range staged {
			if err := writeArchiveFile(tw, "./"+strings.TrimPrefix(sf.dest, "/"), sf.source, sf.mode); err != nil {
				return err
			}
		}
		return nil
	}()

	if writeErr != nil {
		tw.Close()
		zw.Close()
		os.Remove(path)
		return writeErr
	}

	if err := tw.Close(); err != nil {
		zw.Close()
		return fmt.Errorf("failed to close tar archive: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("failed to close zstd stream: %w", err)
	}
	return nil
}

func writeMetadataFile(tw *tar.Writer, name, source string, mode int64) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("failed to read maintainer script %s: %w", source, err)
	}
	return writeArchiveBytes(tw, name, data, mode)
}

func writeArchiveBytes(tw *tar.Writer, name string, data []byte, mode int64) error {
	hdr := &tar.Header{
		Name:     name,
		Mode:     mode,
		Size:     int64(len(data)),
		Typeflag: tar.TypeReg,
		ModTime:  time.Unix(0, 0),
		Uname:    "root",
		Gname:    "root",
		Format:   tar.FormatPAX,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("failed to write tar header %s: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("failed to write tar contents %s: %w", name, err)
	}
	return nil
}

func writeArchiveFile(tw *tar.Writer, name, source string, mode int64) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("failed to read payload file %s: %w", source, err)
	}
	return writeArchiveBytes(tw, name, data, mode)
}
