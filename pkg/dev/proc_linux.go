//go:build linux

package dev

import (
	"os/exec"
	"syscall"
)

func setSysProcAttr(cmd *exec.Cmd) {
	// Pdeathsig is deliberately not used: Go may retire the OS thread that
	// forked the child, which would kill a healthy process. We rely instead on
	// context cancellation and killProcessTree for explicit cleanup.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
}
