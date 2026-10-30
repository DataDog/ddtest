package runner

import "os"

// execBazelCompiler runs the real compiler with argv; Windows has no exec.
func execBazelCompiler(real string, argv []string) int {
	return runBazelCompiler(real, argv, os.Stderr)
}
