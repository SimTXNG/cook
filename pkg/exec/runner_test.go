package exec

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
)

func newPrefixWriter(w *bytes.Buffer) *prefixWriter {
	return &prefixWriter{mu: &sync.Mutex{}, w: w, prefix: "hook"}
}

func TestPrefixWriterOnlyPrefixesCompleteLines(t *testing.T) {
	var out bytes.Buffer
	pw := newPrefixWriter(&out)

	if n, err := pw.Write([]byte("foo")); n != 3 || err != nil {
		t.Fatalf("Write(foo) = (%d, %v), want (3, nil)", n, err)
	}
	if out.Len() != 0 {
		t.Fatalf("incomplete line written early: %q", out.String())
	}

	if n, err := pw.Write([]byte("bar\n")); n != 4 || err != nil {
		t.Fatalf("Write(bar\\n) = (%d, %v), want (4, nil)", n, err)
	}
	if got, want := out.String(), "[hook] foobar\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}

	if err := pw.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got, want := out.String(), "[hook] foobar\n"; got != want {
		t.Fatalf("output after Flush = %q, want %q", got, want)
	}
}

func TestPrefixWriterFlushPartialLineWithoutNewline(t *testing.T) {
	var out bytes.Buffer
	pw := newPrefixWriter(&out)

	if _, err := pw.Write([]byte("tail")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := pw.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got, want := out.String(), "[hook] tail"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}

	if err := pw.Flush(); err != nil {
		t.Fatalf("second Flush: %v", err)
	}
	if got, want := out.String(), "[hook] tail"; got != want {
		t.Fatalf("output after second Flush = %q, want %q", got, want)
	}
}

type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) { return 0, errors.New("boom") }

func TestPrefixWriterWriteConsumesBytesOnError(t *testing.T) {
	pw := &prefixWriter{mu: &sync.Mutex{}, w: failingWriter{}, prefix: "hook"}

	n, err := pw.Write([]byte("line\n"))
	if n != len("line\n") {
		t.Fatalf("Write returned n = %d, want %d", n, len("line\n"))
	}
	if err == nil {
		t.Fatal("Write returned nil error, want underlying error")
	}
}

func TestPrefixWriterSharedWriterIsRaceFree(t *testing.T) {
	var out bytes.Buffer
	mu := &sync.Mutex{}
	a := &prefixWriter{mu: mu, w: &out, prefix: "hook"}
	b := &prefixWriter{mu: mu, w: &out, prefix: "hook"}

	var wg sync.WaitGroup
	for _, tc := range []struct {
		pw   *prefixWriter
		data string
	}{
		{a, "a1\na2\n"},
		{b, "b1\nb2\n"},
	} {
		wg.Add(1)
		go func(pw *prefixWriter, data string) {
			defer wg.Done()
			for _, chunk := range []string{data[:2], data[2:]} {
				if _, err := pw.Write([]byte(chunk)); err != nil {
					t.Errorf("Write: %v", err)
				}
			}
		}(tc.pw, tc.data)
	}
	wg.Wait()
	if err := a.Flush(); err != nil {
		t.Fatalf("Flush a: %v", err)
	}
	if err := b.Flush(); err != nil {
		t.Fatalf("Flush b: %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4: %q", len(lines), out.String())
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "[hook] ") {
			t.Fatalf("line %q is not prefixed", line)
		}
	}
}

func TestBuildEnvReplacesDuplicateKeys(t *testing.T) {
	t.Setenv("CGO_ENABLED", "0")

	extra := map[string]string{"CGO_ENABLED": "1"}
	env := BuildEnv(extra)

	count := 0
	for _, entry := range env {
		if strings.HasPrefix(entry, "CGO_ENABLED=") {
			count++
			if got, want := entry, "CGO_ENABLED=1"; got != want {
				t.Fatalf("entry = %q, want %q", got, want)
			}
		}
	}
	if count != 1 {
		t.Fatalf("CGO_ENABLED appears %d times, want 1", count)
	}
	if got, want := extra["CGO_ENABLED"], "1"; got != want {
		t.Fatalf("caller map mutated: %q", got)
	}
}
