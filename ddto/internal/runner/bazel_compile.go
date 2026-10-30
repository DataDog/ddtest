package runner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/ddto/internal/instrument"
	"github.com/DataDog/ddtest/ddto/internal/toolwrap"
	"github.com/DataDog/ddtest/ddto/internal/version"
)

// The instrumented SDK that ddtest instrument bazel writes keeps the SDK's
// own compiler as compile.real, next to this executable named compile, and its
// options in bazelCompilerConfig. rules_go runs the SDK's compile for every
// package, and for the standard library through go install -toolexec, so the
// compiler mode below is -toolexec for Bazel: it applies the edits of
// ddtest instrument to the packages that they name and runs the real compiler.
const (
	bazelRealCompiler   = "compile.real"
	bazelCompilerConfig = "ddtest-instrument.json"
)

// bazelConfig is the instrumented SDK's configuration.
type bazelConfig struct {
	// Version is the ddtest release that wrote the SDK.
	Version string `json:"version"`
	// Contract is the version of the SDK's layout and of Mini's repository.
	Contract int `json:"contract"`
	// PanicHook edits the runtime as --panic-hook does.
	PanicHook bool `json:"panicHook"`
}

// RunsAsBazelCompiler reports whether a process whose argv[0] is arg0 runs as
// the compiler of an instrumented SDK: named compile, next to compile.real.
func RunsAsBazelCompiler(arg0 string) bool {
	if strings.TrimSuffix(filepath.Base(arg0), ".exe") != "compile" {
		return false
	}
	_, err := os.Stat(bazelRealCompilerPath(arg0))
	return err == nil
}

func bazelRealCompilerPath(arg0 string) string {
	name := bazelRealCompiler
	if strings.HasSuffix(arg0, ".exe") {
		name += ".exe"
	}
	return filepath.Join(filepath.Dir(arg0), name)
}

// RunBazelCompiler runs as the instrumented SDK's compiler, given the
// process's arguments. A package that no edit names reaches the real
// compiler at once, without any file I/O; so does the -V=full probe.
func RunBazelCompiler(argv []string) int {
	real := bazelRealCompilerPath(argv[0])
	args := argv[1:]
	pkg := bazelCanonicalPackage(compilerFlag(args, "-p"))
	if !bazelEdits(pkg, compilerBoolFlag(args, "-race"), argv[0]) {
		return execBazelCompiler(real, argv)
	}
	return runBazelEdits(real, argv, pkg, os.Stderr)
}

// bazelCanonicalPackage returns the import path that an importmap stands for.
// Gazelle gives a vendored package <prefix>/vendor/<path>, or vendor/<path>
// with an empty prefix; any other importmap is compiled unmodified.
func bazelCanonicalPackage(importmap string) string {
	if importmap == "vendor" || strings.HasPrefix(importmap, "vendor/") {
		importmap = "/" + importmap
	}
	if i := strings.LastIndex(importmap, "/vendor/"); i >= 0 {
		return importmap[i+len("/vendor/"):]
	}
	return importmap
}

// bazelEdits reports whether ddto edits pkg's compilation.
func bazelEdits(pkg string, race bool, arg0 string) bool {
	switch pkg {
	case "testing", "os/signal", instrument.TestifySuiteImport, instrument.GoleakImport:
		return true
	case "runtime":
		return readBazelConfig(arg0).PanicHook
	}
	if sdk, own := toolwrap.SDKCompilePackage(pkg); sdk != "" && own {
		return true
	}
	return race && strings.HasPrefix(pkg, miniModule+"/")
}

func readBazelConfig(arg0 string) bazelConfig {
	var config bazelConfig
	if data, err := os.ReadFile(filepath.Join(filepath.Dir(arg0), bazelCompilerConfig)); err == nil {
		_ = json.Unmarshal(data, &config)
	}
	return config
}

func compilerFlag(args []string, name string) string {
	for i, arg := range args {
		if arg == "--" {
			break
		}
		if arg == name && i+1 < len(args) {
			return args[i+1]
		}
		if value, ok := strings.CutPrefix(arg, name+"="); ok {
			return value
		}
	}
	return ""
}

func compilerBoolFlag(args []string, name string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == name || arg == name+"=true" {
			return true
		}
	}
	return false
}

