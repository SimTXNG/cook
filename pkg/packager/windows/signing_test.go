package windows

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/simtxng/cook/pkg/config"
)

func TestIsSigningConfigured(t *testing.T) {
	cfg := &config.Config{}
	if IsSigningConfigured(cfg) {
		t.Errorf("expected signing not configured for empty config")
	}

	cfg.Windows.Signing.Certificate = "cert.pfx"
	if !IsSigningConfigured(cfg) {
		t.Errorf("expected signing configured when certificate is provided")
	}

	cfg.Windows.Signing.Certificate = ""
	cfg.Windows.Signing.Thumbprint = "abcdef123456"
	if !IsSigningConfigured(cfg) {
		t.Errorf("expected signing configured when thumbprint is provided")
	}
}

func TestBuildSignToolArgs(t *testing.T) {
	cfg := &config.Config{
		RootDir: "/path/to/project",
		Windows: config.WindowsConfig{
			Signing: config.WindowsSignConfig{
				Certificate:     "certs/cert.pfx",
				TimestampServer: "http://timestamp.custom.com",
				Password:        "s3cret",
			},
		},
	}

	args, err := BuildSignToolArgs(cfg, "bin/app.exe")
	if err != nil {
		t.Fatalf("BuildSignToolArgs failed: %v", err)
	}
	joined := strings.Join(args, " ")

	if strings.Contains(joined, "/sha1") {
		t.Errorf("must not emit both /f and /sha1 in %s", joined)
	}
	if !strings.Contains(joined, "sign /fd SHA256") {
		t.Errorf("missing sign /fd SHA256 in %s", joined)
	}
	if !strings.Contains(joined, "/tr http://timestamp.custom.com /td SHA256") {
		t.Errorf("missing timestamp server in %s", joined)
	}
	if !strings.Contains(joined, "/f /path/to/project/certs/cert.pfx") {
		t.Errorf("missing certificate path in %s", joined)
	}
	if !strings.Contains(joined, "/p s3cret") {
		t.Errorf("missing password in %s", joined)
	}
	if !strings.HasSuffix(joined, "bin/app.exe") {
		t.Errorf("missing target file at end of %s", joined)
	}
}

func TestBuildSignToolArgsThumbprint(t *testing.T) {
	cfg := &config.Config{
		RootDir: "/path/to/project",
		Windows: config.WindowsConfig{
			Signing: config.WindowsSignConfig{
				Thumbprint: "ABCDEF123456",
			},
		},
	}

	args, err := BuildSignToolArgs(cfg, "bin/app.exe")
	if err != nil {
		t.Fatalf("BuildSignToolArgs failed: %v", err)
	}
	joined := strings.Join(args, " ")

	if strings.Contains(joined, "/f ") {
		t.Errorf("thumbprint signing must not emit /f in %s", joined)
	}
	if !strings.Contains(joined, "/sha1 ABCDEF123456") {
		t.Errorf("missing /sha1 thumbprint in %s", joined)
	}
}

func TestBuildSignToolArgsNeither(t *testing.T) {
	cfg := &config.Config{RootDir: "/path/to/project"}
	if _, err := BuildSignToolArgs(cfg, "bin/app.exe"); err == nil {
		t.Fatalf("expected error when neither certificate nor thumbprint is configured")
	}
}

func TestBuildOsslsigncodeArgs(t *testing.T) {
	cfg := &config.Config{
		RootDir: "/path/to/project",
		Windows: config.WindowsConfig{
			Signing: config.WindowsSignConfig{
				Certificate: "certs/cert.pfx",
				Password:    "s3cret",
			},
		},
	}

	args := BuildOsslsigncodeArgs(cfg, "bin/app.exe", "bin/app.exe.signed")
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "sign -h sha256") {
		t.Errorf("missing sign -h sha256 in %s", joined)
	}
	if !strings.Contains(joined, "-pkcs12 /path/to/project/certs/cert.pfx") {
		t.Errorf("missing pkcs12 cert in %s", joined)
	}
	if !strings.Contains(joined, "-pass s3cret") {
		t.Errorf("missing non-interactive passphrase in %s", joined)
	}
	if !strings.Contains(joined, "-in bin/app.exe -out bin/app.exe.signed") {
		t.Errorf("missing in/out in %s", joined)
	}
}

func TestReplaceFileOverExisting(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cook-replace-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	target := filepath.Join(tmpDir, "app.exe")
	tmp := filepath.Join(tmpDir, "app.exe.signed")
	if err := os.WriteFile(target, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := replaceFile(tmp, target); err != nil {
		t.Fatalf("replaceFile failed: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("expected replaced content %q, got %q", "new", string(got))
	}
	if fileExists(tmp) {
		t.Errorf("temp signed file should have been consumed")
	}
}
