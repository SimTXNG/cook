//go:build windows

package dev

import (
	"fmt"
	"os/exec"
)

func setSysProcAttr(cmd *exec.Cmd) {
	// Windows default process creation
}

func killProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return
	}
	// Terminate process tree forcefully on Windows
	killCmd := exec.Command("taskkill", "/T", "/F", "/PID", fmt.Sprintf("%d", pid))
	_ = killCmd.Run()
	_ = cmd.Process.Kill()
}
