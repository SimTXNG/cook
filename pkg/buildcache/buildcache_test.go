package buildcache

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/simtxng/cook/pkg/config"
)

func TestFingerprintChangesWithInputs(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(file, []byte("one"), 0644); err != nil {
		t.Fatal(err)
	}

	first, err := Fingerprint([]string{file}, "extra")
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	second, err := Fingerprint([]string{file}, "extra")
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if first != second {
		t.Fatal("fingerprint should be stable for unchanged inputs")
	}

	if err := os.WriteFile(file, []byte("two-two"), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := Fingerprint([]string{file}, "extra")
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if changed == first {
		t.Fatal("fingerprint should change when the input changes")
	}

	extraChanged, err := Fingerprint([]string{file}, "other")
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if extraChanged == changed {
		t.Fatal("fingerprint should change when extra data changes")
	}
}

func TestStorePersistsAndChecksOutputs(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "out.bin")
	if err := os.WriteFile(output, []byte("built"), 0644); err != nil {
		t.Fatal(err)
	}

	path := Path(dir)
	store := Open(path)
	if store.Fresh("go:app", "fp", output) {
		t.Fatal("empty store must not report fresh")
	}
	if err := store.Mark("go:app", "fp"); err != nil {
		t.Fatalf("Mark: %v", err)
	}

	reopened := Open(path)
	if !reopened.Fresh("go:app", "fp", output) {
		t.Fatal("marked entry should be fresh after reload")
	}
	if reopened.Fresh("go:app", "other", output) {
		t.Fatal("mismatched fingerprint must not be fresh")
	}
	if err := os.Remove(output); err != nil {
		t.Fatal(err)
	}
	if reopened.Fresh("go:app", "fp", output) {
		t.Fatal("missing output must not be fresh")
	}
}

func TestWalkFilesSkipsDirectories(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"src/main.go",
		"node_modules/dep/index.js",
		".git/config",
		"vendor/lib/lib.go",
	} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	skip := map[string]bool{"node_modules": true, "vendor": true}
	files, err := WalkFiles(root, skip, func(rel string) bool {
		return filepath.Ext(rel) == ".go"
	})
	if err != nil {
		t.Fatalf("WalkFiles: %v", err)
	}
	if len(files) != 1 || filepath.Base(files[0]) != "main.go" {
		t.Fatalf("WalkFiles = %v, want only src/main.go", files)
	}
}

func TestBindingsOutputsAndFrontendInputs(t *testing.T) {
	root := t.TempDir()
	frontend := filepath.Join(root, "frontend")
	if err := os.MkdirAll(filepath.Join(frontend, "bindings"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(frontend, "node_modules"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(frontend, "package.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(frontend, "node_modules", "ignored.js"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		RootDir:  root,
		Frontend: config.FrontendConfig{Dir: "frontend"},
	}

	outputs := BindingsOutputs(cfg)
	if len(outputs) != 1 || outputs[0] != filepath.Join(frontend, "bindings") {
		t.Fatalf("BindingsOutputs = %v", outputs)
	}

	files, err := FrontendInputs(cfg)
	if err != nil {
		t.Fatalf("FrontendInputs: %v", err)
	}
	for _, file := range files {
		if filepath.Base(file) == "ignored.js" {
			t.Fatalf("FrontendInputs should skip node_modules: %v", files)
		}
	}
	if len(files) == 0 {
		t.Fatal("FrontendInputs returned no files")
	}
}
