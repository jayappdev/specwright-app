//go:build windows

package core

import (
	"os/exec"
	"syscall"
)

// detach starts cmd apart from this process, so it outlives it.
func detach(cmd *exec.Cmd) {
	const detachedProcess, newProcessGroup = 0x00000008, 0x00000200
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | newProcessGroup}
}
