package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCopyContentsAndMode(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.txt")
	if err := os.WriteFile(src, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "nested", "dst.txt")

	if err := Copy(src, dst, 0640); err != nil {
		t.Fatalf("Copy failed: %v", err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("expected contents hello, got %q", data)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0640 {
		t.Errorf("expected mode 0640, got %v", fi.Mode().Perm())
	}
}

func TestCopyExecutable(t *testing.T) {
	src := filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(src, []byte("#!/bin/sh\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "copy")

	if err := CopyExecutable(src, dst); err != nil {
		t.Fatalf("CopyExecutable failed: %v", err)
	}

	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0755 {
		t.Errorf("expected mode 0755, got %v", fi.Mode().Perm())
	}
}

func TestCopyOverwritesAndTruncates(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.txt")
	if err := os.WriteFile(src, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	dstDir := t.TempDir()
	dst := filepath.Join(dstDir, "dst.txt")
	if err := os.WriteFile(dst, []byte("much longer old content"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := Copy(src, dst, 0644); err != nil {
		t.Fatalf("Copy failed: %v", err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Errorf("expected truncated contents %q, got %q", "new", data)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0644 {
		t.Errorf("expected mode 0644, got %v", fi.Mode().Perm())
	}
}

func TestCopyDirPreservesModesAndNesting(t *testing.T) {
	src := t.TempDir()
	if err := os.Chmod(src, 0750); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(src, "a", "b")
	if err := os.MkdirAll(sub, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(src, "a"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0700); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(sub, "tool")
	if err := os.WriteFile(exe, []byte("bin"), 0755); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(src, "a", "note.txt")
	if err := os.WriteFile(data, []byte("data"), 0640); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "copy")
	if err := CopyDir(src, dst); err != nil {
		t.Fatalf("CopyDir failed: %v", err)
	}

	for _, tc := range []struct {
		path string
		mode os.FileMode
	}{
		{dst, 0750},
		{filepath.Join(dst, "a"), 0755},
		{filepath.Join(dst, "a", "b"), 0700},
		{filepath.Join(dst, "a", "b", "tool"), 0755},
		{filepath.Join(dst, "a", "note.txt"), 0640},
	} {
		fi, err := os.Stat(tc.path)
		if err != nil {
			t.Fatalf("stat %s: %v", tc.path, err)
		}
		if fi.Mode().Perm() != tc.mode {
			t.Errorf("%s: expected mode %v, got %v", tc.path, tc.mode, fi.Mode().Perm())
		}
	}

	got, err := os.ReadFile(filepath.Join(dst, "a", "note.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "data" {
		t.Errorf("expected contents data, got %q", got)
	}
}

func TestCopyDirPreservesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require elevated privileges on Windows")
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "target"), []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", filepath.Join(src, "dangling")); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "copy")
	if err := CopyDir(src, dst); err != nil {
		t.Fatalf("CopyDir failed: %v", err)
	}

	for _, name := range []string{"link", "dangling"} {
		fi, err := os.Lstat(filepath.Join(dst, name))
		if err != nil {
			t.Fatalf("lstat %s: %v", name, err)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s is not a symlink: %v", name, fi.Mode())
		}
	}

	got, err := os.Readlink(filepath.Join(dst, "link"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "target" {
		t.Errorf("expected link target %q, got %q", "target", got)
	}
}
