//go:build darwin

package main

import (
	"runtime"
)

// getSystemInfo 检测系统内核版本和架构 (Darwin specific)
func getSystemInfo() (kernelVersion, architecture string, err error) {
	debugPrint("Running on Darwin, using runtime.GOOS and runtime.GOARCH.")
	// On Darwin, syscall.Uname might not be available or reliable with CGO_ENABLED=0.
	// Use runtime.GOOS and runtime.GOARCH directly.
	return "", runtime.GOARCH, nil // kernelVersion might not be easily available without cgo
}