// bazelSourceStart returns the index of the first Go source: after "--", as
// rules_go passes them, or the trailing run of .go arguments, as cmd/go does.
func bazelSourceStart(args []string) int {
	for i, arg := range args {
		if arg == "--" {
			return i + 1
		}
	}
	start := len(args)
	for start > 0 && strings.HasSuffix(args[start-1], ".go") {
		start--
	}
	return start
}

// errBazelUnsupported is a library whose code ddto does not recognize: it
// compiles unmodified, with a warning, as under ddtest instrument.
type errBazelUnsupported struct{ message string }

func (e errBazelUnsupported) Error() string { return e.message }

// bazelCompile is one edited compilation.
type bazelCompile struct {
	argv     []string // argv[0] is the compiler's name, argv[1:] its arguments
	start    int      // index in argv of the first Go source
	temp     string
	replaced map[int][]byte
	added    map[string]string // generated file name -> content
	extra    []string          // compiler inputs that other edits prepared
	cleanups []func()
}

func runBazelEdits(real string, argv []string, pkg string, stderr io.Writer) int {
	temp, err := os.MkdirTemp("", "ddtest-instrument-compile-")
	if err != nil {
		fmt.Fprintln(stderr, version.BuildLogPrefix+" ERROR:", err)
		return 2
	}
	defer os.RemoveAll(temp)
	c := &bazelCompile{argv: argv, start: 1 + bazelSourceStart(argv[1:]), temp: temp, replaced: map[int][]byte{}, added: map[string]string{}}
	defer func() {
		for _, cleanup := range c.cleanups {
			cleanup()
		}
	}()
	switch {
	case pkg == "testing":
		err = c.testing()
	case pkg == "os/signal":
		err = c.signal()
	case pkg == "runtime":
		err = c.runtime()
	case pkg == instrument.TestifySuiteImport:
		err = c.testify()
	case pkg == instrument.GoleakImport:
		err = c.goleak()
	case strings.HasPrefix(pkg, miniModule+"/"):
		err = c.norace()
	default:
		err = c.sdk(pkg)
	}
	var unsupported errBazelUnsupported
	if errors.As(err, &unsupported) {
		fmt.Fprintln(stderr, version.BuildLogPrefix+" WARN:", unsupported.message)
		return execBazelCompiler(real, argv)
	}
	var forwarded []string
	if err == nil {
		forwarded, err = c.finish()
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s ERROR: %s: %v\n", version.BuildLogPrefix, pkg, err)
		return 2
	}
	return runBazelCompiler(real, forwarded, stderr)
}

// sources reads the compilation's Go files, keyed by their names on the
// command line, and returns each name's index.
func (c *bazelCompile) sources() (map[string][]byte, map[string]int, error) {
	files, index := map[string][]byte{}, map[string]int{}
	for i := c.start; i < len(c.argv); i++ {
		data, err := os.ReadFile(c.argv[i])
		if err != nil {
			return nil, nil, err
		}
		files[c.argv[i]], index[c.argv[i]] = data, i
	}
	if len(files) == 0 {
		return nil, nil, errors.New("no Go sources on the command line")
	}
	return files, index, nil
}

func (c *bazelCompile) replace(edited map[string][]byte, index map[string]int) {
	for name, data := range edited {
		c.replaced[index[name]] = data
	}
}

// goroot is the SDK's root: this executable is $GOROOT/pkg/tool/<goos_goarch>/compile.
func (c *bazelCompile) goroot() string {
	dir, err := filepath.Abs(filepath.Dir(c.argv[0]))
	if err != nil {
		dir = filepath.Dir(c.argv[0])
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(dir)))
}

func (c *bazelCompile) gorootSources(pkg string) (map[string][]byte, error) {
	dir := filepath.Join(c.goroot(), "src", filepath.FromSlash(pkg))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sources := map[string][]byte{}
	for _, entry := range entries {
		if name := entry.Name(); strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			if sources[filepath.Join(dir, name)], err = os.ReadFile(filepath.Join(dir, name)); err != nil {
				return nil, err
			}
		}
	}
	return sources, nil
}

