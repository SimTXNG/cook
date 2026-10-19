package dev

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/simtxng/cook/pkg/config"
)

// FrontendServer manages the frontend dev server process.
type FrontendServer struct {
	cfg        *config.Config
	port       int
	secure     bool
	mu         sync.Mutex
	cmd        *exec.Cmd
	cancelFunc context.CancelFunc
	done       chan struct{}
}

// NewFrontendServer creates a new FrontendServer instance.
func NewFrontendServer(cfg *config.Config, port int, secure ...bool) *FrontendServer {
	f := &FrontendServer{
		cfg:  cfg,
		port: port,
	}
	if len(secure) > 0 {
		f.secure = secure[0]
	}
	return f
}

// Start launches the frontend development server in the background.
func (f *FrontendServer) Start(ctx context.Context) error {
	frontendDir := f.cfg.Frontend.Dir
	if frontendDir == "" {
		frontendDir = "frontend"
	}
	if !filepath.IsAbs(frontendDir) {
		frontendDir = filepath.Join(f.cfg.RootDir, frontendDir)
	}
	if abs, err := filepath.Abs(frontendDir); err == nil {
		frontendDir = abs
	}

	if fi, err := os.Stat(frontendDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("frontend directory not found at %s", frontendDir)
	}

	pm := f.cfg.Frontend.PackageManager
	if pm == "" {
		pm = detectPackageManager(frontendDir)
	}

	// Ensure dependencies are installed
	nodeModules := filepath.Join(frontendDir, "node_modules")
	if _, err := os.Stat(nodeModules); os.IsNotExist(err) {
		LogCook("Installing frontend dependencies using %s...", pm)
		installCmd := shellCommandContext(ctx, config.DefaultInstallCmd(pm))
		installCmd.Dir = frontendDir
		installCmd.Stdout = os.Stdout
		installCmd.Stderr = os.Stderr
		if err := installCmd.Run(); err != nil {
			return fmt.Errorf("failed to install frontend dependencies: %w", err)
		}
		LogSuccess("Frontend dependencies installed.")
	}

	// Prepare command
	var devExec string
	var devArgs []string
	customDevCmd := strings.TrimSpace(f.cfg.Frontend.DevCmd)

	if customDevCmd == "" {
		// Fast path: if node and vite are directly in node_modules, run node directly!
		// This bypasses npm/sh shell wrappers, prevents process leaking, and starts instantly.
		nodeBin, errNode := exec.LookPath("node")
		viteJs := filepath.Join(frontendDir, "node_modules", "vite", "bin", "vite.js")
		if absVite, err := filepath.Abs(viteJs); err == nil {
			viteJs = absVite
		}
		if errNode == nil {
			if _, err := os.Stat(viteJs); err == nil {
				devExec = nodeBin
				devArgs = []string{viteJs, "--port", fmt.Sprintf("%d", f.port), "--strictPort"}
			}
		}

		if devExec == "" {
			devExec = pm
			devArgs = []string{"run", "dev", "--", "--port", fmt.Sprintf("%d", f.port), "--strictPort"}
		}
	}

	serverCtx, cancel := context.WithCancel(ctx)
	var cmd *exec.Cmd
	if customDevCmd != "" {
		cmd = shellCommandContext(serverCtx, customDevCmd)
	} else {
		cmd = exec.CommandContext(serverCtx, devExec, devArgs...)
	}
	cmd.Dir = frontendDir

	// Setup process group so the whole tree can be terminated on shutdown.
	setSysProcAttr(cmd)

	// Inject environment variables
	cmd.Env = append(append([]string(nil), os.Environ()...),
		fmt.Sprintf("WAILS_VITE_PORT=%d", f.port),
		fmt.Sprintf("PORT=%d", f.port),
		"VITE_HMR=true",
	)

	return startProcess(cmd, cancel, LogVite, "vite", nil, func(done chan struct{}) {
		f.mu.Lock()
		f.cmd = cmd
		f.cancelFunc = cancel
		f.done = done
		f.mu.Unlock()
	}, nil)
}

// WaitReady polls the frontend dev server until it responds or timeout is reached.
func (f *FrontendServer) WaitReady(ctx context.Context, timeout time.Duration) error {
	scheme := "http"
	if f.secure {
		scheme = "https"
	}
	url := fmt.Sprintf("%s://127.0.0.1:%d", scheme, f.port)

	probeCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		probeCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	transport := &http.Transport{}
	if f.secure {
		// Local dev servers use self-signed certificates; this is a readiness probe only.
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	client := &http.Client{Transport: transport}
	dialer := &net.Dialer{Timeout: 100 * time.Millisecond}

	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()

	for {
		if f.probeReady(probeCtx, client, dialer, url) {
			return nil
		}

		select {
		case <-probeCtx.Done():
			return fmt.Errorf("frontend dev server at %s did not become ready: %w", url, probeCtx.Err())
		case <-ticker.C:
		}
	}
}

func (f *FrontendServer) probeReady(ctx context.Context, client *http.Client, dialer *net.Dialer, url string) bool {
	conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", f.port))
	if err != nil {
		return false
	}
	_ = conn.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// Stop terminates the frontend server and all child processes.
func (f *FrontendServer) Stop() {
	f.mu.Lock()
	cmd := f.cmd
	done := f.done
	cancel := f.cancelFunc
	f.cmd = nil
	f.done = nil
	f.cancelFunc = nil
	f.mu.Unlock()

	stopProcess(cmd, cancel, done)
}

func detectPackageManager(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, "bun.lockb")); err == nil {
		return "bun"
	}
	if _, err := os.Stat(filepath.Join(dir, "bun.lock")); err == nil {
		return "bun"
	}
	if _, err := os.Stat(filepath.Join(dir, "pnpm-lock.yaml")); err == nil {
		return "pnpm"
	}
	if _, err := os.Stat(filepath.Join(dir, "yarn.lock")); err == nil {
		return "yarn"
	}
	return "npm"
}
