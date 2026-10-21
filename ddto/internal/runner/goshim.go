package runner

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

// The go that ddto puts first on PATH for gotestsum, which runs go test
// itself, is ddto, named go in a directory of ddto's user cache. Run as go,
// ddto runs as ddto go, so gotestsum's go test reports to CI Visibility with
// gotestsum's own arguments.
var (
	// goShimFirst is the go shim directory that this process puts first on
	// PATH for the command that it runs.
	goShimFirst string
	// goShimLeft is the go shim directory that this process left off PATH.
	goShimLeft string
	// goShimGo is the go that a go command with the go shim runs.
	goShimGo string
)

// GoShimArgs returns ddto's arguments from its command line, and whether ddto
// runs as the go shim: then they are go and its arguments. Every ddto first
// leaves the go shims off PATH, as written otherwise, so the go that it and
// its commands run is the toolchain's, and tests see their caller's PATH.
func GoShimArgs(args []string) ([]string, bool) {
	if kept, left := withoutGoShims(os.Getenv("PATH")); left != "" {
		_ = os.Setenv("PATH", kept)
		goShimLeft = left
	}
	if isProgram(args[0], "go") {
		return append([]string{"go"}, args[1:]...), true
	}
	return args[1:], false
}

// logGoShim logs the go shim that this process left off PATH, and the one that
// it puts first for the command it runs.
func (d *cliDebug) logGoShim() {
	if goShimLeft != "" {
		d.printf("go_shim left=%q", goShimLeft)
	}
	if goShimFirst != "" {
		d.printf("go_shim first_on_path=%q", goShimFirst)
	}
	if goShimGo != "" {
		d.printf("go_shim go=%q", goShimGo)
	}
}

// goShimFlag puts the go shim first on PATH for any command, so that the go
// test that it, or a command it starts, runs reports to CI Visibility.
const goShimFlag = "--go-shim"

// GoShimCommand returns the command that ddto runs, without --go-shim, and
// whether it runs with the go shim: after --go-shim, for gotestsum in any
// form, and, for ddto run as the go shim, for go run and go tool, which can
// start another program that runs go test.
func GoShimCommand(args []string, fromGoShim bool) ([]string, bool) {
	if len(args) > 0 && args[0] == goShimFlag {
		return args[1:], true
	}
	if runsGotestsum(args) {
		return args, true
	}
	if fromGoShim && len(args) > 0 && isProgram(args[0], "go") {
		_, sub, _ := goSubcommand(args[1:])
		return args, sub == "run" || sub == "tool"
	}
	return args, false
}

// runsGotestsum reports whether args run gotestsum: itself, as a tool of the
// module, or with go run.
func runsGotestsum(args []string) bool {
	if IsGotestsum(args) {
		return true
	}
	if len(args) < 2 || !isProgram(args[0], "go") {
		return false
	}
	_, sub, rest := goSubcommand(args[1:])
	switch sub {
	case "tool":
		return len(rest) > 0 && (rest[0] == "gotestsum" || rest[0] == "gotest.tools/gotestsum")
	case "run":
		if i := runPackage(rest); i < len(rest) {
			pkg, _, _ := strings.Cut(rest[i], "@")
			return pkg == "gotest.tools/gotestsum"
		}
	}
	return false
}

// goSubcommand splits go's arguments into a leading -C flag, which go
// requires first, the subcommand and the subcommand's arguments.
func goSubcommand(args []string) (chdir []string, sub string, rest []string) {
	chdir, args = leadingChdir(args)
	if len(args) == 0 {
		return chdir, "", nil
	}
	return chdir, args[0], args[1:]
}

// leadingChdir splits a first -C flag, with its value, off args.
func leadingChdir(args []string) (chdir, rest []string) {
	if len(args) == 0 || !strings.HasPrefix(args[0], "-") {
		return nil, args
	}
	name, _, hasValue := strings.Cut(strings.TrimLeft(args[0], "-"), "=")
	switch {
	case name != "C":
		return nil, args
	case hasValue:
		return args[:1], args[1:]
	case len(args) >= 2:
		return args[:2], args[2:]
	}
	return nil, args
}

