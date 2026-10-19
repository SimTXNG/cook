package generator

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"
	"unicode"

	"github.com/simtxng/cook/pkg/config"
)

// TemplateContext is the data context exposed to all templates.
type TemplateContext struct {
	*config.Config
	Env map[string]string
}

// GeneratedFile represents the outcome of a rendered template.
type GeneratedFile struct {
	TemplatePath string
	DestPath     string
	Created      bool
	Changed      bool
}

// Generator manages template discovery and rendering based on cook configuration.
type Generator struct{}

// NewGenerator creates a new Generator instance.
func NewGenerator() *Generator {
	return &Generator{}
}

// Generate scans, parses, and renders templates using single-source-of-truth metadata,
// and copies static assets from the templates directory into the build staging directory.
func (g *Generator) Generate(ctx context.Context, cfg *config.Config, targetFilter string, dryRun bool) ([]GeneratedFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	templatesDir := cfg.Templates.Dir
	if templatesDir == "" {
		// Prefer templates/ at root if exists, else build/templates
		rootTmpl := filepath.Join(cfg.RootDir, "templates")
		if fi, err := os.Stat(rootTmpl); err == nil && fi.IsDir() {
			templatesDir = rootTmpl
		} else {
			templatesDir = filepath.Join(cfg.RootDir, "build", "templates")
		}
	} else if !filepath.IsAbs(templatesDir) {
		templatesDir = filepath.Join(cfg.RootDir, templatesDir)
	}

	// Fallback to "templates" in root if build/templates does not exist
	if _, err := os.Stat(templatesDir); os.IsNotExist(err) {
		altDir := filepath.Join(cfg.RootDir, "templates")
		if _, errAlt := os.Stat(altDir); errAlt == nil {
			templatesDir = altDir
		}
	}

	// Collect template candidates (both dynamic .tmpl and static assets)
	type candidate struct {
		src      string
		dest     string
		perm     os.FileMode
		havePerm bool
	}
	var candidates []candidate

	// 1. Scan directory if exists
	if fi, err := os.Stat(templatesDir); err == nil && fi.IsDir() {
		err := filepath.Walk(templatesDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			candidates = append(candidates, candidate{src: path, perm: info.Mode().Perm(), havePerm: true})
			return nil
		})
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, fmt.Errorf("failed to scan templates directory %s: %w", templatesDir, err)
		}
	}

	// 2. Explicit mappings from config
	for _, m := range cfg.Templates.Mappings {
		src := m.Src
		if !filepath.IsAbs(src) {
			src = filepath.Join(cfg.RootDir, src)
		}
		dest := m.Dest
		if !filepath.IsAbs(dest) && dest != "" {
			dest = filepath.Join(cfg.RootDir, dest)
		}
		candidates = append(candidates, candidate{src: src, dest: dest})
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	// Build TemplateContext
	envMap := make(map[string]string)
	for _, e := range os.Environ() {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	tmplCtx := TemplateContext{Config: cfg, Env: envMap}
	var results []GeneratedFile

	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		isTmpl := strings.HasSuffix(c.src, ".tmpl") || strings.HasSuffix(c.src, ".template")

		// Suppress duplicate candidates before touching the file. A static file
		// is skipped when either template sibling exists; when both template
		// forms exist, .tmpl wins over .template.
		if !isTmpl {
			if fileExists(c.src+".tmpl") || fileExists(c.src+".template") {
				continue
			}
		} else if strings.HasSuffix(c.src, ".template") {
			if fileExists(strings.TrimSuffix(c.src, ".template") + ".tmpl") {
				continue
			}
		}

		data, err := os.ReadFile(c.src)
		if err != nil {
			return nil, fmt.Errorf("failed to read template %s: %w", c.src, err)
		}

		// Rendered templates always get 0644; copied static assets keep their
		// source permission bits so executable assets still work.
		fileMode := os.FileMode(0644)
		if !isTmpl {
			if c.havePerm {
				fileMode = c.perm
			} else {
				fi, err := os.Stat(c.src)
				if err != nil {
					return nil, fmt.Errorf("failed to stat static asset %s: %w", c.src, err)
				}
				fileMode = fi.Mode().Perm()
			}
		}

		// Determine destination path
		destPath := c.dest
		var contentToParse string

		if isTmpl {
			contentToParse = string(data)

			// Check for inline output directive on first line
			directiveDest, cleanContent := extractOutputDirective(contentToParse)
			if directiveDest != "" {
				destPath = directiveDest
				if !filepath.IsAbs(destPath) {
					destPath = filepath.Join(cfg.RootDir, destPath)
				}
				contentToParse = cleanContent
			}
		}

		if destPath == "" {
			destPath = resolveDefaultDest(cfg.RootDir, templatesDir, c.src)
		}

		// Apply target filter if specified
		if targetFilter != "" {
			filter := strings.ToLower(targetFilter)
			if !strings.Contains(strings.ToLower(c.src), filter) && !strings.Contains(strings.ToLower(destPath), filter) {
				continue
			}
		}

		rendered := data
		if isTmpl {
			// Parse and execute template
			tmpl, err := template.New(filepath.Base(c.src)).Funcs(funcMap).Parse(contentToParse)
			if err != nil {
				return nil, fmt.Errorf("template parse error in %s: %w", c.src, err)
			}

			if err := ctx.Err(); err != nil {
				return nil, err
			}

			var buf bytes.Buffer
			if err := tmpl.Execute(&buf, tmplCtx); err != nil {
				return nil, fmt.Errorf("template execution error in %s: %w", c.src, err)
			}
			rendered = buf.Bytes()
		}

		created := false
		changed := true

		existing, err := os.ReadFile(destPath)
		switch {
		case err == nil:
			if bytes.Equal(existing, rendered) {
				changed = false
			}
		case os.IsNotExist(err):
			created = true
		default:
			return nil, fmt.Errorf("failed to read existing destination %s: %w", destPath, err)
		}

		if changed && !dryRun {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				return nil, fmt.Errorf("failed to create directory for %s: %w", destPath, err)
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := os.WriteFile(destPath, rendered, fileMode); err != nil {
				return nil, fmt.Errorf("failed to write generated file %s: %w", destPath, err)
			}
		}

		results = append(results, GeneratedFile{
			TemplatePath: c.src,
			DestPath:     destPath,
			Created:      created,
			Changed:      changed,
		})
	}

	return results, nil
}

