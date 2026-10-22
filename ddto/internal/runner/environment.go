package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/version"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const minimumGoVersion = "go1.25.0"

// Go assumes these versions, including their GODEBUG defaults, when go.mod or
// go.work has no go directive.
const (
	defaultGoModVersion  = "1.16"
	defaultGoWorkVersion = "1.18"
)

// crossExecHelper returns the go_$GOOS_$GOARCH_exec program that go test runs
// test binaries with when it cross-compiles and no -exec is given. go test
// looks it up from dir, its working directory, and runs the binaries directly
// without one that it would run. It fails for a helper that go test would run
// through a path relative to dir, or that ddto cannot look up as go test does.
func crossExecHelper(environment *goEnvironment, dir string) (string, error) {
	if environment.GOOS == environment.GOHOSTOS && environment.GOARCH == environment.GOHOSTARCH {
		return "", nil
	}
	path, err := lookPathFrom(dir, "go_"+environment.GOOS+"_"+environment.GOARCH+"_exec")
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, exec.ErrDot) {
		return "", nil
	}
	return path, err
}

// errRelativeProgram is a program that Go would run through a path relative
// to its working directory, as GODEBUG=execerrdot=0 allows.
var errRelativeProgram = errors.New("found relative to the current directory, which GODEBUG=execerrdot=0 allows")

// errUnknownProgram is a lookup that ddto cannot reproduce: through a PATH
// entry relative to another drive's current directory, or with an execerrdot
// setting that GODEBUG limits to some call stacks.
var errUnknownProgram = errors.New("looked up through a PATH entry or GODEBUG setting that ddto cannot reproduce")

// lookPathFrom looks file up on PATH as exec.LookPath does with dir as the
// current directory, where go test looks up -exec programs and the
// cross-compilation helper after -C. It returns the absolute path of the file
// that Go would run. A file that Go would refuse to run returns exec.ErrDot,
// one that Go would run relative to dir returns errRelativeProgram, and a
// lookup that ddto cannot reproduce returns errUnknownProgram.
func lookPathFrom(dir, file string) (string, error) {
	// Names that exec.LookPath rejects before it searches.
	switch file {
	case "", ".", "..":
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}
	windows := runtime.GOOS == "windows"
	refuse, known := execErrDot()
	// relativeMatch is the result for a match relative to dir that Go would
	// not refuse.
	relativeMatch := func() error {
		if !known {
			return &exec.Error{Name: file, Err: errUnknownProgram}
		}
		return &exec.Error{Name: file, Err: errRelativeProgram}
	}
	// pending is a match relative to dir, which Go refuses unless, on
	// Windows, PATH later names the same file absolutely.
	var pending string
	if _, skip := os.LookupEnv("NoDefaultCurrentDirectoryInExePath"); windows && !skip {
		// Windows searches the current directory first.
		if f, err := exec.LookPath(filepath.Join(dir, file)); err == nil {
			if !refuse || !known {
				return "", relativeMatch()
			}
			pending = f
		}
	}
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if entry == "" {
			if windows {
				continue
			}
			// An empty entry is the current directory on Unix.
			entry = "."
		}
		relative := !filepath.IsAbs(entry)
		candidate := filepath.Join(entry, file)
		if relative {
			switch volume := filepath.VolumeName(entry); {
			case volume != "" && !strings.EqualFold(volume, filepath.VolumeName(dir)):
				// Relative to that drive's own current directory.
				return "", &exec.Error{Name: file, Err: errUnknownProgram}
			case volume != "":
				candidate = filepath.Join(dir, candidate[len(volume):])
			case os.IsPathSeparator(entry[0]):
				// Rooted on the current drive, on Windows.
				candidate = filepath.VolumeName(dir) + candidate
			default:
				candidate = filepath.Join(dir, candidate)
			}
		}
		f, err := exec.LookPath(candidate)
		switch {
		case err != nil:
			continue
		case pending != "" && !sameLink(pending, f):
			return "", &exec.Error{Name: file, Err: exec.ErrDot}
		case !relative:
			return f, nil
		case !refuse || !known:
			return "", relativeMatch()
		case !windows:
			return "", &exec.Error{Name: file, Err: exec.ErrDot}
		case pending == "":
			pending = f
		}
	}
	if pending != "" {
		return "", &exec.Error{Name: file, Err: exec.ErrDot}
	}
	return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
}

// execErrDot reports whether Go refuses programs found relative to the
// current directory: unless GODEBUG's last execerrdot setting is 0. A setting
// for the call stacks that a pattern selects, value#pattern, is not known.
func execErrDot() (refuse, known bool) {
	value := ""
	for _, setting := range strings.Split(os.Getenv("GODEBUG"), ",") {
		if name, v, ok := strings.Cut(setting, "="); ok && name == "execerrdot" {
			value = v
		}
	}
	return value != "0", !strings.Contains(value, "#")
}

// sameLink reports whether a and b name the same file, without following a
// final symbolic link, as exec.LookPath compares them.
func sameLink(a, b string) bool {
	ai, err := os.Lstat(a)
	if err != nil {
		return false
	}
	bi, err := os.Lstat(b)
	return err == nil && os.SameFile(ai, bi)
}

