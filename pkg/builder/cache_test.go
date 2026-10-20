package builder

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/simtxng/cook/pkg/config"
)

func TestBuildWithOptionsBuildsAllBinaries(t *testing.T) {
	root := setupGoModule(t)
	writeFile(t, filepath.Join(root, "cmd", "bar", "main.go"), "package main\n\nfunc main() {}\n")

	cfg := &config.Config{
		RootDir: root,
		Package: config.PackageConfig{OutputDir: "bin"},
		Binaries: []config.BinaryConfig{
			{Name: "foo", Path: "./cmd/foo"},
			{Name: "bar", Path: "./cmd/bar"},
		},
	}

	built, err := NewBuilder(cfg).BuildWithOptions(context.Background(), nil, runtime.GOOS, runtime.GOARCH, BuildOptions{Jobs: 2})
	if err != nil {
		t.Fatalf("BuildWithOptions: %v", err)
	}
	if len(built) != 2 {
		t.Fatalf("built %d binaries, want 2", len(built))
	}
	for _, name := range []string{"foo", "bar"} {
		if _, err := os.Stat(filepath.Join(root, "bin", name)); err != nil {
			t.Errorf("missing built binary %s: %v", name, err)
		}
	}
}

func TestBuildSkipUnchangedReusesBinary(t *testing.T) {
	root := setupGoModule(t)
	cfg := &config.Config{
		RootDir: root,
		Package: config.PackageConfig{OutputDir: "bin"},
		Binaries: []config.BinaryConfig{
			{Name: "foo", Path: "./cmd/foo"},
		},
	}

	builder := NewBuilder(cfg)
	opts := BuildOptions{Jobs: 1, SkipUnchanged: true}
	if _, err := builder.BuildWithOptions(context.Background(), nil, runtime.GOOS, runtime.GOARCH, opts); err != nil {
		t.Fatalf("first build: %v", err)
	}

	output := filepath.Join(root, "bin", "foo")
	before, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(10 * time.Millisecond)
	if _, err := builder.BuildWithOptions(context.Background(), nil, runtime.GOOS, runtime.GOARCH, opts); err != nil {
		t.Fatalf("second build: %v", err)
	}

	after, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("unchanged build rewrote the binary (%v -> %v)", before.ModTime(), after.ModTime())
	}
}

func TestBuildBinaryUPXUsesConfiguredLevel(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skip("UPX is skipped on darwin and the fake script is POSIX-only")
	}

	root := setupGoModule(t)
	binDir := t.TempDir()
	logPath := filepath.Join(binDir, "upx.log")
	upx := filepath.Join(binDir, "upx")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$UPX_LOG\"\ncase \"$1\" in\n  -l) exit 1 ;;\nesac\nexit 0\n"
	writeFile(t, upx, script)
	if err := os.Chmod(upx, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UPX_LOG", logPath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := &config.Config{
		RootDir: root,
		Package: config.PackageConfig{OutputDir: "bin"},
	}
	bCfg := &config.BinaryConfig{Name: "foo", Path: "./cmd/foo", UPX: true, UPXLevel: 2}

	if _, err := BuildBinary(context.Background(), cfg, bCfg, runtime.GOOS, runtime.GOARCH); err != nil {
		t.Fatalf("BuildBinary: %v", err)
	}

	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read upx log: %v", err)
	}
	if !strings.Contains(string(log), "-2") {
		t.Fatalf("upx was not invoked with the configured level:\n%s", log)
	}
}

func TestGoBuildCacheCold(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("GOCACHE", cache)
	if !goBuildCacheCold() {
		t.Fatal("empty cache should be cold")
	}
	if err := os.MkdirAll(filepath.Join(cache, "00"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !goBuildCacheCold() {
		t.Fatal("cache with only empty shards should be cold")
	}
	if err := os.WriteFile(filepath.Join(cache, "00", "entry"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if goBuildCacheCold() {
		t.Fatal("cache with an entry should be warm")
	}
}
