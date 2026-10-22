package runner

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/toolwrap"
)

type flagKind uint8

const (
	boolFlag flagKind = iota + 1
	valueFlag
)

type flagSpec struct {
	kind flagKind
	// list marks build flags that also change the packages go list resolves.
	list bool
}

// goTestFlags is the flag set cmd/go registers for go test: build, cover, test
// command and test binary flags. As in go test, any other flag belongs to the
// test binary. TestFlagTableMatchesToolchain checks this table against the
// running toolchain's help.
var goTestFlags = map[string]flagSpec{
	// Build flags (go help build). Debug flags are cmd/go's undocumented ones.
	"C": {valueFlag, false}, "a": {boolFlag, true}, "n": {boolFlag, true}, "x": {boolFlag, true},
	"p": {valueFlag, true}, "race": {boolFlag, true}, "msan": {boolFlag, true}, "asan": {boolFlag, true},
	"cover": {boolFlag, true}, "covermode": {valueFlag, true}, "coverpkg": {valueFlag, true},
	"work": {boolFlag, true}, "asmflags": {valueFlag, true}, "buildmode": {valueFlag, true},
	"buildvcs": {boolFlag, true}, "compiler": {valueFlag, true}, "gccgoflags": {valueFlag, true},
	"gcflags": {valueFlag, true}, "installsuffix": {valueFlag, true}, "ldflags": {valueFlag, true},
	"linkshared": {boolFlag, true}, "mod": {valueFlag, true}, "modcacherw": {boolFlag, true},
	"modfile": {valueFlag, true}, "overlay": {valueFlag, true}, "pgo": {valueFlag, true},
	"pkgdir": {valueFlag, true}, "tags": {valueFlag, true}, "trimpath": {boolFlag, true},
	"toolexec": {valueFlag, false}, "debug-actiongraph": {valueFlag, false},
	"debug-runtime-trace": {valueFlag, false}, "debug-trace": {valueFlag, false},
	// go test command flags (go help test).
	"c": {boolFlag, false}, "exec": {valueFlag, false}, "json": {boolFlag, false},
	"o": {valueFlag, false}, "vet": {valueFlag, false},
	// Test binary flags (go help testflag).
	"artifacts": {boolFlag, false}, "bench": {valueFlag, false}, "benchmem": {boolFlag, false},
	"benchtime": {valueFlag, false}, "blockprofile": {valueFlag, false}, "blockprofilerate": {valueFlag, false},
	"count": {valueFlag, false}, "coverprofile": {valueFlag, false}, "cpu": {valueFlag, false},
	"cpuprofile": {valueFlag, false}, "failfast": {boolFlag, false}, "fullpath": {boolFlag, false},
	"fuzz": {valueFlag, false}, "fuzzminimizetime": {valueFlag, false}, "fuzztime": {valueFlag, false},
	"list": {valueFlag, false}, "memprofile": {valueFlag, false}, "memprofilerate": {valueFlag, false},
	"mutexprofile": {valueFlag, false}, "mutexprofilefraction": {valueFlag, false},
	"outputdir": {valueFlag, false}, "parallel": {valueFlag, false}, "run": {valueFlag, false},
	"short": {boolFlag, false}, "shuffle": {valueFlag, false}, "skip": {valueFlag, false},
	"timeout": {valueFlag, false}, "trace": {valueFlag, false}, "v": {boolFlag, false},
}

// testBinaryFlags are also accepted with go test's "test." prefix.
var testBinaryFlags = words("artifacts bench benchmem benchtime blockprofile blockprofilerate count coverprofile cpu cpuprofile failfast fullpath fuzz fuzzminimizetime fuzztime list memprofile memprofilerate mutexprofile mutexprofilefraction outputdir parallel run short shuffle skip timeout trace v")

// cacheableTestFlags are the test binary flags with which go test may still
// take a package's result from its cache (go help test). Go has only added to
// this set, so it lists the newest supported Go's: a flag outside it disables
// the cache in every version.
var cacheableTestFlags = words("benchtime coverprofile cpu failfast fullpath list outputdir parallel run short skip timeout v")