// goDebugDefault returns the GODEBUG default=go1.N value for a Go version.
func goDebugDefault(version string) string {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return ""
	}
	return "go" + strings.Join(parts[:2], ".")
}

// goFlagsEnv returns the GOFLAGS entry that gives a go command flags and
// nothing else. An empty value would not do: go then applies the GOFLAGS that
// go env -w saved, so no flags is a space.
func goFlagsEnv(flags string) string {
	if flags == "" {
		flags = " "
	}
	return "GOFLAGS=" + flags
}

// callerGoFlags returns the GOFLAGS that the caller's go commands use: the
// environment's, or, when the environment leaves GOFLAGS empty, the value
// that go env -w saved, which go then applies (cmd/go/internal/cfg).
func callerGoFlags() string {
	if flags := os.Getenv("GOFLAGS"); flags != "" {
		return flags
	}
	return savedGoSetting("GOFLAGS")
}

// savedGoSetting returns name's last value in the file that go env -w writes,
// found as cmd/go's cfg.EnvFile finds it, or "".
func savedGoSetting(name string) string {
	file := os.Getenv("GOENV")
	if file == "off" {
		return ""
	}
	if file == "" {
		dir, err := os.UserConfigDir()
		if err != nil || dir == "" {
			return ""
		}
		file = filepath.Join(dir, "go", "env")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	value := ""
	for line := range strings.SplitSeq(string(data), "\n") {
		if key, v, ok := strings.Cut(line, "="); ok && key == name {
			value = v
		}
	}
	return value
}

type goEnvironment struct {
	GOMOD, GOWORK, GOMODCACHE, GOVERSION string
	GOROOT                               string
	GOOS, GOARCH, GOHOSTOS, GOHOSTARCH   string
	// GOEXPERIMENT is the setting as go env reports it, not canonical.
	GOEXPERIMENT string
}

func readGoEnvironment(ctx context.Context, dir string) (*goEnvironment, error) {
	out, err := goTool(ctx, dir, nil, "env", "-json", "GOMOD", "GOWORK", "GOMODCACHE", "GOVERSION", "GOROOT", "GOOS", "GOARCH", "GOHOSTOS", "GOHOSTARCH", "GOEXPERIMENT")
	if err != nil {
		// go env reads the caller's environment, such as GOFLAGS.
		return nil, goRejected(err)
	}
	var environment goEnvironment
	if err := json.Unmarshal([]byte(out), &environment); err != nil {
		return nil, fmt.Errorf("read Go environment: %w", err)
	}
	return &environment, nil
}

// testRuntimeVersion returns what runtime.Version() reports in the test
// binaries that the selected toolchain links in dir, for the requests that the
// Git worker makes as they would. goVersion, from go env, is the go command's
// own version, with the toolchain's default experiments. When goExperiment
// sets others, the linker appends their canonical list, which go tool compile
// -V prints, to the toolchain's version. A wrong result only costs the test
// processes their own requests.
func testRuntimeVersion(ctx context.Context, dir, goVersion, goExperiment string) string {
	debug := debugFromContext(ctx)
	if _, own, _ := cutExperiments(goVersion); goExperiment == own {
		debug.printf("test runtime version=%q source=go_env", goVersion)
		return goVersion
	}
	phase := debug.start("test runtime version")
	cmd := goCommand(ctx, dir, "tool", "compile", "-V")
	cmd.Env = append(cmd.Environ(), goFlagsEnv(""), "GOEXPERIMENT="+goExperiment)
	out, err := cmd.Output()
	phase.finish(err)
	if err != nil {
		debug.printf("test runtime version=%q source=go_env compile_error=true", goVersion)
		return goVersion
	}
	runtimeVersion := strings.TrimPrefix(strings.TrimSpace(string(out)), "compile version ")
	if base, experiments, _ := strings.Cut(runtimeVersion, " X:"); experiments == "" {
		runtimeVersion = base
	} else {
		runtimeVersion = base + experimentSeparator(goVersion, base) + "X:" + experiments
	}
	debug.printf("test runtime version=%q source=go_tool_compile", runtimeVersion)
	return runtimeVersion
}

// cutExperiments splits a runtime version at the experiments that the linker
// appends after "-X:" or " X:".
func cutExperiments(value string) (base, experiments string, found bool) {
	for _, separator := range []string{"-X:", " X:"} {
		if base, experiments, found := strings.Cut(value, separator); found {
			return base, experiments, true
		}
	}
	return value, "", false
}

// experimentSeparator returns what the linker writes before "X:" after base.
// goVersion shows it when the toolchain has default experiments. Otherwise Go
// 1.26 and later write "-" after a version without one (go.dev/issue/75953),
// and earlier versions a space.
func experimentSeparator(goVersion, base string) string {
	if i := strings.Index(goVersion, "X:"); i > 0 {
		return goVersion[i-1 : i]
	}
	if strings.Contains(base, "-") || !version.IsValid(base) || version.Compare(base, "go1.26rc1") < 0 {
		return " "
	}
	return "-"
}

func supportsGoToolchain(value string) bool {
	// Development distributions report "devel go1.N-<revision> ...".
	fields := strings.Fields(strings.TrimPrefix(value, "devel "))
	return len(fields) != 0 && version.IsValid(fields[0]) && version.Compare(fields[0], minimumGoVersion) >= 0
}
