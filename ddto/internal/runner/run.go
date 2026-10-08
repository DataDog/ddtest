package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/instrument"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

const SDKVersion = version.SDKVersion
const sdkPackage = "github.com/DataDog/dd-trace-go/v2/civisibility"
const miniPackage = "github.com/tonyredondo/dd-ci-testing-poc/testopt"

type goPackage struct {
	commandLine                        bool // Selected by the original package query, not dependency discovery.
	Dir, Name, ImportPath              string
	Standard                           bool
	GoFiles, TestGoFiles, XTestGoFiles []string
	Imports, TestImports, XTestImports []string
	Deps                               []string
	Module                             *struct {
		Path, Version string
		Main          bool
		Replace       *struct{ Path, Dir, Version string }
	}
	Error *struct{ Err string }
}
type Overlay struct {
	Replace map[string]string
	Testify *LibraryEntry `json:",omitempty"`
	Goleak  *LibraryEntry `json:",omitempty"`
}

type Plan struct {
	File, Dir                       string
	InstrumentedFiles, TestPackages int
	// Modfile is a temporary go.mod that provides a runtime the module does
	// not require; the module's own go.mod and go.sum stay untouched.
	Modfile string
	// Warnings name optional integrations skipped for unsupported libraries.
	Warnings         []string
	coverOverlay     bool
	orchestrionChain string
	launcher         *goLauncher
	orchestrion      bool
	mini             bool
	testify          bool
	goleak           bool
	goleakCache      string
}

// Prepare creates a complete plan before native Go compilation starts. Callers
// own the plan directory and must remove it after all compiler processes finish.
func Prepare(ctx context.Context, dir string, args []string) (Plan, error) {
	return PrepareRuntime(ctx, dir, args, SDK)
}

// PrepareRuntime selects the event runtime without changing Go's test options.
// A missing runtime is provided through a temporary modfile. Preparation is
// silent; RunRuntime streams provisioning diagnostics to its stderr writer.
func PrepareRuntime(ctx context.Context, dir string, args []string, runtime Runtime) (Plan, error) {
	opts, err := parseOptions(args, os.Getenv("GOFLAGS"))
	if err != nil {
		return Plan{}, err
	}
	dir = workingDirectory(dir, opts)
	if opts.help || explicitFiles(dir, opts.packages) {
		return Plan{}, errors.New("help and explicit Go file arguments run without instrumentation")
	}
	return prepare(ctx, dir, opts, runtime, nil)
}

// workingDirectory applies go test's -C, relative to the caller's directory.
func workingDirectory(dir string, opts options) string {
	if opts.chdir == "" || filepath.IsAbs(opts.chdir) {
		if opts.chdir != "" {
			return filepath.Clean(opts.chdir)
		}
		return dir
	}
	return filepath.Join(dir, opts.chdir)
}