func words(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

func lookupFlag(name string) (string, flagSpec, bool) {
	if spec, ok := goTestFlags[name]; ok {
		return name, spec, true
	}
	if short, ok := strings.CutPrefix(name, "test."); ok && testBinaryFlags[short] {
		return short, goTestFlags[short], true
	}
	return "", flagSpec{}, false
}

// argument is one go test command-line element in its original order. Flag is
// the canonical name of a go test flag; packages and test binary arguments
// have none. Raw keeps the original spelling, including a separate value.
type argument struct {
	raw  []string
	flag string
}

type options struct {
	packages, buildFlags []string
	arguments            []argument
	chdir                string
	mod                  string
	modfile              string
	overlay              string
	toolexec             string
	exec                 string
	coverage             bool
	coverPatterns        []string
	race                 bool
	// workFlagPattern records a per-package build flag for the work pattern.
	workFlagPattern bool
	// panicHook is ddto's --panic-hook, which go test does not see.
	panicHook bool
	// fileCoverage and fileCoverageContext are ddto's
	// --experimental-file-coverage and its =context.
	fileCoverage, fileCoverageContext bool
	// coverProfile and outputDir are go test's -coverprofile and -outputdir.
	coverProfile, outputDir string
	// execSet records -exec, even empty: then go test uses no
	// cross-compilation helper.
	execSet bool
	// compileOnly and printOnly record -c and -n; with either, go test runs
	// no test binary.
	compileOnly, printOnly bool
	// runsEveryTest reports that go test takes no result from its cache: it
	// runs in local directory mode, or the test binaries get an argument
	// outside the cacheable flags. cacheRule says why, for debug logs, without
	// any value from the command line.
	runsEveryTest bool
	cacheRule     string
	// rebuildAll and output record -a and -o as go test applies them, the
	// last one winning.
	rebuildAll bool
	output     string
	// workfile applies only to preparation children, never os.Setenv.
	workfile         string
	environment      *goEnvironment
	workspaceGoFlags *string
	// help and fileMode select native go test without instrumentation.
	help, fileMode bool
}

// splitFlags splits GOFLAGS and -toolexec values like cmd/go's quoted.Split:
// a field may be wrapped in single or double quotes, without unescaping.
func splitFlags(s string) ([]string, error) {
	return toolwrap.SplitFlags(s)
}

// parseOptions classifies arguments with go test's own algorithm: GOFLAGS apply
// first; -C must come first; a flag go test does not define, and everything
// after -args, --args or --, belongs to the test binary. The package list ends
// at the first flag after it, or at any flag go test does not define.
func parseOptions(args []string, goflags string) (options, error) {
	var o options
	envFlags, err := splitFlags(goflags)
	if err != nil {
		return o, fmt.Errorf("parsing $GOFLAGS: %w", err)
	}
	for _, f := range envFlags {
		// cmd/go applies only the GOFLAGS entries that go test defines.
		name, value, hasValue := strings.Cut(strings.TrimLeft(f, "-"), "=")
		canonical, spec, known := lookupFlag(name)
		if !known {
			continue
		}
		if canonical == "C" {
			return o, errors.New("-C flag must be first flag on command line")
		}
		if spec.kind == boolFlag && !hasValue {
			value = "true"
		}
		if err := o.apply(canonical, value); err != nil {
			return o, err
		}
		o.noteTestFlag(canonical, " in GOFLAGS")
	}
	var chdirSet bool
	o.chdir, chdirSet, args = chdirFlag(args)
	if chdirSet && o.chdir == "" {
		// go changes to it and fails, where ddto would test the current
		// directory: go test reports it.
		return o, errors.New("-C with an empty directory")
	}
	var packages []string
	inPackages, afterFlagWithoutValue := false, false
	for len(args) > 0 {
		raw := args[0]
		arg := raw
		if strings.HasPrefix(arg, "--") {
			if arg == "--" {
				// The terminator and everything after it go to the test binary.
				o.arguments = append(o.arguments, argument{raw: args})
				o.runsEveryTestBecause("test binary arguments after --")
				break
			}
			arg = arg[1:]
		}
		if arg == "-?" || arg == "-h" || arg == "-help" {
			o.help = true
			return o, nil
		}
		wasAfterFlagWithoutValue := afterFlagWithoutValue
		afterFlagWithoutValue = false
		if len(arg) < 2 || arg[0] != '-' || arg[1] == '-' || arg[1] == '=' {
			if !inPackages && packages != nil {
				if wasAfterFlagWithoutValue {
					// Possibly the value of an unknown flag; keep looking for flags.
					o.arguments = append(o.arguments, argument{raw: args[:1]})
					args = args[1:]
					continue
				}
				o.arguments = append(o.arguments, argument{raw: args})
				o.runsEveryTestBecause("test binary arguments after the packages")
				break
			}
			inPackages = true
			packages = append(packages, raw)
			o.arguments = append(o.arguments, argument{raw: args[:1]})
			args = args[1:]
			continue
		}
		inPackages = false
		name, value, hasValue := strings.Cut(arg[1:], "=")
		canonical, spec, known := lookupFlag(name)
		if !known {
			if packages == nil {
				packages = []string{}
			}
			// Go hands what follows -args, and an unknown flag, to the test
			// binary.
			if raw == "-args" || raw == "--args" {
				o.arguments = append(o.arguments, argument{raw: args})
				for _, arg := range args[1:] {
					o.noteTestArgument(arg)
				}
				break
			}
			o.runsEveryTestBecause("an unknown flag, which the test binary gets")
			o.arguments = append(o.arguments, argument{raw: args[:1]})
			args = args[1:]
			afterFlagWithoutValue = !hasValue
			continue
		}
		width := 1
		switch {
		case spec.kind == valueFlag && !hasValue:
			if len(args) < 2 {
				return o, fmt.Errorf("flag needs an argument: -%s", name)
			}
			value, width = args[1], 2
		case spec.kind == boolFlag && !hasValue:
			value = "true"
		}
		if canonical == "C" {
			return o, errors.New("-C flag must be first flag on command line")
		}
		if err := o.apply(canonical, value); err != nil {
			return o, err
		}
		o.noteTestFlag(canonical, "")
		if spec.list {
			o.buildFlags = append(o.buildFlags, args[:width]...)
		}
		o.arguments = append(o.arguments, argument{raw: args[:width], flag: canonical})
		args = args[width:]
	}
	if len(packages) == 0 {
		// Local directory mode caches no result.
		o.runsEveryTestBecause("local directory mode")
		packages = []string{"."}
	}
	o.packages = packages
	o.fileMode = strings.HasSuffix(packages[0], ".go")
	return o, nil
}

// linksCachedResults reports that go test links test binaries even for
// results in its cache: -o saves the binary, and -a rebuilds everything.
func (o *options) linksCachedResults() bool {
	return o.linkRule() != ""
}

// linkRule names the flag that makes go test link test binaries even for
// cached results, if any.
func (o *options) linkRule() string {
	switch {
	case o.output != "":
		return "-o"
	case o.rebuildAll:
		return "-a"
	}
	return ""
}

// noteTestFlag records that a test binary flag outside the cacheable ones
// keeps go test from taking results from its cache.
func (o *options) noteTestFlag(name, from string) {
	if testBinaryFlags[name] && !cacheableTestFlags[name] {
		o.runsEveryTestBecause("test binary flag -" + name + from)
	}
}

// runsEveryTestBecause records that go test takes no result from its cache,
// and the first reason.
func (o *options) runsEveryTestBecause(reason string) {
	if !o.runsEveryTest {
		o.runsEveryTest, o.cacheRule = true, reason
	}
}

// noteTestArgument records that an argument that go test hands to the test
// binary as is, as those after -args, keeps it from taking results from its
// cache, unless it has the form -test.<cacheable flag>=<value>.
func (o *options) noteTestArgument(arg string) {
	name, hasValue := "", false
	if flag, ok := strings.CutPrefix(arg, "-test."); ok {
		name, _, hasValue = strings.Cut(flag, "=")
	}
	if !hasValue || !cacheableTestFlags[name] {
		o.runsEveryTestBecause("a test binary argument after -args")
	}
}

// chdirFlag handles -C exactly where cmd/go accepts it: first after "test".
// It reports whether the flag was given, also with an empty directory.
func chdirFlag(args []string) (string, bool, []string) {
	if len(args) == 0 {
		return "", false, args
	}
	switch a := args[0]; {
	case a == "-C" || a == "--C":
		if len(args) > 1 {
			return args[1], true, args[2:]
		}
	case strings.HasPrefix(a, "-C=") || strings.HasPrefix(a, "--C="):
		_, dir, _ := strings.Cut(a, "=")
		return dir, true, args[1:]
	}
	return "", false, args
}

func (o *options) apply(name, value string) error {
	switch name {
	case "mod":
		o.mod = value
	case "modfile":
		o.modfile = value
	case "overlay":
		o.overlay = value
	case "toolexec":
		o.toolexec = value
	case "exec":
		o.exec, o.execSet = value, true
	case "cover":
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid boolean value %q for -cover", value)
		}
		o.coverage = enabled
	case "race":
		// go test reports an invalid value itself.
		if enabled, err := strconv.ParseBool(value); err == nil {
			o.race = enabled
		}
	case "asmflags", "gccgoflags", "gcflags", "ldflags":
		// Like cmd/go, a value that starts with a flag has no pattern.
		value = strings.TrimSpace(value)
		if pattern, _, ok := strings.Cut(value, "="); ok && !strings.HasPrefix(value, "-") && strings.TrimSpace(pattern) == "work" {
			o.workFlagPattern = true
		}
	case "o":
		o.output = value
	case "a":
		if enabled, err := strconv.ParseBool(value); err == nil {
			o.rebuildAll = enabled
		}
	case "c", "n":
		if enabled, err := strconv.ParseBool(value); err == nil {
			if name == "c" {
				o.compileOnly = enabled
			} else {
				o.printOnly = enabled
			}
		}
	case "covermode", "coverprofile":
		o.coverage = true
		if name == "coverprofile" {
			o.coverProfile = value
		}
	case "outputdir":
		o.outputDir = value
	case "coverpkg":
		o.coverage = true
		o.coverPatterns = nil
		if value != "" {
			o.coverPatterns = strings.Split(value, ",")
		}
	}
	return nil
}
