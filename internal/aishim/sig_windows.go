//go:build windows

package aishim

import "os"

func termSignal() os.Signal { return os.Kill }
