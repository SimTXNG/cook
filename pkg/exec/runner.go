package exec

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Runner manages process execution with streaming logs and environment variable injection.
type Runner struct {
	Stdout io.Writer
	Stderr io.Writer
	Prefix string
}

func NewRunner(prefix string) *Runner {
	return &Runner{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Prefix: prefix,
	}
}

// Run executes a shell command string within the given directory and environment.
func (r *Runner) Run(ctx context.Context, cmdStr string, dir string, extraEnv map[string]string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd.exe", "/c", cmdStr)
	} else {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", cmdStr)
	}
	return r.runCmd(ctx, cmd, dir, extraEnv, strconv.Quote(cmdStr))
}

// RunArgs executes a binary directly with arguments (no shell interpolation).
func (r *Runner) RunArgs(ctx context.Context, name string, args []string, dir string, extraEnv map[string]string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	return r.runCmd(ctx, cmd, dir, extraEnv, name+" "+strings.Join(args, " "))
}

// runCmd wires the command's directory, environment and output streams, runs it,
// flushes buffered output and wraps any failure with the given label.
func (r *Runner) runCmd(ctx context.Context, cmd *exec.Cmd, dir string, extraEnv map[string]string, label string) error {
	cmd.Dir = dir
	cmd.Env = buildEnv(extraEnv)

	stdout, stderr, flush := r.streams()
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	runErr := cmd.Run()
	if flushErr := flush(); runErr == nil {
		runErr = flushErr
	}
	if runErr != nil {
		return fmt.Errorf("command %s failed in %s: %w", label, dir, runErr)
	}
	return nil
}

// buildEnv merges extraEnv into the current environment, replacing existing
// keys rather than appending duplicates.
func buildEnv(extraEnv map[string]string) []string {
	base := os.Environ()
	if len(extraEnv) == 0 {
		return base
	}

	env := make([]string, 0, len(base)+len(extraEnv))
	for _, entry := range base {
		key := entry
		if i := strings.IndexByte(entry, '='); i > 0 {
			key = entry[:i]
		}
		if _, ok := extraEnv[key]; ok {
			continue
		}
		env = append(env, entry)
	}

	keys := make([]string, 0, len(extraEnv))
	for k := range extraEnv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+extraEnv[k])
	}
	return env
}

// streams returns the writers for stdout and stderr plus a function that flushes
// any buffered partial lines once the command has finished.
func (r *Runner) streams() (io.Writer, io.Writer, func() error) {
	if r.Prefix == "" {
		return r.Stdout, r.Stderr, func() error { return nil }
	}

	mu := &sync.Mutex{}
	stdout := &prefixWriter{mu: mu, w: r.Stdout, prefix: r.Prefix}
	stderr := &prefixWriter{mu: mu, w: r.Stderr, prefix: r.Prefix}
	return stdout, stderr, func() error {
		if err := stdout.Flush(); err != nil {
			return err
		}
		return stderr.Flush()
	}
}

type prefixWriter struct {
	mu     *sync.Mutex
	w      io.Writer
	prefix string
	buf    []byte
}

// Write buffers p until complete lines are available, then prefixes each line.
// It always reports len(p) consumed so callers never resend bytes.
func (pw *prefixWriter) Write(p []byte) (n int, err error) {
	pw.mu.Lock()
	defer pw.mu.Unlock()

	pw.buf = append(pw.buf, p...)
	return len(p), pw.flushLocked(false)
}

// Flush writes any remaining unterminated line so it is not lost.
func (pw *prefixWriter) Flush() error {
	pw.mu.Lock()
	defer pw.mu.Unlock()

	return pw.flushLocked(true)
}

func (pw *prefixWriter) flushLocked(final bool) error {
	for {
		i := bytes.IndexByte(pw.buf, '\n')
		if i < 0 {
			break
		}
		if err := pw.writeLine(pw.buf[:i]); err != nil {
			return err
		}
		pw.buf = pw.buf[i+1:]
	}

	if final && len(pw.buf) > 0 {
		if err := pw.writePrefixed(pw.buf, false); err != nil {
			return err
		}
		pw.buf = pw.buf[:0]
	}
	return nil
}

func (pw *prefixWriter) writeLine(line []byte) error {
	return pw.writePrefixed(line, true)
}

func (pw *prefixWriter) writePrefixed(line []byte, newline bool) error {
	suffix := ""
	if newline {
		suffix = "\n"
	}
	out := fmt.Sprintf("[%s] %s%s", pw.prefix, line, suffix)
	n, err := pw.w.Write([]byte(out))
	if err != nil {
		return err
	}
	if n < len(out) {
		return io.ErrShortWrite
	}
	return nil
}