// explicitFiles mirrors go test's file mode: an argument ending in .go selects
// it only when that argument names an existing file rather than a package.
func explicitFiles(dir string, packages []string) bool {
	for _, p := range packages {
		if !strings.HasSuffix(p, ".go") {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func prepare(ctx context.Context, dir string, opts options, runtime Runtime, progress io.Writer) (plan Plan, err error) {
	debug := debugFromContext(ctx)
	phase := debug.start("prepare")
	defer func() { phase.finish(err) }()
	runtimePackage := sdkPackage
	switch runtime {
	case SDK:
	case Mini:
		runtimePackage = miniPackage
	default:
		return plan, fmt.Errorf("unknown CI runtime: %s", runtime)
	}
	replacements := map[string]string{}
	if opts.overlay != "" {
		path := opts.overlay
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		phase := debug.start("read user overlay")
		data, e := os.ReadFile(path)
		if e != nil {
			phase.finish(e)
			return plan, e
		}
		var overlay Overlay
		if e = json.Unmarshal(data, &overlay); e != nil {
			phase.finish(e)
			return plan, e
		}
		for from, to := range overlay.Replace {
			if !filepath.IsAbs(from) {
				from = filepath.Join(dir, from)
			}
			if to != "" && !filepath.IsAbs(to) {
				to = filepath.Join(dir, to)
			}
			replacements[filepath.Clean(from)] = to
		}
		phase.finish(nil)
		debug.printf("user overlay entries=%d", len(replacements))
	}
	temp, e := os.MkdirTemp("", "dd-ci-testing-poc-")
	if e != nil {
		return plan, e
	}
	plan.Dir = temp
	plan.mini = runtime == Mini
	plan.orchestrion = isOrchestrionToolexec(opts.toolexec)
	defer func() {
		if err != nil {
			_ = os.RemoveAll(temp)
		}
	}()
	list := func(patterns ...string) (packages []goPackage, err error) {
		phase := debug.start("resolve packages")
		defer func() {
			phase.finish(err)
			debug.printf("package query patterns=%d resolved=%d", len(patterns), len(packages))
		}()
		listArgs := append([]string{"list", "-e", "-json=Dir,Name,ImportPath,Standard,GoFiles,TestGoFiles,XTestGoFiles,Imports,TestImports,XTestImports,Deps,Module,Error"}, opts.buildFlags...)
		listArgs = append(listArgs, patterns...)
		cmd := exec.CommandContext(ctx, "go", listArgs...)
		// Keep Env nil: os/exec then sets PWD to dir, so go list reports
		// package directories with the spelling of dir, even through symbolic
		// links. Relative patterns are matched against that same spelling.
		cmd.Dir = dir
		return readPackages(cmd, "resolve packages")
	}
	if runtime == Mini && opts.mod != "mod" {
		plan.Modfile, e = preprovideMini(ctx, dir, opts, temp, replacements, progress)
		if e != nil {
			return plan, e
		}
		if plan.Modfile != "" {
			opts.buildFlags = append(opts.buildFlags, "-modfile="+plan.Modfile)
		}
	}
	patterns := append(append([]string(nil), opts.packages...), "testing", runtimePackage)
	var packages []goPackage
	if opts.mod == "mod" {
		// -mod=mod lets a query add requirements to go.mod and go.sum. Native go
		// test adds them only for the user's packages, never for the runtime it
		// does not import. List those packages with the user's flags, then
		// resolve the runtime read-only: a missing runtime is provided below.
		packages, e = list(patterns[:len(patterns)-1]...)
		if e == nil && !slices.ContainsFunc(packages, func(p goPackage) bool { return p.ImportPath == runtimePackage }) {
			var probe []goPackage
			probe, e = list("-mod=readonly", runtimePackage)
			packages = append(packages, probe...)
		}
	} else {
		packages, e = list(patterns...)
	}
	if e != nil {
		return plan, e
	}
	for _, p := range packages {
		if p.ImportPath == runtimePackage && p.Error != nil && plan.Modfile == "" {
			// The module does not require the runtime: provide it through a
			// temporary go.mod instead of failing or editing the module.
			if plan.Modfile, e = provideRuntime(ctx, dir, opts, runtime, temp, replacements, progress); e != nil {
				return plan, fmt.Errorf("%s: %s\nddtest could not provide it: %w", p.ImportPath, p.Error.Err, e)
			}
			opts.buildFlags = append(opts.buildFlags, "-modfile="+plan.Modfile)
			if packages, e = list(patterns...); e != nil {
				return plan, e
			}
			break
		}
	}
	for _, p := range packages {
		if p.Error != nil {
			return plan, fmt.Errorf("%s: %s", p.ImportPath, p.Error.Err)
		}
	}
	var native *goPackage
	foundRuntime := false
	for i := range packages {
		p := &packages[i]
		if p.ImportPath == "testing" {
			native = p
		}
		if p.ImportPath == runtimePackage {
			if runtime == SDK && (p.Module == nil || p.Module.Version != SDKVersion || p.Module.Replace != nil) {
				return plan, fmt.Errorf("POC requires unmodified dd-trace-go %s", SDKVersion)
			}
			if debug != nil && p.Module != nil {
				debug.printf("runtime module_version=%q replacement=%t", p.Module.Version, p.Module.Replace != nil)
			}
			foundRuntime = true
		}
	}
	if !foundRuntime || native == nil {
		return plan, fmt.Errorf("missing testing or selected CI runtime package")
	}
	rewritten, e := transformTesting(native, replacements, runtime, debug)
	if e != nil {
		return plan, e
	}
	// Backing files are shared only within this plan and finalized before Go starts.
	backingByContent := map[string]string{}
	add := func(logical, content string) error {
		if _, exists := replacements[logical]; exists {
			return fmt.Errorf("generated file conflicts with user overlay: %s", logical)
		}
		if _, e := os.Lstat(logical); e == nil {
			return fmt.Errorf("generated file already exists: %s", logical)
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		backing, exists := backingByContent[content]
		if !exists {
			backing = filepath.Join(temp, fmt.Sprintf("generated-%d.go", len(replacements)))
			if e := os.WriteFile(backing, []byte(content), 0600); e != nil {
				return e
			}
			backingByContent[content] = backing
		}
		replacements[logical] = backing
		return nil
	}
	for logical, src := range rewritten.Files {
		backing := filepath.Join(temp, filepath.Base(logical))
		if e = os.WriteFile(backing, src, 0600); e != nil {
			return plan, e
		}
		replacements[logical] = backing
	}
	hooks := hooksForRuntime(runtime, rewritten.ParallelStop)
	if plan.orchestrion && plan.mini {
		hooks += miniOrchestrionCacheMarker
	}
	if e = add(filepath.Join(native.Dir, "zz_dd_ci_visibility_hooks.go"), hooks); e != nil {
		return plan, e
	}
	for _, p := range packages {
		if p.ImportPath == "testing" || p.ImportPath == runtimePackage || len(p.TestGoFiles)+len(p.XTestGoFiles) == 0 {
			continue
		}
		if p.Module == nil {
			return plan, fmt.Errorf("stdlib tests are outside this POC: %s", p.ImportPath)
		}
		content := "package " + p.Name + "_test\nimport _ " + fmt.Sprintf("%q", runtimePackage) + "\n"
		if runtime == Mini {
			// The caller's compiler path identifies this package without baking
			// its directory into the source. Identical packages still share files.
			content = "package " + p.Name + "_test\nimport __dd_ci_runtime " + fmt.Sprintf("%q", runtimePackage) + "\nfunc init() { __dd_ci_runtime.RegisterTestPackage() }\n"
		}
		if e = add(filepath.Join(p.Dir, "zz_dd_ci_visibility_test.go"), content); e != nil {
			return plan, e
		}
		plan.TestPackages++
	}
	libraries, e := resolveTestLibraries(ctx, dir, opts, packages)
	if e != nil {
		return plan, e
	}
	testifyPhase := debug.start("instrument testify")
	testify, warning, e := prepareTestifyPackage(libraries[instrument.TestifySuiteImport], replacements, runtime, temp)
	testifyPhase.finish(e)
	debug.printf("testify detected=%t instrumented=%t warning=%t", libraries[instrument.TestifySuiteImport] != nil, testify != nil, warning != "")
	if e != nil {
		return plan, e
	}
	if warning != "" {
		plan.Warnings = append(plan.Warnings, warning)
	}
	plan.testify = testify != nil
	if testify != nil {
		// Exported marker changes testing's content ID, which suite imports.
		// Native compiler identity can then be shared with uninstrumented packages.
		marker := replacements[filepath.Join(native.Dir, "zz_dd_ci_visibility_hooks.go")]
		file, e := os.OpenFile(marker, os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			return plan, e
		}
		_, e = file.WriteString(instrument.TestifyCacheMarker(testify.Fingerprint))
		closeErr := file.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			return plan, e
		}
	}
	var goleak *LibraryEntry
	if runtime == Mini {
		goleakPhase := debug.start("instrument goleak")
		goleak, warning, e = prepareGoleak(libraries[instrument.GoleakImport], replacements, temp)
		goleakPhase.finish(e)
		debug.printf("goleak detected=%t instrumented=%t warning=%t", libraries[instrument.GoleakImport] != nil, goleak != nil, warning != "")
		if e != nil {
			return plan, e
		}
		if warning != "" {
			plan.Warnings = append(plan.Warnings, warning)
		}
		if goleak != nil {
			plan.goleak = true
			plan.goleakCache, e = goleakCacheFlag(dir, opts, libraries[instrument.GoleakImport], goleak.Fingerprint)
			if e != nil {
				return plan, e
			}
		}
	}
	plan.coverOverlay = needsCoverOverlay(dir, opts, packages, []goPackage{*native})
	plan.File = filepath.Join(temp, "overlay.json")
	plan.InstrumentedFiles = len(rewritten.Files)
	writePhase := debug.start("write overlay")
	encoded, e := json.Marshal(Overlay{Replace: replacements, Testify: testify, Goleak: goleak})
	if e != nil {
		writePhase.finish(e)
		return plan, e
	}
	e = os.WriteFile(plan.File, encoded, 0600)
	writePhase.finish(e)
	if e != nil {
		return plan, e
	}
	if e = prepareOrchestrionLauncher(ctx, dir, opts, &plan, progress); e != nil {
		return plan, e
	}
	debug.printf("plan ready testing_files=%d test_packages=%d overlay_entries=%d generated_backing_files=%d temporary_modfile=%t cover_bridge=%t", plan.InstrumentedFiles, plan.TestPackages, len(replacements), len(backingByContent), plan.Modfile != "", plan.coverOverlay)
	return plan, nil
}

func transformTesting(native *goPackage, replacements map[string]string, runtime Runtime, debug *cliDebug) (result instrument.TestingSources, err error) {
	phase := debug.start("instrument testing")
	defer func() { phase.finish(err) }()
	files := map[string][]byte{}
	for _, file := range native.GoFiles {
		path := filepath.Join(native.Dir, file)
		actual := path
		if to, ok := replacements[path]; ok {
			actual = to
		}
		src, e := os.ReadFile(actual)
		if e != nil {
			return result, e
		}
		files[path] = src
	}
	transform := instrument.Transform
	if runtime == Mini {
		transform = instrument.TransformWithFuzz
	}
	result, err = transform(files)
	if err != nil {
		return result, err
	}
	debug.printf("testing sources=%d rewritten=%d fuzz=%t parallel_stop=%t", len(files), len(result.Files), runtime == Mini, result.ParallelStop)
	return result, nil
}

// Run retains native test output, flags, working directory, and exit status.
// It deliberately preserves the user's test-result caching choice.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return RunRuntime(ctx, args, SDK, stdin, stdout, stderr)
}

// RunRuntime compiles and executes tests using one explicitly selected runtime.
// Interrupt and termination signals are forwarded to go test instead of ending
// ddtest first: the plan is removed only after Go exits, and the result keeps
// go test's exit status. A signal during preparation stops it and cleans up.
func RunRuntime(ctx context.Context, args []string, runtime Runtime, stdin io.Reader, stdout, stderr io.Writer) (exitCode int) {
	ctx = withCLIDebug(ctx, stderr)
	debug := debugFromContext(ctx)
	if debug != nil {
		stderr = debug.writer
	}
	started := debug.start("ddtest")
	defer func() {
		var err error
		if exitCode != 0 {
			err = errors.New("command failed")
		}
		started.finish(err)
		debug.printf("ddtest exit_code=%d", exitCode)
	}()
	debug.printf("runtime=%s", runtime)
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, forwardedSignals...)
	defer signal.Stop(signals)
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	opts, err := parseOptions(args, os.Getenv("GOFLAGS"))
	if err != nil {
		fmt.Fprintln(stderr, version.BuildLogPrefix+" ERROR:", err)
		return 2
	}
	applyGoLauncher(ctx, &opts)
	dir := workingDirectory(cwd, opts)
	debug.printf("working_directory=%q package_patterns=%d build_flags=%d", dir, len(opts.packages), len(opts.buildFlags))
	if opts.help || explicitFiles(dir, opts.packages) {
		if !opts.help {
			fmt.Fprintln(stderr, version.BuildLogPrefix+" WARN: explicit Go files run without CI Visibility instrumentation")
		}
		debug.printf("instrumentation bypass help=%t explicit_files=%t", opts.help, !opts.help)
		// Native go test handles -C, help and file mode itself.
		return runGo(ctx, cwd, append([]string{"test"}, args...), nil, signals, stdin, stdout, stderr)
	}
	plan, interrupted, err := prepareInterruptibly(ctx, dir, opts, runtime, signals, stderr)
	if err == nil {
		defer os.RemoveAll(plan.Dir)
	}
	if interrupted != nil {
		debug.printf("preparation interrupted")
		return interruptedStatus(interrupted)
	}
	if err != nil {
		fmt.Fprintln(stderr, version.BuildLogPrefix+" ERROR:", err)
		return 2
	}
	for _, warning := range plan.Warnings {
		fmt.Fprintln(stderr, version.BuildLogPrefix+" WARN:", warning)
	}
	var tool string
	var env []string
	if plan.coverOverlay || plan.testify || plan.goleak || plan.orchestrion {
		executable, e := os.Executable()
		if e != nil {
			fmt.Fprintln(stderr, e)
			return 2
		}
		if tool, e = toolCommand(executable, plan.File, plan.toolMode()); e != nil {
			fmt.Fprintln(stderr, e)
			return 2
		}
		// Reset private chain state inherited from a parent instrumented test.
		// An unrelated wrapper must never inherit Orchestrion's package bypass.
		chain := opts.toolexec
		if plan.orchestrionChain != "" {
			chain = plan.orchestrionChain
		}
		env = []string{userToolexecEnv + "=" + chain, orchestrionBypassEnv + "=false"}
		if plan.orchestrion {
			env[1] = orchestrionBypassEnv + "=" + string(runtime)
		}
	}
	debug.printf("tool selection testify=%t goleak=%t cover=%t user_toolexec=%t orchestrion=%t", plan.testify, plan.goleak, plan.coverOverlay, opts.toolexec != "", plan.orchestrion)
	forwarded := goTestArguments(plan, opts, tool)
	if plan.launcher != nil {
		ctx = context.WithValue(ctx, goLauncherKey{}, *plan.launcher)
	}
	return runGo(ctx, dir, forwarded, env, signals, stdin, stdout, stderr)
}

// prepareInterruptibly cancels package resolution when a signal arrives. The
// watcher has stopped before returning, so later signals reach go test.
func prepareInterruptibly(ctx context.Context, dir string, opts options, runtime Runtime, signals <-chan os.Signal, progress io.Writer) (Plan, os.Signal, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var interrupted os.Signal
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case interrupted = <-signals:
			cancel()
		case <-stop:
		}
	}()
	plan, err := prepare(ctx, dir, opts, runtime, progress)
	close(stop)
	<-stopped
	return plan, interrupted, err
}