// runPackage returns the index of go run's package in its arguments: the
// first one that is neither a build flag nor a flag's value.
func runPackage(args []string) int {
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "-") {
			return i
		}
		name, _, hasValue := strings.Cut(strings.TrimLeft(args[i], "-"), "=")
		if _, spec, known := lookupFlag(name); known && spec.kind == valueFlag && !hasValue {
			i++
		}
	}
	return len(args)
}

// IsGotestsum reports whether args run gotestsum.
func IsGotestsum(args []string) bool {
	return len(args) > 0 && isProgram(args[0], "gotestsum")
}

// isProgram reports whether path names the program name, with or without
// Windows' .exe.
func isProgram(path, name string) bool {
	base := filepath.Base(path)
	if runtime.GOOS == "windows" {
		base = strings.TrimSuffix(strings.ToLower(base), ".exe")
	}
	return base == name
}

// RunWithGoShim runs command with the go shim first on its PATH and returns
// its exit status. If ddto cannot set the shim up, it warns and runs the
// command without it.
func RunWithGoShim(command []string) int {
	run, tool, err := goShimSetup(command)
	if err != nil {
		fmt.Fprintln(os.Stderr, version.BuildLogPrefix+" WARN:", err, "- running", command[0], "without CI Visibility")
		return ExecCommand(command)
	}
	if tool != nil {
		return tool.run()
	}
	return ExecCommand(run)
}

// goShimSetup puts the go shim first on PATH for command and returns the
// command to run, or the module tool to run. A go command runs the go that
// PATH names before the shim; see goShimGoCommand. On an error, PATH stays as
// it was.
func goShimSetup(command []string) ([]string, *moduleTool, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}
	dir, err := goShimDir(self)
	if err != nil {
		return nil, nil, err
	}
	run, entries := command, []string{dir}
	var tool *moduleTool
	if isProgram(command[0], "go") {
		toolchain, err := exec.LookPath(command[0])
		if err != nil {
			return nil, nil, err
		}
		if run, tool, err = goShimGoCommand(self, toolchain, command); err != nil {
			return nil, nil, err
		}
		if tool != nil {
			// go tool puts its toolchain's bin first, here after the shim.
			entries = append(entries, tool.gorootBin)
		}
		goShimGo = toolchain
	}
	path, err := pathWith(entries)
	if err != nil {
		return nil, nil, err
	}
	if err := os.Setenv("PATH", path); err != nil {
		return nil, nil, err
	}
	goShimFirst = dir
	return run, tool, nil
}

// GoShimExecMode is ddto's mode as go run's -exec program with the go shim.
const GoShimExecMode = "go-shim-exec"

// goToolSignals are the signals that go tool forwards to a tool
// (cmd/go/internal/tool/signal.go).
var goToolSignals = []os.Signal{syscall.SIGHUP, os.Interrupt, syscall.SIGQUIT, syscall.SIGTERM}

// moduleTool is a module's tool that go tool would run: ddto runs it as go
// tool does, with the go shim first on its PATH.
type moduleTool struct {
	name, path string
	args       []string
	// gorootBin, the selected toolchain's bin, goes after the shim.
	gorootBin string
	// switchGOROOT is GOROOT for the tool after a toolchain switch, as
	// go's execGoToolchain sets it; unsetGOROOT removes it instead.
	switchGOROOT string
	unsetGOROOT  bool
	// diagnostics are what go wrote while it built the tool.
	diagnostics []byte
}

