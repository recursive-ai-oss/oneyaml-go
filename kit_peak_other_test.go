//go:build !(linux || darwin || freebsd)

package oneyaml_test

import "os"

// peakRSSMB reports no peak where the OS gives none through SysUsage.
func peakRSSMB(*os.ProcessState) (float64, bool) { return 0, false }

func ownPeakKB() (int64, bool) { return 0, false }