func (c *bazelCompile) testing() error {
	files, index, err := c.sources()
	if err != nil {
		return err
	}
	exithook, err := c.gorootSources("internal/runtime/exithook")
	exitHook := err == nil && instrument.ExitHookToolchainError(exithook) == nil
	var rewritten instrument.TestingSources
	if exitHook {
		rewritten, err = instrument.TransformWithTimeout(files)
	} else {
		rewritten, err = instrument.TransformWithFuzz(files)
	}
	if err != nil {
		return err
	}
	c.replace(rewritten.Files, index)
	c.added["zz_dd_ci_visibility_hooks.go"] = instrument.MiniHooks(rewritten.ParallelStop, rewritten.Timeout)
	if exitHook {
		c.added["zz_dd_ci_visibility_exit.go"] = instrument.ExitHook
	}
	return nil
}

func (c *bazelCompile) signal() error {
	files, index, err := c.sources()
	if err != nil {
		return err
	}
	edited, err := instrument.TransformSignal(files)
	if err != nil {
		// As under ddtest instrument, Mini then handles no signal.
		return errBazelUnsupported{"os/signal is not instrumented: " + err.Error()}
	}
	c.replace(edited, index)
	c.added["zz_dd_ci_visibility_signal.go"] = instrument.SignalHook
	return nil
}

func (c *bazelCompile) runtime() error {
	files, index, err := c.sources()
	if err != nil {
		return err
	}
	edited, err := instrument.TransformPanic(files)
	if err != nil {
		return errBazelUnsupported{"the panic hook is not installed: " + err.Error() + "; an unrecovered panic still loses the test process's events"}
	}
	c.replace(edited, index)
	c.added["zz_dd_ci_visibility_panic.go"] = instrument.PanicHook
	return nil
}

func (c *bazelCompile) testify() error {
	files, index, err := c.sources()
	if err != nil {
		return err
	}
	rewritten, err := instrument.TransformTestifyPackage(files, true)
	if errors.Is(err, instrument.ErrUnsupportedAPI) {
		return errBazelUnsupported{fmt.Sprintf("Testify is not instrumented (%v); its suites run as ordinary tests without Testify suite metadata", err)}
	}
	if err != nil {
		return err
	}
	c.replace(rewritten, index)
	c.added["zz_dd_ci_visibility_testify.go"] = instrument.TestifyEntryHook("", true)
	return nil
}

func (c *bazelCompile) goleak() error {
	files, index, err := c.sources()
	if err != nil {
		return err
	}
	unsupported := func(err error) error {
		return errBazelUnsupported{fmt.Sprintf("goleak is not instrumented (%v); its leak checks run without CI goroutine filters and can report CI workers", err)}
	}
	if err := instrument.GoleakAPI(files); err != nil {
		if errors.Is(err, instrument.ErrUnsupportedAPI) {
			return unsupported(err)
		}
		return err
	}
	edited := map[string][]byte{}
	for name, data := range files {
		out, found, err := instrument.TransformGoleakEntry(name, data)
		if errors.Is(err, instrument.ErrUnsupportedAPI) {
			return unsupported(err)
		}
		if err != nil {
			return err
		}
		if found {
			edited[name] = out
		}
	}
	if len(edited) == 0 {
		return unsupported(fmt.Errorf("%w: goleak.Find entry was not found", instrument.ErrUnsupportedAPI))
	}
	c.replace(edited, index)
	c.added["zz_dd_ci_visibility_goleak.go"] = instrument.GoleakEntryHook()
	return nil
}

// norace keeps the race detector out of Mini, as ddtest instrument's overlay
// does with -race.
func (c *bazelCompile) norace() error {
	files, index, err := c.sources()
	if err != nil {
		return err
	}
	edited := map[string][]byte{}
	for name, data := range files {
		if out, ok := appendNorace(nil, data); ok {
			edited[name] = out
		}
	}
	c.replace(edited, index)
	return nil
}

// sdk applies the SDK guards and span copies, which already work from the
// compiler's arguments alone.
func (c *bazelCompile) sdk(pkg string) error {
	sdk, _ := toolwrap.SDKCompilePackage(pkg)
	forwarded, cleanup, err := prepareSDKCICompile(c.argv, sdk)
	if err != nil {
		if sdk == sdkTracerPackage && errors.Is(err, instrument.ErrUnsupportedAPI) {
			return errBazelUnsupported{"SDK span copies disabled: " + err.Error()}
		}
		return err
	}
	c.cleanups = append(c.cleanups, cleanup)
	for i := c.start; i < len(forwarded); i++ {
		if i >= len(c.argv) {
			c.extra = append(c.extra, forwarded[i])
		} else if forwarded[i] != c.argv[i] {
			data, err := os.ReadFile(forwarded[i])
			if err != nil {
				return err
			}
			c.replaced[i] = data
		}
	}
	return nil
}

