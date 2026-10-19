package windows

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"go.digitalxero.dev/go-msix"

	"github.com/simtxng/cook/pkg/config"
)

// BuildMSIX generates a Windows MSIX package using pure-Go go-msix.
func BuildMSIX(ctx context.Context, cfg *config.Config, arch string, binaries map[string]string) ([]string, error) {
	if arch == "" {
		arch = "amd64"
	}
	msixArch := toMSIXArch(arch)

	guiBinPath := findGUIBinary(cfg, binaries)
	if guiBinPath == "" {
		return nil, fmt.Errorf("no GUI binary found for MSIX packaging")
	}

	guiExeName := filepath.Base(guiBinPath)
	if !strings.HasSuffix(guiExeName, ".exe") {
		guiExeName += ".exe"
	}

	// 1. Identity
	pkgName := cfg.Package.Identifier
	if pkgName == "" {
		pkgName = cfg.Package.Name
	}
	// Sanitize package name (only letters, numbers, dots, dashes)
	pkgName = sanitizePackageName(pkgName)
	if err := validatePackageName(pkgName); err != nil {
		return nil, err
	}

	fullNumericVersion := msixIdentityVersion(cfg)

	company := cfg.Package.Company
	if company == "" {
		company = "SimTX"
	}
	publisher := cfg.Windows.Signing.Publisher
	if publisher == "" {
		publisher = company
		if !strings.HasPrefix(publisher, "CN=") {
			publisher = "CN=" + publisher
		}
	}

	prodName := cfg.Package.ProductName
	if prodName == "" {
		prodName = cfg.Package.Name
	}
	desc := cfg.Package.Description
	if desc == "" {
		desc = prodName
	}

	builder := msix.NewBuilder().
		WithIdentity(msix.NewIdentity().
			WithName(pkgName).
			WithVersion(fullNumericVersion).
			WithPublisher(publisher).
			WithProcessorArchitecture(msixArch).
			Build()).
		WithProperties(msix.NewProperties().
			WithDisplayName(prodName).
			WithPublisherDisplayName(company).
			WithDescription(desc).
			WithLogo("Assets/StoreLogo.png").
			Build()).
		WithDependencies(msix.NewDependencies().
			AddTargetDeviceFamily("Windows.Desktop", "10.0.17763.0", "10.0.19041.0").
			Build()).
		AddResource(msix.NewResource().WithLanguage("en-us").Build()).
		WithCapabilities(msix.NewCapabilities().AddRestricted("runFullTrust").Build())

	// Application visual elements
	appID := sanitizeAppID(cfg.Package.Name)
	builder.AddApplication(msix.NewApplication().
		WithID(appID).
		WithExecutable(guiExeName).
		WithEntryPoint("Windows.FullTrustApplication").
		WithVisualElements(msix.NewVisualElements().
			WithDisplayName(prodName).
			WithDescription(desc).
			WithBackgroundColor("transparent").
			WithSquare150x150Logo("Assets/Square150x150Logo.png").
			WithSquare44x44Logo("Assets/Square44x44Logo.png").
			Build()).
		Build())

	// Add GUI binary file
	builder.AddFile(guiExeName, guiBinPath)

	// Add companion CLI binary if present
	cliBinPath := findCLIBinary(cfg, binaries)
	if cliBinPath != "" {
		cliExeName := filepath.Base(cliBinPath)
		if !strings.HasSuffix(cliExeName, ".exe") {
			cliExeName += ".exe"
		}
		builder.AddFile(cliExeName, cliBinPath)
	}

	// Assets: honor a configured image icon, otherwise fall back to generated
	// solid-color placeholder PNGs.
	iconPath, err := resolveIconPath(cfg)
	if err != nil {
		return nil, err
	}
	iconImg, err := loadMSIXIconImage(iconPath)
	if err != nil {
		return nil, err
	}

	logoColor := color.RGBA{R: 0x21, G: 0x96, B: 0xF3, A: 0xFF}
	logo150, err := msixLogoBytes(iconImg, 150, 150, logoColor)
	if err != nil {
		return nil, err
	}
	logo44, err := msixLogoBytes(iconImg, 44, 44, logoColor)
	if err != nil {
		return nil, err
	}
	logoStore, err := msixLogoBytes(iconImg, 50, 50, logoColor)
	if err != nil {
		return nil, err
	}

	builder.AddFileFromBytes("Assets/Square150x150Logo.png", logo150)
	builder.AddFileFromBytes("Assets/Square44x44Logo.png", logo44)
	builder.AddFileFromBytes("Assets/StoreLogo.png", logoStore)

	outDir := filepath.Join(cfg.RootDir, cfg.Package.OutputDir)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output dir for MSIX: %w", err)
	}

	msixFileName := fmt.Sprintf("%s-%s.msix", cfg.Package.Name, msixArch)
	outPath := filepath.Join(outDir, msixFileName)

	// Build into a temp file and publish only after a successful Build so a
	// failed package never leaves a corrupt partial .msix behind.
	tmp, err := os.CreateTemp(outDir, "."+msixFileName+".*.tmp")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp MSIX file for %s: %w", outPath, err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	fmt.Printf("==> Building pure-Go MSIX package -> %s\n", outPath)
	if err := builder.Build(ctx, tmp); err != nil {
		return nil, fmt.Errorf("failed to build MSIX package: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("failed to flush MSIX package %s: %w", outPath, err)
	}
	if err := replaceFile(tmpName, outPath); err != nil {
		return nil, fmt.Errorf("failed to publish MSIX package %s: %w", outPath, err)
	}
	committed = true

	return []string{outPath}, nil
}

