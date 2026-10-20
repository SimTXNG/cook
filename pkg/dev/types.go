package dev

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// DevOptions contains configuration options for cook dev mode.
type DevOptions struct {
	Port          int
	Host          string
	Secure        bool
	Debounce      time.Duration
	SkipBindings  bool
	SkipFrontend  bool
	SkipBackend   bool
	ExtraTags     []string
	GCFlags       string
	SkipUnchanged bool
	Force         bool
	Env           map[string]string
}

// DefaultDevOptions returns default development options.
func DefaultDevOptions() DevOptions {
	return DevOptions{
		Port:     9245,
		Host:     "127.0.0.1",
		Debounce: 150 * time.Millisecond,
	}
}

// ANSI color codes for terminal logging
const (
	ColorReset   = "\033[0m"
	ColorBold    = "\033[1m"
	ColorRed     = "\033[31m"
	ColorGreen   = "\033[32m"
	ColorMagenta = "\033[35m"
	ColorCyan    = "\033[36m"
	ColorGray    = "\033[90m"
)

// Printer coordinates synchronized, colored prefix output to the terminal.
type Printer struct {
	mu  sync.Mutex
	out io.Writer
}

var defaultPrinter = &Printer{out: os.Stdout}

func (p *Printer) Printf(prefixColor, prefix, format string, args ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	timestamp := time.Now().Format("15:04:05")
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(p.out, "%s%s%s %s[%s]%s %s\n", ColorGray, timestamp, ColorReset, prefixColor, prefix, ColorReset, msg)
}

func LogCook(format string, args ...any) {
	defaultPrinter.Printf(ColorBold+ColorCyan, "cook", format, args...)
}

func LogVite(format string, args ...any) {
	defaultPrinter.Printf(ColorMagenta, "vite", format, args...)
}

func LogBackend(format string, args ...any) {
	defaultPrinter.Printf(ColorGreen, "backend", format, args...)
}

func LogError(prefix, format string, args ...any) {
	defaultPrinter.Printf(ColorBold+ColorRed, prefix, format, args...)
}

func LogSuccess(format string, args ...any) {
	defaultPrinter.Printf(ColorBold+ColorGreen, "cook", format, args...)
}
