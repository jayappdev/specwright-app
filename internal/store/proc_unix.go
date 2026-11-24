//go:build !windows

package store

import "syscall"

// processAlive reports whether a process with this id is running.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
