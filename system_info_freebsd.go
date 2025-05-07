//go:build freebsd

package main

import "runtime"

// getSystemInfo 检测系统内核版本和架构 (FreeBSD)
func getSystemInfo() (kernelVersion, architecture string, err error) {
	debugPrint("Running on FreeBSD, using runtime.GOOS and runtime.GOARCH.")
	return "", runtime.GOARCH, nil
}