// finish writes the edited and generated files and returns the compiler's
// arguments. Generated files take a name in the package's directory, made
// absolute so that the compiler's -trimpath rewrites it as it rewrites the
// package's own files.
func (c *bazelCompile) finish() ([]string, error) {
	out := append([]string(nil), c.argv...)
	var contents [][]byte
	indexes := make([]int, 0, len(c.replaced))
	for i := range c.replaced {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	for _, i := range indexes {
		data := c.replaced[i]
		path := filepath.Join(c.temp, fmt.Sprintf("%03d_%s", i, filepath.Base(c.argv[i])))
		if err := os.WriteFile(path, data, 0600); err != nil {
			return nil, err
		}
		out[i] = path
		contents = append(contents, data)
	}
	dir, err := filepath.Abs(filepath.Dir(c.argv[c.start]))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(c.added))
	for name := range c.added {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		data := []byte(instrument.HookFileLine(dir, name) + c.added[name])
		path := filepath.Join(c.temp, name)
		if err := os.WriteFile(path, data, 0600); err != nil {
			return nil, err
		}
		out = append(out, path)
		contents = append(contents, data)
	}
	for _, path := range c.extra {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		out = append(out, path)
		contents = append(contents, data)
	}
	return addStandardImports(out, contents, c.temp)
}

// addStandardImports adds to the compilation's import configuration the
// standard packages that the edits import and the package's own sources do
// not, such as internal/runtime/exithook in testing. rules_go and the
// standard library's go install both name standard archives
// <root>/<import path>.a; any other import is an error.
func addStandardImports(args []string, contents [][]byte, temp string) ([]string, error) {
	position, config := -1, ""
	for i, arg := range args {
		if arg == "-importcfg" && i+1 < len(args) {
			position, config = i+1, args[i+1]
			break
		}
		if value, ok := strings.CutPrefix(arg, "-importcfg="); ok {
			position, config = i, value
			break
		}
	}
	if position < 0 {
		return nil, errors.New("the compilation has no -importcfg")
	}
	data, err := os.ReadFile(config)
	if err != nil {
		return nil, err
	}
	have, root := map[string]bool{}, ""
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		verb, rest, ok := strings.Cut(strings.TrimSpace(scanner.Text()), " ")
		if !ok || verb != "packagefile" && verb != "importmap" {
			continue
		}
		key, value, _ := strings.Cut(rest, "=")
		have[key] = true
		if verb == "packagefile" && root == "" && standardImport(key) && strings.HasSuffix(filepath.ToSlash(value), "/"+key+".a") {
			root = strings.TrimSuffix(filepath.ToSlash(value), "/"+key+".a")
		}
	}
	var missing []string
	seen := map[string]bool{}
	for _, content := range contents {
		file, err := parser.ParseFile(token.NewFileSet(), "", content, parser.ImportsOnly)
		if err != nil {
			return nil, err
		}
		for _, spec := range file.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			if path == "unsafe" || path == "C" || have[path] || seen[path] {
				continue
			}
			seen[path] = true
			missing = append(missing, path)
		}
	}
	if len(missing) == 0 {
		return args, nil
	}
	sort.Strings(missing)
	extra := ""
	for _, path := range missing {
		if !standardImport(path) || root == "" {
			return nil, fmt.Errorf("the edits import %s, which the compilation's -importcfg lacks", path)
		}
		archive := root + "/" + path + ".a"
		if _, err := os.Stat(filepath.FromSlash(archive)); err != nil {
			return nil, fmt.Errorf("the edits import %s: %v", path, err)
		}
		extra += "packagefile " + path + "=" + archive + "\n"
	}
	updated := filepath.Join(temp, "importcfg")
	if err := os.WriteFile(updated, append(append(bytes.Clone(data), '\n'), extra...), 0600); err != nil {
		return nil, err
	}
	out := append([]string(nil), args...)
	if strings.HasPrefix(out[position], "-importcfg=") {
		out[position] = "-importcfg=" + updated
	} else {
		out[position] = updated
	}
	return out, nil
}

func standardImport(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}
