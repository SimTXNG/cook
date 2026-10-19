package darwin

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/simtxng/cook/pkg/config"
	cookExec "github.com/simtxng/cook/pkg/exec"
	"github.com/simtxng/cook/pkg/fsutil"
)

func TestResolveDMGOptions(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "cook.toml")
	toml := "[package]\nname = \"simtx\"\nproduct_name = \"SimTX\"\n\n[[binaries]]\nname = \"simtx\"\n"
	if err := os.WriteFile(configPath, []byte(toml), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	opts := ResolveDMGOptions(cfg)

	if opts.VolumeName != "SimTX" {
		t.Errorf("expected VolumeName=SimTX, got %s", opts.VolumeName)
	}
	if opts.WindowWidth != 540 {
		t.Errorf("expected WindowWidth=540, got %d", opts.WindowWidth)
	}
	if opts.WindowHeight != 380 {
		t.Errorf("expected WindowHeight=380, got %d", opts.WindowHeight)
	}
	if !slices.Equal(opts.AppPosition, []int{135, 190}) {
		t.Errorf("expected AppPosition=(135, 190), got %v", opts.AppPosition)
	}
	if !slices.Equal(opts.ApplicationsPosition, []int{405, 190}) {
		t.Errorf("expected ApplicationsPosition=(405, 190), got %v", opts.ApplicationsPosition)
	}
}

func TestResolveDMGOptionsPreservesPositions(t *testing.T) {
	// Explicit zero coordinates must survive ResolveDMGOptions: presence, not
	// value, drives the fallback, which now lives in BuildCreateDMGArgs.
	cfg := &config.Config{
		Package: config.PackageConfig{Name: "simtx"},
		Darwin: config.DarwinConfig{
			DMG: config.DarwinDMGConfig{
				AppPosition:          []int{0, 190},
				ApplicationsPosition: []int{0, 0},
			},
		},
		RootDir: "/path/to/project",
	}

	opts := ResolveDMGOptions(cfg)

	if !slices.Equal(opts.AppPosition, []int{0, 190}) {
		t.Errorf("expected explicit AppPosition (0, 190) to be honored, got %v", opts.AppPosition)
	}
	if !slices.Equal(opts.ApplicationsPosition, []int{0, 0}) {
		t.Errorf("expected explicit ApplicationsPosition (0, 0) to be honored, got %v", opts.ApplicationsPosition)
	}
}

func TestBuildCreateDMGArgsPositionFallback(t *testing.T) {
	// An absent or short position slice falls back to the defaults at the args
	// boundary, while explicit zero coordinates are honored.
	fallbackArgs := strings.Join(BuildCreateDMGArgs(DMGOptions{VolumeName: "SimTX"}, "/staging/SimTX.app", "/out/SimTX.dmg"), " ")
	if !strings.Contains(fallbackArgs, "--icon SimTX.app 135 190") {
		t.Errorf("expected default app icon position, got %s", fallbackArgs)
	}
	if !strings.Contains(fallbackArgs, "--app-drop-link 405 190") {
		t.Errorf("expected default Applications position, got %s", fallbackArgs)
	}

	explicitArgs := strings.Join(BuildCreateDMGArgs(DMGOptions{
		VolumeName:           "SimTX",
		AppPosition:          []int{0, 190},
		ApplicationsPosition: []int{0, 0},
	}, "/staging/SimTX.app", "/out/SimTX.dmg"), " ")
	if !strings.Contains(explicitArgs, "--icon SimTX.app 0 190") {
		t.Errorf("expected explicit app icon position, got %s", explicitArgs)
	}
	if !strings.Contains(explicitArgs, "--app-drop-link 0 0") {
		t.Errorf("expected explicit Applications position, got %s", explicitArgs)
	}
}

func TestBuildCreateDMGArgs(t *testing.T) {
	opts := DMGOptions{
		VolumeName:           "SimTX",
		WindowWidth:          540,
		WindowHeight:         380,
		AppPosition:          []int{135, 190},
		ApplicationsPosition: []int{405, 190},
	}

	// Production stages the .app in a dedicated temporary directory, so the
	// DMG source contains only the bundle (plus create-dmg's Applications
	// symlink) and never sibling artifacts from the output directory.
	args := BuildCreateDMGArgs(opts, "/staging/SimTX.app", "/output/SimTX.dmg")
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "--volname SimTX") {
		t.Errorf("missing --volname in %s", joined)
	}
	if !strings.Contains(joined, "--window-size 540 380") {
		t.Errorf("missing --window-size in %s", joined)
	}
	if !strings.Contains(joined, "--icon SimTX.app 135 190") {
		t.Errorf("missing --icon coordinates in %s", joined)
	}
	if !strings.Contains(joined, "--app-drop-link 405 190") {
		t.Errorf("missing --app-drop-link in %s", joined)
	}
	if !strings.HasSuffix(joined, "/output/SimTX.dmg /staging") {
		t.Errorf("missing output dmg and staging source in %s", joined)
	}
	if strings.Contains(joined, "/output ") {
		t.Errorf("source directory must not be the output directory with sibling files: %s", joined)
	}
}

