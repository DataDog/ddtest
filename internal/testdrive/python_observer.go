package testdrive

import (
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
)

// Isolated interpreters ignore PYTHONPATH. Add only our owned observer and
// tracer explicitly, then enter pytest with the original interpreter flags.
const pytestObserverBootstrap = `import os, runpy, sys
sys.dont_write_bytecode = True
observer, tracer = sys.argv[1:3]
sys.argv = sys.argv[:1] + sys.argv[3:]
if sys.path and sys.path[0] == "":
    sys.path[0] = os.getcwd()
sys.path[:0] = [path for path in (observer, tracer) if path]
runpy.run_module("pytest", run_name="__main__", alter_sys=True)
`

func pytestObserverArguments(command string, args []string, observer, tracer string) []string {
	words := append([]string{command}, args...)
	for i, word := range words {
		if !platform.IsPythonExecutable(word) {
			continue
		}
		index, module := onboard.PythonModule(words[i+1:])
		if index < 0 || module[0] != "pytest" {
			continue
		}
		flags := words[i+1 : i+1+index]
		for j := 0; j < len(flags); j++ {
			flag := flags[j]
			if slices.Contains([]string{"-W", "-X", "--check-hash-based-pycs"}, flag) {
				j++
				continue
			}
			if strings.HasPrefix(flag, "-W") || strings.HasPrefix(flag, "-X") {
				continue
			}
			if strings.HasPrefix(flag, "-") && strings.ContainsAny(flag[1:], "IE") {
				prefix := slices.Clone(words[:i+1+index])
				prefix = append(prefix, "-c", pytestObserverBootstrap, observer, tracer)
				return append(prefix, module[1:]...)[1:]
			}
		}
		return args
	}
	return args
}
