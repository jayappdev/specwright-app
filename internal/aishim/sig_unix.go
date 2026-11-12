//go:build !windows

package aishim

import (
	"os"
	"syscall"
)

func termSignal() os.Signal { return syscall.SIGTERM }