// goTestArguments keeps the user's arguments in order. Our overlay already
// contains the user's entries and our -toolexec chains the user's, so those
// flags are replaced wherever they appear before the test binary arguments.
// Go applies per-package flags in order: the goleak cache marker follows the
// last user -gcflags, which keeps goleak's other effective compiler flags.
func goTestArguments(plan Plan, opts options, tool string) []string {
	forwarded := []string{"test", "-overlay=" + plan.File}
	if plan.Modfile != "" {
		forwarded = append(forwarded, "-modfile="+plan.Modfile)
	}
	if tool != "" {
		forwarded = append(forwarded, "-toolexec="+tool)
	}
	lastGcflags := -1
	if plan.goleakCache != "" {
		for i, argument := range opts.arguments {
			if argument.flag == "gcflags" {
				lastGcflags = i
			}
		}
		if lastGcflags < 0 {
			forwarded = append(forwarded, plan.goleakCache)
		}
	}
	for i, argument := range opts.arguments {
		if argument.flag == "overlay" || argument.flag == "toolexec" && tool != "" || argument.flag == "modfile" && plan.Modfile != "" {
			continue
		}
		forwarded = append(forwarded, argument.raw...)
		if i == lastGcflags {
			forwarded = append(forwarded, plan.goleakCache)
		}
	}
	return forwarded
}