// run runs the tool as go tool does: with the caller's environment, signals
// forwarded, and go tool's report of a tool that did not exit cleanly.
func (t *moduleTool) run() int {
	_, _ = os.Stderr.Write(t.diagnostics)
	cmd := exec.Command(t.path, t.args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	switch {
	case t.switchGOROOT != "":
		cmd.Env = append(os.Environ(), "GOROOT="+t.switchGOROOT)
	case t.unsetGOROOT:
		cmd.Env = slices.DeleteFunc(os.Environ(), func(entry string) bool {
			name, _, _ := strings.Cut(entry, "=")
			return name == "GOROOT" || runtime.GOOS == "windows" && strings.EqualFold(name, "GOROOT")
		})
	}
	err := cmd.Start()
	if err == nil {
		signals := make(chan os.Signal, 100)
		signal.Notify(signals, goToolSignals...)
		go func() {
			for sig := range signals {
				_ = cmd.Process.Signal(sig)
			}
		}()
		err = cmd.Wait()
		signal.Stop(signals)
		close(signals)
	}
	if err == nil {
		return 0
	}
	exit, ok := err.(*exec.ExitError)
	if !ok || !exit.Exited() {
		fmt.Fprintf(os.Stderr, "go tool %s: %s\n", t.name, err)
	}
	if ok && exit.ExitCode() != -1 {
		return exit.ExitCode()
	}
	return 1
}

// goShimGoCommand returns the go command to run with the go shim, or the
// module tool to run. go run and go tool put $GOROOT/bin before the shim on
// their program's PATH, so go run runs its program through ddto's
// GoShimExecMode, which puts the shim first again, and ddto runs a module
// tool itself, as go tool would. The toolchain's own tools run through go
// tool, which gives them the environment that go computes.
func goShimGoCommand(self, toolchain string, command []string) ([]string, *moduleTool, error) {
	chdir, sub, rest := goSubcommand(command[1:])
	native := append([]string{toolchain}, command[1:]...)
	switch sub {
	case "run":
		env, err := readGoShimEnv(toolchain, chdir)
		if err != nil {
			return nil, nil, err
		}
		if err := goRunExecutor(env, rest); err != nil {
			return nil, nil, err
		}
		program, err := quoteToolWords([]string{self, GoShimExecMode})
		if err != nil {
			return nil, nil, err
		}
		// -C stays go run's first flag.
		runChdir, rest := leadingChdir(rest)
		run := append(append([]string{toolchain}, chdir...), "run")
		run = append(append(run, runChdir...), "-exec="+program)
		return append(run, rest...), nil, nil
	case "tool":
		if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
			return nil, nil, errors.New("go tool takes flags before the tool")
		}
		var diagnostics bytes.Buffer
		probe := exec.Command(toolchain, append(append(append([]string(nil), chdir...), "tool", "-n"), rest[0])...)
		probe.Stderr = &diagnostics
		out, err := probe.Output()
		path := strings.TrimSpace(string(out))
		if err != nil || isToolchainTool(path) {
			// go reports a tool that it cannot build itself.
			return native, nil, nil
		}
		if len(chdir) != 0 {
			return nil, nil, errors.New("go -C runs a module tool in another directory")
		}
		// With a cross-compilation executor, go tool -n prints it first.
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			return nil, nil, fmt.Errorf("go tool -n %s printed %q, not one program", rest[0], path)
		}
		env, err := readGoShimEnv(toolchain, nil)
		if err != nil {
			return nil, nil, err
		}
		tool := &moduleTool{name: rest[0], path: path, args: rest[1:], gorootBin: filepath.Join(env.GOROOT, "bin"), diagnostics: diagnostics.Bytes()}
		if switchedToolchain(toolchain, env.GOROOT) {
			// execGoToolchain sets GOROOT for a downloaded toolchain and
			// removes it for one found on PATH.
			if strings.Contains(filepath.ToSlash(env.GOROOT), "golang.org/toolchain@") {
				tool.switchGOROOT = env.GOROOT
			} else {
				tool.unsetGOROOT = true
			}
		}
		return nil, tool, nil
	}
	return native, nil, nil
}

