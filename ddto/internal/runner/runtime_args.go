package runner

import (
	"fmt"
	"path/filepath"
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