// runGo retains native output and exit status. Signals are forwarded to Go
// rather than killing it; context cancellation interrupts it, then kills it
// after a grace period.
func runGo(ctx context.Context, dir string, args, envOverrides []string, signals <-chan os.Signal, stdin io.Reader, stdout, stderr io.Writer) (exitCode int) {
	debug := debugFromContext(ctx)
	phase := debug.start("go test")
	defer func() {
		var err error
		if exitCode != 0 {
			err = errors.New("command failed")
		}
		phase.finish(err)
		debug.printf("go test exit_code=%d", exitCode)
	}()
	command := []string{"go"}
	if launcher, ok := ctx.Value(goLauncherKey{}).(goLauncher); ok {
		command = launcher.command
	}
	argv := append(append([]string(nil), command[1:]...), args...)
	cmd := exec.Command(command[0], argv...)
	cmd.Dir = dir
	if len(envOverrides) != 0 {
		// Environ computes PWD from Dir, including its symbolic-link spelling.
		cmd.Env = append(cmd.Environ(), envOverrides...)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(stderr, version.BuildLogPrefix+" ERROR:", err)
		return 2
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	canceled := ctx.Done()
	var kill <-chan time.Time
	for {
		select {
		case err := <-done:
			if err == nil {
				return 0
			}
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				if code, signaled := signalExitCode(exit.ProcessState); signaled {
					return code
				}
				return exit.ExitCode()
			}
			fmt.Fprintln(stderr, version.BuildLogPrefix+" ERROR:", err)
			return 2
		case s := <-signals:
			debug.printf("forwarding signal to go test")
			_ = cmd.Process.Signal(s)
		case <-canceled:
			debug.printf("go test context canceled; interrupting")
			canceled = nil
			_ = interruptProcess(cmd.Process)
			timer := time.NewTimer(10 * time.Second)
			defer timer.Stop()
			kill = timer.C
		case <-kill:
			debug.printf("go test grace period expired; killing")
			kill = nil
			_ = cmd.Process.Kill()
		}
	}
}
