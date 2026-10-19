package exec

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/simtxng/cook/pkg/config"
)

func newTestConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		RootDir: t.TempDir(),
		Package: config.PackageConfig{
			Name:      "testapp",
			OutputDir: "bin",
		},
	}
}

func TestBuildEnvUsesBinDirOnly(t *testing.T) {
	cfg := newTestConfig(t)
	hm := NewHookManager(cfg)

	env := hm.BuildEnv(nil)

	want := filepath.Join(cfg.RootDir, "bin")
	if got := env["BIN_DIR"]; got != want {
		t.Fatalf("BIN_DIR = %q, want %q", got, want)
	}
	if _, ok := env["OUTPUT_DIR"]; ok {
		t.Fatalf("OUTPUT_DIR = %q, want it absent", env["OUTPUT_DIR"])
	}
}

func TestRunCommandsUsesManagerRunner(t *testing.T) {
	cfg := newTestConfig(t)
	var stdout, stderr bytes.Buffer
	hm := &HookManager{
		Config: cfg,
		Runner: &Runner{Stdout: &stdout, Stderr: &stderr, Prefix: "custom"},
	}

	if err := hm.runCommands(t.Context(), "test", []string{"printf hello"}, nil); err != nil {
		t.Fatalf("runCommands: %v", err)
	}

	if got, want := stdout.String(), "[custom] hello"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunFixScriptUsesManagerRunner(t *testing.T) {
	cfg := newTestConfig(t)
	scriptPath := filepath.Join(cfg.RootDir, "fix.sh")
	if err := os.WriteFile(scriptPath, []byte("printf fixed\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	var stdout, stderr bytes.Buffer
	hm := &HookManager{
		Config: cfg,
		Runner: &Runner{Stdout: &stdout, Stderr: &stderr, Prefix: "fix"},
	}

	if err := hm.RunFixScript(t.Context(), "fix.sh", nil, nil); err != nil {
		t.Fatalf("RunFixScript: %v", err)
	}

	if got, want := stdout.String(), "[fix] fixed"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}