// resolveDefaultDest maps templates/foo/bar.tmpl -> build/foo/bar. Sources
// outside templatesDir but inside RootDir keep their RootDir-relative layout;
// anything else falls back to its base name under build/templates. The result
// never contains "..".
func resolveDefaultDest(rootDir, templatesDir, srcPath string) string {
	if rel, err := filepath.Rel(templatesDir, srcPath); err == nil && isInside(rel) {
		return filepath.Join(rootDir, "build", stripTemplateSuffix(rel))
	}
	if rel, err := filepath.Rel(rootDir, srcPath); err == nil && isInside(rel) {
		return filepath.Join(rootDir, "build", stripTemplateSuffix(rel))
	}
	return filepath.Join(rootDir, "build", "templates", stripTemplateSuffix(filepath.Base(srcPath)))
}

func stripTemplateSuffix(name string) string {
	return strings.TrimSuffix(strings.TrimSuffix(name, ".tmpl"), ".template")
}

func isInside(rel string) bool {
	return rel != "" && rel != "." && rel != ".." &&
		!filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func titleCase(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	atWordStart := true
	for _, r := range s {
		if unicode.IsSpace(r) {
			b.WriteRune(r)
			atWordStart = true
			continue
		}
		if atWordStart {
			b.WriteRune(unicode.ToUpper(r))
			atWordStart = false
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// extractOutputDirective checks for directives like:
// # cook:out=build/windows/nsis/project.nsi
// // cook:out=build/windows/nsis/project.nsi
// <!-- cook:out=build/windows/nsis/project.nsi -->
func extractOutputDirective(content string) (string, string) {
	firstLine, rest, hasNewline := strings.Cut(content, "\n")
	firstLine = strings.TrimSpace(firstLine)
	const directivePrefix = "cook:out="
	idx := strings.Index(firstLine, directivePrefix)
	if idx == -1 {
		return "", content
	}

	targetPart := strings.TrimSpace(firstLine[idx+len(directivePrefix):])
	// Strip trailing comment markers like --> or */
	targetPart = strings.TrimSuffix(targetPart, "-->")
	targetPart = strings.TrimSuffix(targetPart, "*/")
	targetPart = strings.TrimSpace(targetPart)

	if !hasNewline {
		return targetPart, ""
	}
	return targetPart, rest
}

var funcMap = template.FuncMap{
	"lower":     strings.ToLower,
	"upper":     strings.ToUpper,
	"title":     titleCase,
	"trim":      strings.TrimSpace,
	"replace":   strings.ReplaceAll,
	"contains":  strings.Contains,
	"hasPrefix": strings.HasPrefix,
	"hasSuffix": strings.HasSuffix,
	"split":     strings.Split,
	"join":      strings.Join,
	"env":       os.Getenv,
	"now": func(layout string) string {
		if layout == "" {
			layout = time.RFC3339
		}
		return time.Now().Format(layout)
	},
	"year": func() string {
		return time.Now().Format("2006")
	},
	"tildeVersion":    config.FormatDebianVersion,
	"fourPartVersion": config.FormatWindowsVersion,
	"hyphenFree":      config.FormatArchVersion,
}
