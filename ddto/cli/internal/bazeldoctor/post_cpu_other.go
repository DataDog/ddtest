//go:build !unix

package bazeldoctor

// processCPU is unknown here; post-process runs fall back on Windows.
func processCPU() [2]float64 { return [2]float64{} }
