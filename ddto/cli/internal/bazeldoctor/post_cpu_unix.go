//go:build unix

package bazeldoctor

import "syscall"

// processCPU is the process's user and system CPU seconds.
func processCPU() [2]float64 {
	var usage syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &usage) != nil {
		return [2]float64{}
	}
	return [2]float64{float64(usage.Utime.Sec) + float64(usage.Utime.Usec)/1e6,
		float64(usage.Stime.Sec) + float64(usage.Stime.Usec)/1e6}
}
