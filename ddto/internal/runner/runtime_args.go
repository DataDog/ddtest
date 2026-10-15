package runner

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// defaultGitUploadTimeout is how long anyone waits for the Git worker's
// repository upload: test processes for its result, and ddto after go test
// exits, as a test process waited at exit for its own upload.
const defaultGitUploadTimeout = time.Minute

// CLIOptions are ddto's own options, which it consumes before go test's.
type CLIOptions struct {
	Runtime Runtime
	// GitUploadTimeout bounds every wait for the Git worker's repository
	// upload (--git-upload-timeout); ddto then stops the upload.
	GitUploadTimeout time.Duration
	// KeepCIState keeps the invocation's CI state for debugging
	// (--keep-ci-state); KeepCIStateZip, if set, archives it there instead.
	KeepCIState    bool
	KeepCIStateZip string
	// PanicHook edits the runtime so that Mini sends its events before an
	// unrecovered panic ends a test process (--panic-hook). Every package of
	// the build depends on the runtime, so the first build with it, per Go
	// version and build variant, compiles them all again.
	PanicHook bool
	// SessionPerBinary keeps one test session per test binary, which each
	// test process reports, instead of the command's, which ddto reports
	// (--session-per-binary).
	SessionPerBinary bool
}

// DefaultCLIOptions are the options without any ddto flag.
func DefaultCLIOptions() CLIOptions {
	return CLIOptions{Runtime: Mini, GitUploadTimeout: defaultGitUploadTimeout}
}

// ParseCLIArgs consumes ddto's own options before Go prepares a build.
// Go flag values and the custom test-argument tail are passed through
// unchanged. Put ddto's options before custom test flags, -args or --.
func ParseCLIArgs(args []string) (CLIOptions, []string, error) {
	options := DefaultCLIOptions()
	goArgs := make([]string, 0, len(args))
	for len(args) > 0 {
		arg := args[0]
		name, value, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		if strings.HasPrefix(arg, "--") && (name == "runtime" || name == "git-upload-timeout") {
			width := 1
			if !hasValue {
				if len(args) < 2 {
					return CLIOptions{}, nil, fmt.Errorf("--%s needs a value", name)
				}
				value, width = args[1], 2
			}
			if err := options.set(name, value); err != nil {
				return CLIOptions{}, nil, err
			}
			args = args[width:]
			continue
		}
		if arg == "--panic-hook" || arg == "--session-per-binary" {
			options.PanicHook = options.PanicHook || arg == "--panic-hook"
			options.SessionPerBinary = options.SessionPerBinary || arg == "--session-per-binary"
			args = args[1:]
			continue
		}
		if strings.HasPrefix(arg, "--") && name == "keep-ci-state" {
			// No separate value: it would be taken for a package.
			if err := options.keepCIState(value, hasValue); err != nil {
				return CLIOptions{}, nil, err
			}
			args = args[1:]
			continue
		}
		width := 1
		if strings.HasPrefix(arg, "-") && arg != "-" {
			_, spec, known := lookupFlag(name)
			if !known || arg == "--" {
				// The CLI does not own custom flags or their values. Callers
				// can also use -args/-- to pass a test flag with ddto's name.
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
	return options, goArgs, nil
}

func (o *CLIOptions) set(name, value string) error {
	switch name {
	case "runtime":
		o.Runtime = Runtime(value)
		if o.Runtime != Mini && o.Runtime != SDK {
			return fmt.Errorf("runtime must be sdk or mini, got %q", value)
		}
	case "git-upload-timeout":
		timeout, err := time.ParseDuration(value)
		if err != nil || timeout < 0 {
			return fmt.Errorf("--git-upload-timeout needs a duration such as 2m, got %q", value)
		}
		o.GitUploadTimeout = timeout
	}
	return nil
}

func (o *CLIOptions) keepCIState(value string, hasValue bool) error {
	o.KeepCIState, o.KeepCIStateZip = true, ""
	if !hasValue {
		return nil
	}
	if !strings.HasSuffix(strings.ToLower(value), ".zip") {
		return fmt.Errorf("--keep-ci-state takes no value or a .zip path, got %q", value)
	}
	path, err := filepath.Abs(value)
	if err != nil {
		return err
	}
	o.KeepCIStateZip = path
	return nil
}

// TestForm is the go test command that ddto's arguments name.
type TestForm struct {
	// Args are go test's arguments.
	Args []string
	// Name is the form, such as "go -C test", for debug logs.
	Name string
	// Orchestrion and GoTool select Orchestrion mode, through go tool.
	Orchestrion, GoTool bool
}

// TestCommand reports whether ddto's arguments name go test: test, go test, or
// Orchestrion's orchestrion go test and go tool orchestrion go test, which
// run in Orchestrion mode. As go does, it accepts -C dir after go and before
// test; go test takes it as its first flag.
func TestCommand(args []string) (TestForm, bool) {
	if len(args) > 0 && args[0] == "test" {
		return TestForm{Args: args[1:], Name: "test"}, true
	}
	for _, form := range []struct {
		prefix              []string
		orchestrion, goTool bool
	}{
		{[]string{"go"}, false, false},
		{[]string{"orchestrion", "go"}, true, false},
		{[]string{"go", "tool", "orchestrion", "go"}, true, true},
	} {
		if testArgs, chdir, ok := goTestArgs(args, form.prefix); ok {
			name := strings.Join(form.prefix, " ")
			if chdir {
				name += " -C"
			}
			return TestForm{Args: testArgs, Name: name + " test", Orchestrion: form.orchestrion, GoTool: form.goTool}, true
		}
	}
	return TestForm{}, false
}

// goTestArgs returns go test's arguments when args are prefix, a go command,
// then test, after an optional -C dir, and whether -C came first.
func goTestArgs(args, prefix []string) ([]string, bool, bool) {
	if len(args) <= len(prefix) || !slices.Equal(args[:len(prefix)], prefix) {
		return nil, false, false
	}
	args = args[len(prefix):]
	var chdir []string
	switch a := args[0]; {
	case (a == "-C" || a == "--C") && len(args) > 1:
		chdir, args = args[:2], args[2:]
	case strings.HasPrefix(a, "-C=") || strings.HasPrefix(a, "--C="):
		chdir, args = args[:1], args[1:]
	}
	if len(args) == 0 || args[0] != "test" {
		return nil, false, false
	}
	return append(slices.Clip(chdir), args[1:]...), chdir != nil, true
}