func TestStageAppForCreateDMGExcludesSiblings(t *testing.T) {
	outDir := t.TempDir()
	appPath := filepath.Join(outDir, "SimTX.app")
	if err := os.MkdirAll(filepath.Join(appPath, "Contents", "MacOS"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appPath, "Contents", "MacOS", "simtx"), []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}
	// Sibling artifacts in the output directory must never be staged.
	for _, sibling := range []string{"entitlements.plist", "SimTX.dmg", "simtx-cli"} {
		if err := os.WriteFile(filepath.Join(outDir, sibling), []byte("sibling"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	stagingDir, stagedApp, err := stageAppForCreateDMG(appPath)
	if err != nil {
		t.Fatalf("stageAppForCreateDMG failed: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(stagingDir) })

	if filepath.Dir(stagedApp) != stagingDir {
		t.Errorf("staged app %s is not directly inside staging dir %s", stagedApp, stagingDir)
	}

	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "SimTX.app" {
		t.Errorf("expected staging dir to contain only SimTX.app, got %v", entries)
	}
	for _, sibling := range []string{"entitlements.plist", "SimTX.dmg", "simtx-cli"} {
		if _, err := os.Stat(filepath.Join(stagingDir, sibling)); !os.IsNotExist(err) {
			t.Errorf("sibling %s leaked into the DMG staging directory", sibling)
		}
	}

	fi, err := os.Stat(filepath.Join(stagedApp, "Contents", "MacOS", "simtx"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&0111 == 0 {
		t.Errorf("staged binary lost its executable bit: %v", fi.Mode())
	}
}

func TestCopyDirPreservesExecutableBit(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "copy")

	if err := os.MkdirAll(filepath.Join(src, "Contents", "MacOS"), 0755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(src, "Contents", "MacOS", "simtx")
	if err := os.WriteFile(exe, []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(src, "Contents", "Resources", "note.txt")
	if err := os.MkdirAll(filepath.Dir(data), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(data, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := fsutil.CopyDir(src, dst); err != nil {
		t.Fatalf("copyDir failed: %v", err)
	}

	fi, err := os.Stat(filepath.Join(dst, "Contents", "MacOS", "simtx"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&0111 == 0 {
		t.Errorf("copied binary lost its executable bit: %v", fi.Mode())
	}

	fiData, err := os.Stat(filepath.Join(dst, "Contents", "Resources", "note.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if fiData.Mode().Perm() != 0644 {
		t.Errorf("expected copied file mode 0644, got %v", fiData.Mode().Perm())
	}
}

func TestCalculateDMGSize(t *testing.T) {
	appDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(appDir, "Contents", "MacOS"), 0755); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 3<<20)
	if err := os.WriteFile(filepath.Join(appDir, "Contents", "MacOS", "app"), payload, 0755); err != nil {
		t.Fatal(err)
	}

	size, err := calculateDMGSize(appDir)
	if err != nil {
		t.Fatalf("calculateDMGSize failed: %v", err)
	}
	const mib = int64(1 << 20)
	if size%(mib) != 0 {
		t.Errorf("expected size rounded to MiB, got %d", size)
	}
	if size < int64(len(payload))+dmgHeadroomBytes {
		t.Errorf("expected at least payload+headroom (%d), got %d", int64(len(payload))+dmgHeadroomBytes, size)
	}

	emptySize, err := calculateDMGSize(t.TempDir())
	if err != nil {
		t.Fatalf("calculateDMGSize(empty) failed: %v", err)
	}
	if emptySize != dmgHeadroomBytes {
		t.Errorf("expected empty bundle size %d, got %d", dmgHeadroomBytes, emptySize)
	}
}

func TestBuildDMGWithHdiutilDetachesOnce(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "hdiutil.log")
	scriptPath := filepath.Join(tmpDir, "hdiutil")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	appDir := filepath.Join(tmpDir, "SimTX.app")
	if err := os.MkdirAll(filepath.Join(appDir, "Contents", "MacOS"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "Contents", "MacOS", "simtx"), make([]byte, 1<<20), 0755); err != nil {
		t.Fatal(err)
	}

	dmgPath := filepath.Join(tmpDir, "out", "SimTX.dmg")
	if err := os.MkdirAll(filepath.Dir(dmgPath), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{RootDir: tmpDir, Package: config.PackageConfig{OutputDir: "out"}}
	runner := cookExec.NewRunner("")
	runner.Stdout = io.Discard
	runner.Stderr = io.Discard

	if _, err := buildDMGWithHdiutil(context.Background(), cfg, DMGOptions{VolumeName: "SimTX"}, appDir, dmgPath, scriptPath, runner); err != nil {
		t.Fatalf("buildDMGWithHdiutil failed: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read fake hdiutil log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	detaches := 0
	var createLine string
	for _, line := range lines {
		if strings.HasPrefix(line, "detach ") {
			detaches++
		}
		if strings.HasPrefix(line, "create ") {
			createLine = line
		}
	}
	if detaches != 1 {
		t.Errorf("expected exactly one detach call, got %d (%v)", detaches, lines)
	}
	if createLine == "" {
		t.Fatalf("no create call logged: %v", lines)
	}
	if strings.Contains(createLine, "500m") {
		t.Errorf("expected dynamic image size, got %q", createLine)
	}
	if !strings.Contains(createLine, "65m") {
		t.Errorf("expected 65m image size for a 1 MiB app, got %q", createLine)
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
