package testdrive

import (
	"slices"

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
		_, module := onboard.PythonModule(words[i+1:])
		options, ok := onboard.PythonInterpreterOptions(words[i+1:])
		if !ok || module[0] != "pytest" {
			continue
		}
		// Isolated (-I) and -E interpreters ignore PYTHONPATH, so a bootstrap
		// adds the observer instead, after the same options.
		if slices.Contains(options, "-I") || slices.Contains(options, "-E") {
			prefix := append(slices.Clone(words[:i+1]), options...)
			prefix = append(prefix, "-c", pytestObserverBootstrap, observer, tracer)
			return append(prefix, module[1:]...)[1:]
		}
		return args
	}
	return args
}