// toMSIXArch maps Go architecture names to the MSIX processor architecture.
func toMSIXArch(arch string) string {
	switch strings.ToLower(arch) {
	case "arm64", "aarch64":
		return "arm64"
	case "386", "x86", "i386":
		return "x86"
	default:
		return "x64"
	}
}

func sanitizePackageName(name string) string {
	var sb strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('.')
		}
	}
	res := sb.String()
	for strings.Contains(res, "..") {
		res = strings.ReplaceAll(res, "..", ".")
	}
	res = strings.Trim(res, ".-")
	if res == "" {
		return "App"
	}
	return res
}

func validatePackageName(name string) error {
	if len(name) < 3 || len(name) > 50 {
		return fmt.Errorf("invalid MSIX package identifier %q: must be 3-50 characters", name)
	}
	for _, r := range name {
		valid := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-'
		if !valid {
			return fmt.Errorf("invalid MSIX package identifier %q: character %q is not allowed", name, r)
		}
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		return fmt.Errorf("invalid MSIX package identifier %q: must not start or end with a hyphen", name)
	}
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") {
		return fmt.Errorf("invalid MSIX package identifier %q: must not start or end with a period", name)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("invalid MSIX package identifier %q: must not contain consecutive periods", name)
	}
	return nil
}

func msixIdentityVersion(cfg *config.Config) string {
	v := config.VersionParts4(resolvedWindowsVersion(cfg))
	v[3] = 0
	return fmt.Sprintf("%d.%d.%d.%d", v[0], v[1], v[2], v[3])
}

func sanitizeAppID(name string) string {
	var sb strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		}
	}
	res := sb.String()
	if res == "" {
		return "App"
	}
	return res
}

func loadMSIXIconImage(iconPath string) (image.Image, error) {
	if iconPath == "" {
		return nil, nil
	}
	data, err := os.ReadFile(iconPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read configured icon %s: %w", iconPath, err)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		fmt.Printf("==> Warning: configured icon %s is not a decodable image; using generated MSIX logos\n", iconPath)
		return nil, nil
	}
	return img, nil
}

func msixLogoBytes(src image.Image, width, height int, fallback color.Color) ([]byte, error) {
	if src != nil {
		return encodePNGBytes(resizeNearest(src, width, height))
	}
	return generatePNGBytes(width, height, fallback)
}

func generatePNGBytes(width, height int, col color.Color) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: col}, image.Point{}, draw.Src)
	return encodePNGBytes(img)
}

func encodePNGBytes(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("failed to encode MSIX PNG asset: %w", err)
	}
	return buf.Bytes(), nil
}

func resizeNearest(src image.Image, width, height int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	b := src.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 {
		return dst
	}
	for y := 0; y < height; y++ {
		sy := b.Min.Y + y*b.Dy()/height
		for x := 0; x < width; x++ {
			sx := b.Min.X + x*b.Dx()/width
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}
