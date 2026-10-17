//go:build linux || darwin || freebsd

package oneyaml_test

import (
	"bytes"
	"os"
	"runtime"
	"strconv"
	"syscall"
)

// peakRSSMB returns the peak resident set size of an exited child in MB.
// Maxrss is in KiB on Linux and FreeBSD and in bytes on macOS.
func peakRSSMB(ps *os.ProcessState) (float64, bool) {
	ru, ok := ps.SysUsage().(*syscall.Rusage)
	if !ok || ru == nil {
		return 0, false
	}
	if runtime.GOOS == "darwin" {
		return float64(ru.Maxrss) / (1 << 20), true
	}
	return float64(ru.Maxrss) / 1024, true
}

// ownPeakKB reads the process's own high-water resident size on Linux.
func ownPeakKB() (int64, bool) {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, false
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		if rest, ok := bytes.CutPrefix(line, []byte("VmHWM:")); ok {
			f := bytes.Fields(rest)
			if len(f) > 0 {
				if kb, err := strconv.ParseInt(string(f[0]), 10, 64); err == nil {
					return kb, true
				}
			}
		}
	}
	return 0, false
}
