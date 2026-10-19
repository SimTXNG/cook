package dev

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/simtxng/cook/pkg/builder"
	"github.com/simtxng/cook/pkg/config"
)

func TestDevCompileArgsStripProductionTag(t *testing.T) {
	cfg := &config.Config{
		RootDir: t.TempDir(),
		Package: config.PackageConfig{OutputDir: "bin"},
	}
	b := &config.BinaryConfig{
		Name: "app",
		Tags: []string{"production", "debug"},
	}

	args := builder.GoBuildArgs(cfg, b, "linux", builder.GoBuildOptions{
		ExcludeTags: []string{"production"},
		ExtraTags:   []string{"devmode"},
		GCFlags:     "all=-l",
	})

	joined := strings.Join(args, " ")
	if strings.Contains(joined, "production") {
		t.Errorf("production tag should be stripped: %v", args)
	}
	if !strings.Contains(joined, "-tags debug,devmode") {
		t.Errorf("expected remaining and extra tags joined: %v", args)
	}
	if !strings.Contains(joined, "-gcflags=all=-l") {
		t.Errorf("expected dev gcflags: %v", args)
	}
	if strings.Contains(joined, "-trimpath") {
		t.Errorf("dev build must not use -trimpath: %v", args)
	}
}

func TestBackendSupervisor_Compile(t *testing.T) {
	tempDir := t.TempDir()

	// Write simple main.go
	mainGo := filepath.Join(tempDir, "main.go")
	content := `package main

import "fmt"

func main() {
	fmt.Println("hello from dev")
}
`
	if err := os.WriteFile(mainGo, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write main.go: %v", err)
	}

	// Write go.mod
	goMod := filepath.Join(tempDir, "go.mod")
	if err := os.WriteFile(goMod, []byte("module testdev\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:      "testdev",
			OutputDir: "bin",
		},
		Binaries: []config.BinaryConfig{
			{
				Name: "testdev",
				Path: ".",
				GUI:  true,
				Tags: []string{"production", "debug"},
			},
		},
		RootDir: tempDir,
	}

	supervisor := NewBackendSupervisor(cfg)
	binPath, err := supervisor.Compile(context.Background(), nil)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("compiled binary not found at %s: %v", binPath, err)
	}
}

func TestBackendSupervisor_AppExited(t *testing.T) {
	tempDir := t.TempDir()

	mainGo := filepath.Join(tempDir, "main.go")
	content := `package main
import "os"
func main() {
	os.Exit(0)
}
`
	if err := os.WriteFile(mainGo, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write main.go: %v", err)
	}
	goMod := filepath.Join(tempDir, "go.mod")
	_ = os.WriteFile(goMod, []byte("module testexit\n\ngo 1.22\n"), 0644)

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:      "testexit",
			OutputDir: "bin",
		},
		Binaries: []config.BinaryConfig{
			{Name: "testexit", Path: ".", GUI: true},
		},
		RootDir: tempDir,
	}

	supervisor := NewBackendSupervisor(cfg)
	if _, err := supervisor.Compile(context.Background(), nil); err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	if err := supervisor.Start(context.Background(), "http://127.0.0.1:9245", 9245, nil); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	select {
	case <-supervisor.AppExited():
		// Pass! Process exit detected cleanly
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for AppExited signal")
	}
}
