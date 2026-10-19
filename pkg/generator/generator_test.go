package generator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/simtxng/cook/pkg/config"
)

func TestGenerator_BasicRendering(t *testing.T) {
	tempDir := t.TempDir()
	templatesDir := filepath.Join(tempDir, "build", "templates")
	if err := os.MkdirAll(filepath.Join(templatesDir, "windows"), 0755); err != nil {
		t.Fatalf("failed to create templates dir: %v", err)
	}

	tmplContent := `{
	"version": "{{.Version.Raw}}",
	"name": "{{.Package.Name}}",
	"company": "{{.Package.Company}}",
	"four_part": "{{fourPartVersion .Version.Raw}}",
	"tilde": "{{tildeVersion .Version.Raw}}"
}`
	tmplFile := filepath.Join(templatesDir, "windows", "info.json.tmpl")
	if err := os.WriteFile(tmplFile, []byte(tmplContent), 0644); err != nil {
		t.Fatalf("failed to write tmpl: %v", err)
	}

	cfg := &config.Config{
		Package: config.PackageConfig{
			Name:    "myproject",
			Company: "Acme Corp",
		},
		Version: &config.VersionInfo{
			Raw:     "0.1.0-alpha-12",
			Windows: "0.1.0.0",
		},
		Templates: config.TemplateConfig{
			Dir: "build/templates",
		},
		RootDir: tempDir,
	}

	gen := NewGenerator()
	files, err := gen.Generate(context.Background(), cfg, "", false)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("expected 1 file generated, got %d", len(files))
	}

	expectedDest := filepath.Join(tempDir, "build", "windows", "info.json")
	if files[0].DestPath != expectedDest {
		t.Errorf("expected dest %s, got %s", expectedDest, files[0].DestPath)
	}

	data, err := os.ReadFile(expectedDest)
	if err != nil {
		t.Fatalf("failed to read generated dest: %v", err)
	}

	str := string(data)
	if !strings.Contains(str, `"version": "0.1.0-alpha-12"`) {
		t.Errorf("missing version in rendered file: %s", str)
	}
	if !strings.Contains(str, `"four_part": "0.1.0.0"`) {
		t.Errorf("missing four_part in rendered file: %s", str)
	}
	if !strings.Contains(str, `"tilde": "0.1.0~alpha12"`) {
		t.Errorf("missing tilde in rendered file: %s", str)
	}
}

func TestGenerator_InlineDirective(t *testing.T) {
	tempDir := t.TempDir()
	templatesDir := filepath.Join(tempDir, "build", "templates")
	if err := os.MkdirAll(templatesDir, 0755); err != nil {
		t.Fatalf("failed to create templates dir: %v", err)
	}

	tmplContent := `# cook:out=custom/path/target.txt
Hello {{.Package.Name}} v{{.Version.Raw}}!
`
	tmplFile := filepath.Join(templatesDir, "custom.tmpl")
	if err := os.WriteFile(tmplFile, []byte(tmplContent), 0644); err != nil {
		t.Fatalf("failed to write tmpl: %v", err)
	}

	cfg := &config.Config{
		Package: config.PackageConfig{Name: "directed"},
		Version: &config.VersionInfo{Raw: "1.2.3"},
		Templates: config.TemplateConfig{
			Dir: "build/templates",
		},
		RootDir: tempDir,
	}

	gen := NewGenerator()
	files, err := gen.Generate(context.Background(), cfg, "", false)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	expectedDest := filepath.Join(tempDir, "custom", "path", "target.txt")
	if files[0].DestPath != expectedDest {
		t.Errorf("expected %s, got %s", expectedDest, files[0].DestPath)
	}

	data, err := os.ReadFile(expectedDest)
	if err != nil {
		t.Fatalf("failed to read dest: %v", err)
	}

	outStr := string(data)
	if strings.Contains(outStr, "# cook:out") {
		t.Errorf("directive was not stripped from content: %s", outStr)
	}
	if !strings.Contains(outStr, "Hello directed v1.2.3!") {
		t.Errorf("unexpected content: %s", outStr)
	}
}