// switchedToolchain reports whether go switched from toolchain to the one at
// selected: go env reports an exported GOROOT, or else toolchain's own root,
// unless it switched.
func switchedToolchain(toolchain, selected string) bool {
	if goroot, set := os.LookupEnv("GOROOT"); set {
		return filepath.Clean(goroot) != filepath.Clean(selected)
	}
	resolved, err := filepath.EvalSymlinks(toolchain)
	if err != nil {
		return false
	}
	root, selected := filepath.Dir(filepath.Dir(resolved)), filepath.Clean(selected)
	if resolvedSelected, err := filepath.EvalSymlinks(selected); err == nil {
		selected = resolvedSelected
	}
	return root != selected
}

// goShimEnv is what go computes for a go run or go tool command with the go
// shim, with the settings that go env -w stores.
type goShimEnv struct {
	GOROOT, GOFLAGS, GOOS, GOARCH, GOHOSTOS, GOHOSTARCH string
}

// readGoShimEnv reads goShimEnv from go env; tests replace it.
var readGoShimEnv = func(toolchain string, chdir []string) (goShimEnv, error) {
	var env goShimEnv
	out, err := exec.Command(toolchain, append(append([]string(nil), chdir...), "env", "-json", "GOROOT", "GOFLAGS", "GOOS", "GOARCH", "GOHOSTOS", "GOHOSTARCH")...).Output()
	if err != nil {
		return env, fmt.Errorf("go env: %w", err)
	}
	return env, json.Unmarshal(out, &env)
}

// goRunExecutor returns an error when go run would start its program through
// an executor that ddto's own -exec program would replace: the caller's -exec,
// in go run's flags or GOFLAGS, or the executor of a cross-compiled program.
func goRunExecutor(env goShimEnv, args []string) error {
	flags := args[:runPackage(args)]
	if fromEnv, err := splitFlags(env.GOFLAGS); err == nil {
		flags = append(append([]string(nil), flags...), fromEnv...)
	}
	for _, flag := range flags {
		if name, _, _ := strings.Cut(strings.TrimLeft(flag, "-"), "="); strings.HasPrefix(flag, "-") && name == "exec" {
			return errors.New("go run -exec starts its program with $GOROOT/bin before the go shim")
		}
	}
	if env.GOOS != env.GOHOSTOS || env.GOARCH != env.GOHOSTARCH {
		return errors.New("go run cross-compiles, so its program would start through another executor")
	}
	return nil
}

// isToolchainTool reports whether path is in the toolchain's pkg/tool
// directory, as go tool -n prints the toolchain's own tools.
func isToolchainTool(path string) bool {
	platform := filepath.Dir(path)
	tool := filepath.Dir(platform)
	return filepath.Base(tool) == "tool" && filepath.Base(filepath.Dir(tool)) == "pkg"
}

// ExecWithGoShim runs args, the program that go run built and its arguments,
// with the go shim first on its PATH again.
func ExecWithGoShim(args []string) int {
	self, err := os.Executable()
	if err == nil {
		var dir string
		if dir, err = goShimDir(self); err == nil {
			err = prependGoShim(dir)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, version.BuildLogPrefix+" WARN:", err, "- running", args[0], "without the go shim")
	}
	// The program runs as any command that ddto runs, in the same job on
	// Windows.
	return ExecCommand(args)
}

// prependGoShim puts the go shim directory first on PATH.
func prependGoShim(dir string) error {
	path, err := pathWith([]string{dir})
	if err != nil {
		return err
	}
	if err := os.Setenv("PATH", path); err != nil {
		return err
	}
	goShimFirst = dir
	return nil
}

// pathWith returns PATH with entries first, in order. On Windows an entry
// with the separator is quoted; elsewhere it cannot be on PATH.
func pathWith(entries []string) (string, error) {
	var first []string
	for _, entry := range entries {
		if strings.ContainsRune(entry, os.PathListSeparator) {
			if runtime.GOOS != "windows" {
				return "", fmt.Errorf("the directory %s holds the PATH separator", entry)
			}
			entry = `"` + entry + `"`
		}
		first = append(first, entry)
	}
	return strings.Join(append(first, os.Getenv("PATH")), string(os.PathListSeparator)), nil
}

