//go:build unix

package main

import (
	"os"
	"runtime"
	"syscall"
)

const hasPeakRSS = true

func peakRSS(state *os.ProcessState) int64 {
	usage := state.SysUsage().(*syscall.Rusage)
	rss := int64(usage.Maxrss)
	if runtime.GOOS == "linux" {
		rss *= 1024 // kilobytes on Linux, bytes on macOS
	}
	return rss
}