func TestGenerator_DryRun(t *testing.T) {
	tempDir := t.TempDir()
	templatesDir := filepath.Join(tempDir, "build", "templates")
	_ = os.MkdirAll(templatesDir, 0755)

	tmplFile := filepath.Join(templatesDir, "dry.txt.tmpl")
	_ = os.WriteFile(tmplFile, []byte("dry run {{.Version.Raw}}"), 0644)

	cfg := &config.Config{
		Version:   &config.VersionInfo{Raw: "1.0.0"},
		Templates: config.TemplateConfig{Dir: "build/templates"},
		RootDir:   tempDir,
	}

	gen := NewGenerator()
	files, err := gen.Generate(context.Background(), cfg, "", true)
	if err != nil {
		t.Fatalf("dry run failed: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	dest := filepath.Join(tempDir, "build", "dry.txt")
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("file should not exist during dry run")
	}
}

func TestGenerator_StaticAssetCopying(t *testing.T) {
	tempDir := t.TempDir()
	templatesDir := filepath.Join(tempDir, "templates")
	if err := os.MkdirAll(filepath.Join(templatesDir, "windows"), 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	// Create a static binary/text asset
	staticContent := []byte{0x00, 0x01, 0x02, 0x03, 'H', 'e', 'l', 'l', 'o'}
	staticPath := filepath.Join(templatesDir, "windows", "icon.ico")
	if err := os.WriteFile(staticPath, staticContent, 0755); err != nil {
		t.Fatalf("failed to write static asset: %v", err)
	}

	cfg := &config.Config{
		Package:   config.PackageConfig{Name: "static-test"},
		RootDir:   tempDir,
		Templates: config.TemplateConfig{Dir: "templates"},
	}

	gen := NewGenerator()
	files, err := gen.Generate(context.Background(), cfg, "", false)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	expectedDest := filepath.Join(tempDir, "build", "windows", "icon.ico")
	if files[0].DestPath != expectedDest {
		t.Errorf("expected dest %s, got %s", expectedDest, files[0].DestPath)
	}

	data, err := os.ReadFile(expectedDest)
	if err != nil {
		t.Fatalf("failed to read copied asset: %v", err)
	}

	if len(data) != len(staticContent) {
		t.Errorf("expected length %d, got %d", len(staticContent), len(data))
	}

	fi, err := os.Stat(expectedDest)
	if err != nil {
		t.Fatalf("failed to stat copied asset: %v", err)
	}
	if fi.Mode().Perm() != 0755 {
		t.Errorf("expected mode 0755, got %v", fi.Mode().Perm())
	}
}

func TestGenerator_UnchangedFileSkipped(t *testing.T) {
	tempDir := t.TempDir()
	templatesDir := filepath.Join(tempDir, "build", "templates")
	if err := os.MkdirAll(templatesDir, 0755); err != nil {
		t.Fatalf("failed to create templates dir: %v", err)
	}

	tmplFile := filepath.Join(templatesDir, "same.txt.tmpl")
	if err := os.WriteFile(tmplFile, []byte("hello {{.Package.Name}}"), 0644); err != nil {
		t.Fatalf("failed to write tmpl: %v", err)
	}

	cfg := &config.Config{
		Package:   config.PackageConfig{Name: "unchanged"},
		Templates: config.TemplateConfig{Dir: "build/templates"},
		RootDir:   tempDir,
	}

	gen := NewGenerator()
	files, err := gen.Generate(context.Background(), cfg, "", false)
	if err != nil {
		t.Fatalf("first Generate failed: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if !files[0].Created || !files[0].Changed {
		t.Fatalf("first run: expected created+changed, got created=%v changed=%v", files[0].Created, files[0].Changed)
	}

	dest := filepath.Join(tempDir, "build", "same.txt")
	oldTime := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(dest, oldTime, oldTime); err != nil {
		t.Fatalf("failed to set mtime: %v", err)
	}

	files, err = gen.Generate(context.Background(), cfg, "", false)
	if err != nil {
		t.Fatalf("second Generate failed: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Created {
		t.Errorf("unchanged file should not be reported as created")
	}
	if files[0].Changed {
		t.Errorf("unchanged file should not be reported as changed")
	}

	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("failed to stat dest: %v", err)
	}
	if fi.ModTime().Unix() != oldTime.Unix() {
		t.Errorf("unchanged file was rewritten: mtime %v, want %v", fi.ModTime(), oldTime)
	}
}

func TestGenerator_DuplicateSuppression(t *testing.T) {
	t.Run("tmpl wins over template and static", func(t *testing.T) {
		tempDir := t.TempDir()
		templatesDir := filepath.Join(tempDir, "build", "templates")
		if err := os.MkdirAll(templatesDir, 0755); err != nil {
			t.Fatalf("failed to create templates dir: %v", err)
		}

		for name, content := range map[string]string{
			"dup.txt":          "static",
			"dup.txt.tmpl":     "from-tmpl",
			"dup.txt.template": "from-template",
		} {
			if err := os.WriteFile(filepath.Join(templatesDir, name), []byte(content), 0644); err != nil {
				t.Fatalf("failed to write %s: %v", name, err)
			}
		}

		cfg := &config.Config{
			Templates: config.TemplateConfig{Dir: "build/templates"},
			RootDir:   tempDir,
		}

		files, err := NewGenerator().Generate(context.Background(), cfg, "", false)
		if err != nil {
			t.Fatalf("Generate failed: %v", err)
		}
		if len(files) != 1 {
			t.Fatalf("expected 1 candidate, got %d: %+v", len(files), files)
		}
		if got, want := files[0].TemplatePath, filepath.Join(templatesDir, "dup.txt.tmpl"); got != want {
			t.Errorf("expected template %s, got %s", want, got)
		}

		data, err := os.ReadFile(filepath.Join(tempDir, "build", "dup.txt"))
		if err != nil {
			t.Fatalf("failed to read dest: %v", err)
		}
		if string(data) != "from-tmpl" {
			t.Errorf("expected .tmpl content, got %q", string(data))
		}
	})

	t.Run("template sibling suppresses static", func(t *testing.T) {
		tempDir := t.TempDir()
		templatesDir := filepath.Join(tempDir, "build", "templates")
		if err := os.MkdirAll(templatesDir, 0755); err != nil {
			t.Fatalf("failed to create templates dir: %v", err)
		}

		if err := os.WriteFile(filepath.Join(templatesDir, "only.txt"), []byte("static"), 0644); err != nil {
			t.Fatalf("failed to write static: %v", err)
		}
		if err := os.WriteFile(filepath.Join(templatesDir, "only.txt.template"), []byte("from-template"), 0644); err != nil {
			t.Fatalf("failed to write template: %v", err)
		}

		cfg := &config.Config{
			Templates: config.TemplateConfig{Dir: "build/templates"},
			RootDir:   tempDir,
		}

		files, err := NewGenerator().Generate(context.Background(), cfg, "", false)
		if err != nil {
			t.Fatalf("Generate failed: %v", err)
		}
		if len(files) != 1 {
			t.Fatalf("expected 1 candidate, got %d: %+v", len(files), files)
		}
		if got, want := files[0].TemplatePath, filepath.Join(templatesDir, "only.txt.template"); got != want {
			t.Errorf("expected template %s, got %s", want, got)
		}
	})
}

func TestGenerator_TemplateModeIs0644(t *testing.T) {
	tempDir := t.TempDir()
	templatesDir := filepath.Join(tempDir, "build", "templates")
	if err := os.MkdirAll(templatesDir, 0755); err != nil {
		t.Fatalf("failed to create templates dir: %v", err)
	}

	tmplFile := filepath.Join(templatesDir, "script.sh.tmpl")
	if err := os.WriteFile(tmplFile, []byte("#!/bin/sh\necho hi\n"), 0755); err != nil {
		t.Fatalf("failed to write tmpl: %v", err)
	}
	if err := os.Chmod(tmplFile, 0755); err != nil {
		t.Fatalf("failed to chmod tmpl: %v", err)
	}

	cfg := &config.Config{
		Templates: config.TemplateConfig{Dir: "build/templates"},
		RootDir:   tempDir,
	}

	if _, err := NewGenerator().Generate(context.Background(), cfg, "", false); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	fi, err := os.Stat(filepath.Join(tempDir, "build", "script.sh"))
	if err != nil {
		t.Fatalf("failed to stat rendered file: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0644 {
		t.Errorf("rendered template mode = %o, want 0644", got)
	}
}

func TestGenerator_DefaultDestOutsideTemplates(t *testing.T) {
	tempDir := t.TempDir()
	outsideDir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(tempDir, "extra"), 0755); err != nil {
		t.Fatalf("failed to create extra dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "extra", "tool.sh.tmpl"), []byte("in-root"), 0644); err != nil {
		t.Fatalf("failed to write in-root template: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "loose.txt.tmpl"), []byte("outside"), 0644); err != nil {
		t.Fatalf("failed to write outside template: %v", err)
	}

	cfg := &config.Config{
		Templates: config.TemplateConfig{
			Mappings: []config.TemplateMappingConfig{
				{Src: "extra/tool.sh.tmpl"},
				{Src: filepath.Join(outsideDir, "loose.txt.tmpl")},
			},
		},
		RootDir: tempDir,
	}

	files, err := NewGenerator().Generate(context.Background(), cfg, "", false)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %+v", len(files), files)
	}

	dests := map[string]bool{}
	for _, f := range files {
		dests[f.DestPath] = true
	}
	if want := filepath.Join(tempDir, "build", "extra", "tool.sh"); !dests[want] {
		t.Errorf("missing expected dest %s in %+v", want, files)
	}
	if want := filepath.Join(tempDir, "build", "templates", "loose.txt"); !dests[want] {
		t.Errorf("missing expected dest %s in %+v", want, files)
	}
}

func TestGenerator_CanceledContext(t *testing.T) {
	tempDir := t.TempDir()
	templatesDir := filepath.Join(tempDir, "build", "templates")
	if err := os.MkdirAll(templatesDir, 0755); err != nil {
		t.Fatalf("failed to create templates dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(templatesDir, "a.txt.tmpl"), []byte("a"), 0644); err != nil {
		t.Fatalf("failed to write tmpl: %v", err)
	}

	cfg := &config.Config{
		Templates: config.TemplateConfig{Dir: "build/templates"},
		RootDir:   tempDir,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := NewGenerator().Generate(ctx, cfg, "", false); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(tempDir, "build", "a.txt")); !os.IsNotExist(err) {
		t.Errorf("generated file should not exist after cancellation")
	}
}

func TestTitleCase(t *testing.T) {
	tests := map[string]string{
		"hello world":      "Hello World",
		"helloWorld":       "HelloWorld",
		"  multiple  gaps": "  Multiple  Gaps",
		"":                 "",
	}
	for in, want := range tests {
		if got := titleCase(in); got != want {
			t.Errorf("titleCase(%q) = %q, want %q", in, got, want)
		}
	}
}
