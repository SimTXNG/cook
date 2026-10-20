// Package buildcache stores input fingerprints so unchanged build phases can be
// skipped when --skip-unchanged is requested.
package buildcache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/simtxng/cook/pkg/config"
)

// CacheFileName is the state file written at the project root.
const CacheFileName = ".cook-cache.json"

// Entry is one cached task fingerprint.
type Entry struct {
	Fingerprint string `json:"fingerprint"`
	UpdatedUnix int64  `json:"updated_unix"`
}

type state struct {
	Entries map[string]Entry `json:"entries"`
}

// Store is a small, concurrency-safe on-disk fingerprint store.
type Store struct {
	mu      sync.Mutex
	path    string
	entries map[string]Entry
}

// Path returns the cache file path for a project root.
func Path(root string) string {
	return filepath.Join(root, CacheFileName)
}

// Open loads the store at path. A missing or corrupt file yields an empty store.
func Open(path string) *Store {
	s := &Store{path: path, entries: make(map[string]Entry)}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		return s
	}
	if st.Entries != nil {
		s.entries = st.Entries
	}
	return s
}

// Fresh reports whether key was last marked with fingerprint and every output
// still exists as a regular file or directory.
func (s *Store) Fresh(key, fingerprint string, outputs ...string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[key]
	if !ok || entry.Fingerprint != fingerprint {
		return false
	}
	for _, out := range outputs {
		if _, err := os.Stat(out); err != nil {
			return false
		}
	}
	return true
}

// Mark records a successful task and persists the store.
func (s *Store) Mark(key, fingerprint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries[key] = Entry{
		Fingerprint: fingerprint,
		UpdatedUnix: time.Now().Unix(),
	}
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	data, err := json.Marshal(state{Entries: s.entries})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".cook-cache-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// Fingerprint hashes file metadata (path, size, mtime) and arbitrary extra
// values in deterministic order.
func Fingerprint(files []string, extra ...string) (string, error) {
	unique := make([]string, 0, len(files))
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		unique = append(unique, f)
	}
	sort.Strings(unique)

	h := sha256.New()
	for _, f := range unique {
		info, err := os.Stat(f)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintf(h, "%s\x00missing\x00", f)
				continue
			}
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00%d\x00", f, info.Size(), info.ModTime().UnixNano())
	}
	for _, e := range extra {
		h.Write([]byte(e))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// WalkFiles walks root collecting files accepted by include. Directories whose
// base name or root-relative path is in skipDirs are pruned.
func WalkFiles(root string, skipDirs map[string]bool, include func(rel string) bool) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if path == root {
				return nil
			}
			base := d.Name()
			if skipDirs[base] || skipDirs[rel] || strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if include == nil || include(rel) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

var frontendSkipDirs = map[string]bool{
	"node_modules": true,
	"dist":         true,
	"coverage":     true,
	".vite":        true,
	".cache":       true,
	".turbo":       true,
	".next":        true,
	"out":          true,
}

// FrontendInputs collects the files that influence a frontend production build.
// extraSkip may contain additional directories (typically the resolved dist
// directory) to exclude.
func FrontendInputs(cfg *config.Config, extraSkip ...string) ([]string, error) {
	frontendDir := cfg.Frontend.Dir
	if frontendDir == "" {
		frontendDir = "frontend"
	}
	if !filepath.IsAbs(frontendDir) {
		frontendDir = filepath.Join(cfg.RootDir, frontendDir)
	}

	skip := make(map[string]bool, len(frontendSkipDirs)+len(extraSkip))
	for k, v := range frontendSkipDirs {
		skip[k] = v
	}
	for _, dir := range extraSkip {
		if dir == "" {
			continue
		}
		if rel, err := filepath.Rel(frontendDir, dir); err == nil {
			rel = filepath.ToSlash(rel)
			if rel != "." && !strings.HasPrefix(rel, "..") {
				skip[rel] = true
			}
		}
	}

	files, err := WalkFiles(frontendDir, skip, func(rel string) bool {
		base := filepath.Base(rel)
		return !strings.HasSuffix(base, ".log") && !strings.HasSuffix(base, ".tmp")
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	for _, name := range []string{
		"package.json",
		"package-lock.json",
		"npm-shrinkwrap.json",
		"pnpm-lock.yaml",
		"yarn.lock",
		"bun.lock",
		"bun.lockb",
	} {
		for _, dir := range []string{frontendDir, cfg.RootDir} {
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				files = append(files, p)
			}
		}
	}
	return files, nil
}

// BindingsInputs collects Go and Wails configuration files that can change
// generated bindings.
func BindingsInputs(cfg *config.Config) ([]string, error) {
	skip := map[string]bool{
		".git":         true,
		"node_modules": true,
		"frontend":     true,
		"bin":          true,
		"vendor":       true,
		"dist":         true,
	}
	files, err := WalkFiles(cfg.RootDir, skip, func(rel string) bool {
		if strings.HasSuffix(rel, "_test.go") {
			return false
		}
		switch filepath.Ext(rel) {
		case ".go", ".mod", ".sum":
			return true
		}
		return false
	})
	if err != nil {
		return nil, err
	}

	for _, name := range []string{
		cfg.Wails.ConfigFile,
		filepath.Join("build", "config.yml"),
		filepath.Join("build", "config.yaml"),
	} {
		if name == "" {
			continue
		}
		p := name
		if !filepath.IsAbs(p) {
			p = filepath.Join(cfg.RootDir, p)
		}
		if _, err := os.Stat(p); err == nil {
			files = append(files, p)
		}
	}
	return files, nil
}

// BindingsOutputs returns the first existing generated-bindings directory.
func BindingsOutputs(cfg *config.Config) []string {
	dir := cfg.Frontend.Dir
	if dir == "" {
		dir = "frontend"
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(cfg.RootDir, dir)
	}
	for _, rel := range []string{
		"bindings",
		"wailsjs",
		filepath.Join("src", "bindings"),
		filepath.Join("src", "wailsjs"),
	} {
		p := filepath.Join(dir, rel)
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			return []string{p}
		}
	}
	return nil
}
