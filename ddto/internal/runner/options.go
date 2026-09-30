package runner

import (
	"fmt"
	"strings"
)

type options struct {
	packages, buildFlags []string
	overlay              string
}

var buildBool = words("race msan asan cover trimpath buildvcs v a n x work")
var buildValue = words("tags mod modfile overlay p gcflags ldflags asmflags gccgoflags compiler covermode coverpkg pgo pkgdir installsuffix")
var testBool = words("json failfast fullpath short c")
var testValue = words("run skip bench benchtime count timeout parallel shuffle cpu list o coverprofile outputdir cpuprofile memprofile memprofilerate blockprofile blockprofilerate mutexprofile mutexprofilefraction trace fuzz fuzztime fuzzminimizetime")

func words(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

// splitFlags implements GOFLAGS quoting without invoking a shell.
func splitFlags(s string) ([]string, error) {
	var out []string
	for len(strings.TrimSpace(s)) > 0 {
		s = strings.TrimSpace(s)
		var b strings.Builder
		quote := byte(0)
		i := 0
		for ; i < len(s); i++ {
			c := s[i]
			if quote != 0 {
				if c == quote {
					quote = 0
				} else {
					b.WriteByte(c)
				}
				continue
			}
			if c == '\'' || c == '"' {
				quote = c
				continue
			}
			if c == ' ' || c == '\t' || c == '\n' {
				break
			}
			b.WriteByte(c)
		}
		if quote != 0 {
			return nil, fmt.Errorf("unterminated GOFLAGS quote")
		}
		out = append(out, b.String())
		s = s[i:]
	}
	return out, nil
}
func parseOptions(args []string, goflags string) (options, error) {
	var o options
	envFlags, err := splitFlags(goflags)
	if err != nil {
		return o, err
	}
	for _, f := range envFlags {
		name, value, _ := strings.Cut(strings.TrimLeft(f, "-"), "=")
		if name == "toolexec" {
			return o, fmt.Errorf("toolexec conflicts with overlay instrumentation")
		}
		if name == "overlay" {
			o.overlay = value
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-args" {
			break
		}
		if !strings.HasPrefix(a, "-") {
			if strings.HasSuffix(a, ".go") {
				return o, fmt.Errorf("POC supports package mode, not explicit Go files")
			}
			o.packages = append(o.packages, a)
			continue
		}
		name, value, assigned := strings.Cut(strings.TrimLeft(a, "-"), "=")
		name = strings.TrimPrefix(name, "test.")
		if name == "toolexec" {
			return o, fmt.Errorf("toolexec conflicts with overlay instrumentation")
		}
		if name == "C" {
			return o, fmt.Errorf("run ddtest from the target directory; -C is not supported by this POC")
		}
		if buildBool[name] || testBool[name] {
			if buildBool[name] {
				o.buildFlags = append(o.buildFlags, a)
			}
			continue
		}
		if !buildValue[name] && !testValue[name] {
			return o, fmt.Errorf("unknown flag %s; pass custom test flags after -args", a)
		}
		flagArgs := []string{a}
		if !assigned {
			i++
			if i == len(args) {
				return o, fmt.Errorf("missing value for %s", a)
			}
			value = args[i]
			flagArgs = append(flagArgs, value)
		}
		if name == "overlay" {
			o.overlay = value
		}
		if buildValue[name] {
			o.buildFlags = append(o.buildFlags, flagArgs...)
		}
	}
	if len(o.packages) == 0 {
		o.packages = []string{"."}
	}
	return o, nil
}
