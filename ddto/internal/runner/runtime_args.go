package runner

import (
	"fmt"
	"strings"
)

// ParseRuntimeArgs consumes CLI runtime options before Go prepares a build.
// Go flag values and the custom test-argument tail are passed through unchanged.
// Put runtime options before custom test flags, -args or --.
func ParseRuntimeArgs(args []string) (Runtime, []string, error) {
	runtime := Mini
	goArgs := make([]string, 0, len(args))
	for len(args) > 0 {
		arg := args[0]
		name, value, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		if arg == "--runtime" || strings.HasPrefix(arg, "--runtime=") {
			width := 1
			if !hasValue {
				if len(args) < 2 {
					return "", nil, fmt.Errorf("--runtime needs a value: mini or sdk")
				}
				value, width = args[1], 2
			}
			runtime = Runtime(value)
			if runtime != Mini && runtime != SDK {
				return "", nil, fmt.Errorf("runtime must be sdk or mini, got %q", value)
			}
			args = args[width:]
			continue
		}
		width := 1
		if strings.HasPrefix(arg, "-") && arg != "-" {
			_, spec, known := lookupFlag(name)
			if !known || arg == "--" {
				// The CLI does not own custom flags or their values. Callers
				// can also use -args/-- to pass a test flag named runtime.
				goArgs = append(goArgs, args...)
				break
			}
			if spec.kind == valueFlag && !hasValue && len(args) > 1 {
				width = 2
			}
		}
		goArgs = append(goArgs, args[:width]...)
		args = args[width:]
	}
	return runtime, goArgs, nil
}
