package exec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/simtxng/cook/pkg/config"
)

// HookManager manages the execution of lifecycle hooks and fix-scripts.
type HookManager struct {
	// Runner executes every hook and fix script. All stages share this runner,
	// so its Prefix, Stdout and Stderr apply to all hook output.
	Runner *Runner
	Config *config.Config
}

func NewHookManager(cfg *config.Config) *HookManager {
	return &HookManager{
		Runner: NewRunner("hook"),
		Config: cfg,
	}
}

// runner returns the manager's runner, or a default one when it is unset.
func (hm *HookManager) runner() *Runner {
	if hm.Runner != nil {
		return hm.Runner
	}
	return NewRunner("hook")
}

// BuildEnv constructs standard environment variables provided to all hooks and scripts.
func (hm *HookManager) BuildEnv(extra map[string]string) map[string]string {
	env := map[string]string{
		"APP_NAME": hm.Config.Package.Name,
		"ROOT_DIR": hm.Config.RootDir,
		"BIN_DIR":  filepath.Join(hm.Config.RootDir, hm.Config.Package.OutputDir),
	}

	if hm.Config.Version != nil {
		env["APP_VERSION"] = hm.Config.Version.Raw
		env["PKG_VERSION"] = hm.Config.Version.Debian
		env["APP_COMMIT"] = hm.Config.Version.Commit
		env["FULL_VERSION"] = hm.Config.Version.FullVersion()
	}

	for k, v := range extra {
		env[k] = v
	}
	return env
}

// RunPreBuild executes all pre-build hooks.
func (hm *HookManager) RunPreBuild(ctx context.Context) error {
	return hm.runCommands(ctx, "pre-build", hm.Config.Hooks.PreBuild, nil)
}

// RunPostBuild executes all post-build hooks.
func (hm *HookManager) RunPostBuild(ctx context.Context) error {
	return hm.runCommands(ctx, "post-build", hm.Config.Hooks.PostBuild, nil)
}

// RunPrePackage executes all pre-package hooks.
func (hm *HookManager) RunPrePackage(ctx context.Context, target string) error {
	return hm.runCommands(ctx, "pre-package:"+target, hm.Config.Hooks.PrePackage, map[string]string{
		"TARGET": target,
	})
}

// RunPostPackage executes all post-package hooks.
func (hm *HookManager) RunPostPackage(ctx context.Context, target string) error {
	return hm.runCommands(ctx, "post-package:"+target, hm.Config.Hooks.PostPackage, map[string]string{
		"TARGET": target,
	})
}

// RunFixScript executes a named fix script (from hooks.fix_scripts or direct path) with specified arguments.
func (hm *HookManager) RunFixScript(ctx context.Context, scriptNameOrPath string, args []string, extraEnv map[string]string) error {
	scriptPath := scriptNameOrPath
	if mapped, ok := hm.Config.Hooks.FixScripts[scriptNameOrPath]; ok {
		scriptPath = mapped
	}

	if !filepath.IsAbs(scriptPath) {
		scriptPath = filepath.Join(hm.Config.RootDir, scriptPath)
	}

	if _, err := os.Stat(scriptPath); err != nil {
		return fmt.Errorf("fix script %s not found: %w", scriptPath, err)
	}

	env := hm.BuildEnv(extraEnv)

	// Detect if it's a bash/sh script
	var cmdName string
	var finalArgs []string
	if strings.HasSuffix(scriptPath, ".sh") {
		cmdName = "bash"
		finalArgs = append([]string{scriptPath}, args...)
	} else if strings.HasSuffix(scriptPath, ".py") {
		cmdName = "python3"
		finalArgs = append([]string{scriptPath}, args...)
	} else {
		cmdName = scriptPath
		finalArgs = args
	}

	r := hm.runner()
	return r.RunArgs(ctx, cmdName, finalArgs, hm.Config.RootDir, env)
}

func (hm *HookManager) runCommands(ctx context.Context, stage string, cmds []string, extraEnv map[string]string) error {
	if len(cmds) == 0 {
		return nil
	}
	env := hm.BuildEnv(extraEnv)
	r := hm.runner()
	for _, cmd := range cmds {
		fmt.Printf("==> Executing %s hook: %s\n", stage, cmd)
		if err := r.Run(ctx, cmd, hm.Config.RootDir, env); err != nil {
			return fmt.Errorf("hook %s failed: %w", stage, err)
		}
	}
	return nil
}