// goShimRoot is the directory of every go shim, in ddto's user cache.
func goShimRoot() (string, error) {
	cache, err := userCacheDir()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(cache) {
		return "", fmt.Errorf("user cache directory %q is not absolute", cache)
	}
	return filepath.Join(cache, "ddto", "go-shim"), nil
}

// goShimDir returns the directory of a go that is this ddto: a symbolic link
// to it, or on Windows a hard link or copy. Its name changes with ddto's
// path, size and modification time, so an upgraded ddto gets its own.
func goShimDir(self string) (string, error) {
	info, err := os.Stat(self)
	if err != nil {
		return "", err
	}
	root, err := goShimRoot()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(self + "\x00" + strconv.FormatInt(info.Size(), 10) + "\x00" + strconv.FormatInt(info.ModTime().UnixNano(), 10)))
	dir := filepath.Join(root, hex.EncodeToString(sum[:8]))
	shim := filepath.Join(dir, "go")
	if runtime.GOOS == "windows" {
		shim += ".exe"
	}
	if _, err := os.Stat(shim); err == nil {
		return dir, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// Another ddto may create the same shim: build it aside, then rename.
	temporary := shim + ".tmp" + strconv.Itoa(os.Getpid())
	_ = os.Remove(temporary)
	if runtime.GOOS != "windows" {
		err = os.Symlink(self, temporary)
	} else if err = os.Link(self, temporary); err != nil {
		err = copyExecutableFile(self, temporary)
	}
	if err != nil {
		return "", err
	}
	if err := os.Rename(temporary, shim); err != nil {
		_ = os.Remove(temporary)
		if _, statErr := os.Stat(shim); statErr != nil {
			return "", err
		}
	}
	return dir, nil
}

// copyExecutableFile copies the executable at source to target.
func copyExecutableFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return errors.Join(err, out.Close())
}

// withoutGoShims returns the PATH list without its go shims, keeping the
// other entries as written, and the last entry that it left.
func withoutGoShims(list string) (kept, left string) {
	var entries []string
	for _, raw := range pathListEntries(list) {
		entry := raw
		if runtime.GOOS == "windows" {
			entry = strings.ReplaceAll(raw, `"`, "")
		}
		if isGoShimEntry(entry) {
			left = entry
			continue
		}
		entries = append(entries, raw)
	}
	return strings.Join(entries, string(os.PathListSeparator)), left
}

// isGoShimEntry reports whether a PATH entry is a directory that goShimDir
// makes, ddto/go-shim/ and 16 hexadecimal digits, in any user cache: an
// environment that a command filtered may name another. It reads no file, so
// a slow PATH entry does not delay ddto.
func isGoShimEntry(entry string) bool {
	if entry == "" || !filepath.IsAbs(entry) {
		return false
	}
	entry = filepath.Clean(entry)
	name, parent := filepath.Base(entry), filepath.Dir(entry)
	if len(name) != 16 || strings.Trim(name, "0123456789abcdef") != "" {
		return false
	}
	return filepath.Base(parent) == "go-shim" && filepath.Base(filepath.Dir(parent)) == "ddto"
}

// pathListEntries splits a PATH list where filepath.SplitList does, but keeps
// each entry as written: on Windows, quotes may enclose a separator.
func pathListEntries(list string) []string {
	if list == "" {
		return nil
	}
	if runtime.GOOS != "windows" {
		return strings.Split(list, string(os.PathListSeparator))
	}
	var entries []string
	start, quoted := 0, false
	for i := 0; i < len(list); i++ {
		switch list[i] {
		case '"':
			quoted = !quoted
		case os.PathListSeparator:
			if !quoted {
				entries = append(entries, list[start:i])
				start = i + 1
			}
		}
	}
	return append(entries, list[start:])
}
