//go:build !linux && !darwin && !freebsd

package main

import (
	"fmt"
	"runtime"
)

// getSystemInfo 检测系统内核版本和架构 (Fallback for other OS)
func getSystemInfo() (kernelVersion, architecture string, err error) {
	debugPrint("Running on an unsupported OS for syscall.Uname, using runtime.GOOS and runtime.GOARCH.")
	// For other OS, use runtime.GOOS and runtime.GOARCH directly.
	return "", runtime.GOARCH, fmt.Errorf("syscall.Uname is not supported on this OS, using runtime.GOARCH")
}
