//go:build linux

package main

import (
	"fmt"
	"runtime"
	"strings"
	"syscall"
)

// getSystemInfo 检测系统内核版本和架构 (Linux specific)
func getSystemInfo() (kernelVersion, architecture string, err error) {
	var uname syscall.Utsname
	if err := syscall.Uname(&uname); err != nil {
		debugPrint("Failed to get system info using Uname: %v. Using runtime.GOARCH.", err)
		return "", runtime.GOARCH, fmt.Errorf("failed to get system info using Uname, using runtime.GOARCH: %w", err)
	}

	kernelVersion = strings.Trim(string(bytesToString(uname.Release[:])), "\x00")
	architecture = strings.Trim(string(bytesToString(uname.Machine[:])), "\x00")

	debugPrint("Uname Release: %s, Machine: %s", kernelVersion, architecture)

	return kernelVersion, architecture, nil
}
