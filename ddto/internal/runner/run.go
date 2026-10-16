package runner

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/bundle"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/goenv"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/instrument"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

const SDKVersion = version.SDKVersion
const sdkPackage = "github.com/DataDog/dd-trace-go/v2/civisibility"
const miniPackage = "github.com/tonyredondo/dd-ci-testing-poc/testopt"

type goPackage struct {
	commandLine                        bool // Selected by the original package query, not dependency discovery.
	Dir, Name, ImportPath              string
	Standard                           bool
	DepOnly                            bool // Listed by -deps only as a dependency of the named packages.
	GoFiles, TestGoFiles, XTestGoFiles []string
	Imports, TestImports, XTestImports []string
	// ForTest and the other files are listed only for file coverage.
	ForTest                                               string
	CgoFiles, EmbedFiles, TestEmbedFiles, XTestEmbedFiles []string
	EmbedPatterns, TestEmbedPatterns, XTestEmbedPatterns  []string
	SFiles, CFiles, CXXFiles, MFiles, HFiles, FFiles      []string
	SwigFiles, SwigCXXFiles, SysoFiles                    []string
	Module                                                *struct {
		Path, Version, Dir string
		Main               bool
		Replace            *struct{ Path, Dir, Version string }
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
	Modfile          string
	Workfile         string
	modfileOverlay   bool // Temporary workspaces read the explicit modfile through the overlay.
	moduleWorkspace  bool // The caller used module mode; translate its module flags for the workspace.
	workspaceGoFlags string
	// Warnings name optional integrations skipped for unsupported libraries.
	Warnings     []string
	coverOverlay bool
	// fileCoverage reports that the build has file marks; the test
	// processes then get fileCoverageParallel, for
	// --experimental-file-coverage=parallel, whatever they would inherit.
	fileCoverage, fileCoverageParallel bool
	orchestrionChain                   string
	launcher                           *goLauncher
	orchestrion                        bool
	mini                               bool
	sdkCI                              bool
	sdkMirrorDisabled                  bool
	testify                            bool
	goleak                             bool
	compilerCache                      []string
	// workspaceLock keeps a cached vendor workspace from being pruned.
	workspaceLock *os.File
	// gorootMirror runs go from a mirror of a toolchain in GOMODCACHE.
	gorootMirror *gorootMirror
	// environment is the go environment that preparation read.
	environment *goEnvironment
	// toolWrapperLock keeps the -toolexec wrapper executable from pruners.
	toolWrapperLock *os.File
	// bundledMini holds the Mini sources that a release build carries, once
	// provisioning extracts them.
	bundledMini *bundledMini
	// execHelper is the cross-compilation program Go would run test binaries
	// with, without -exec; execWrapper restores the caller's Go settings before
	// a -exec program.
	execHelper  string
	execWrapper string
	// testLauncher reports that a -exec program or Go's cross-compilation
	// helper starts the test binaries, and so sees their environment first.
	testLauncher bool
	// sessionRefusal is why the selected runtime's test processes cannot
	// report in ddto's test session, or "".
	sessionRefusal string
	// tests describes the test binaries and the packages that have tests,
	// with their modules, for the Git worker and the test processes.
	tests utils.CLITests
	// linkSignal makes ddto's tool tell the Git worker when go test links a
	// test binary.
	linkSignal bool
	// goVersion is the selected toolchain's version, as go env reports it,
	// and goExperiment its GOEXPERIMENT setting.
	goVersion, goExperiment string
}

// savedEnvironment records the caller's values of the Go settings that go test
// receives from this plan instead.
func (p Plan) savedEnvironment() []string {
	var saved []string
	if p.Workfile != "" {
		saved = append(saved, goenv.Save("GOWORK"))
		if p.moduleWorkspace {
			saved = append(saved, goenv.Save("GOFLAGS"))
		}
	}
	// Only Mini's test runtime restores them; SDK tests see the mirror.
	if p.gorootMirror != nil && p.mini {
		saved = append(saved, p.gorootMirror.saved...)
	}
	return saved
}

// Release lets other runs prune the plan's cached vendor workspace and
// toolchain mirror, and restores this process's environment. Call it after
// all go processes using the plan finish, before removing Dir.
func (p Plan) Release() {
	if p.workspaceLock != nil {
		_ = p.workspaceLock.Close()
	}
	p.gorootMirror.release()
	if p.toolWrapperLock != nil {
		_ = p.toolWrapperLock.Close()
	}
	p.bundledMini.release()
}

// GoCommand returns the go command for builds that use this plan, and the
// environment entries it needs: a mirrored toolchain's go and its GOROOT, or
// go and none.
func (p Plan) GoCommand() (name string, env []string) {
	if p.gorootMirror == nil {
		return "go", nil
	}
	return p.gorootMirror.toolchain.path, p.gorootMirror.toolchain.env[:1]
}

// Prepare creates a complete plan before native Go compilation starts. Callers
// own the plan directory and must remove it, and call Release, after all
// compiler processes finish.
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
	userOverlay := make(map[string]bool, len(replacements))
	for path := range replacements {
		userOverlay[path] = true
	}
	vendorProvided := false
	temp, e := os.MkdirTemp("", "dd-ci-testing-poc-")
	if e != nil {
		return plan, e
	}
	plan.Dir = temp
	plan.mini = runtime == Mini
	plan.orchestrion = isOrchestrionToolexec(opts.toolexec)
	defer func() {
		if err != nil {
			plan.Release()
			_ = os.RemoveAll(temp)
		}
	}()
	opts.environment, e = readGoEnvironment(ctx, dir)
	plan.environment = opts.environment
	if e != nil {
		return plan, e
	}
	if !supportsGoToolchain(opts.environment.GOVERSION) {
		return plan, fmt.Errorf("ddto requires Go 1.25 or newer; selected toolchain is %q", opts.environment.GOVERSION)
	}
	if needsGOROOTMirror(opts.environment) {
		// Go rejects overlays beneath GOMODCACHE, where GOTOOLCHAIN keeps
		// downloaded toolchains: run go from a mirror of that GOROOT.
		plan.gorootMirror, e = prepareGOROOTMirror(ctx, opts.environment, dir, debug)
		if e != nil {
			return plan, mirrorUnavailable(e)
		}
		ctx = withGoToolchain(ctx, plan.gorootMirror)
	}
	if archive := bundle.Runtime(); runtime == Mini && archive != nil {
		plan.bundledMini = &bundledMini{env: opts.environment, dir: dir, archive: archive, temp: temp}
		ctx = withBundledMini(ctx, plan.bundledMini)
	}
	plan.testLauncher = opts.exec != "" || opts.environment.GOOS != opts.environment.GOHOSTOS || opts.environment.GOARCH != opts.environment.GOHOSTARCH
	plan.goVersion, plan.goExperiment = opts.environment.GOVERSION, opts.environment.GOEXPERIMENT
	plan.tests.GOARCH, plan.tests.RunsEveryTest = opts.environment.GOARCH, opts.runsEveryTest
	debug.printf("toolchain=%q", opts.environment.GOVERSION)
	if runtime == Mini {
		if work := workspaceFile(dir); work != "" {
			plan.Workfile, e = provideMiniWorkspace(ctx, dir, work, temp, opts.environment.GOMODCACHE, replacements)
			if e != nil {
				return plan, e
			}

			// Use the vendor directory only where Go would: a go mod vendor tree
			// next to go.work belongs to its module, not to the workspace.
			vendor := filepath.Join(filepath.Dir(work), "vendor")
			workData, _ := readModuleFile(work, replacements)
			if goSelectsVendor(opts.mod, moduleDirective(workData, "go"), vendor, true, replacements) {
				replaced, err := workspaceReplacedModules(ctx, dir, plan.Workfile)
				if err != nil {
					return plan, err
				}
				selected := vendorManifest{dir: vendor, base: filepath.Dir(work), workReplaced: replaced}
				if plan.Workfile, plan.workspaceLock, e = vendorWorkspace(ctx, plan.Workfile, selected, replacements); e != nil {
					return plan, e
				}
				vendorProvided = true
			}
			opts.workfile = plan.Workfile
		} else if root := moduleRoot(dir); root != "" {
			vendor := filepath.Join(root, "vendor")
			source := opts.modfile
			if source == "" {
				source = filepath.Join(root, "go.mod")
			} else if !filepath.IsAbs(source) {
				source = filepath.Join(dir, source)
			}
			data, _ := readModuleFile(source, replacements)
			_, manifestErr := os.Stat(filepath.Join(vendor, "modules.txt"))
			_, runtimeErr := os.Stat(filepath.Join(vendor, filepath.FromSlash(miniModule), "testopt"))
			if manifestErr == nil && os.IsNotExist(runtimeErr) && goSelectsVendor(opts.mod, moduleDirective(data, "go"), vendor, false, replacements) {
				if opts.modfile != "" {
					if e = overlaySelectedModfile(source, root, temp, data, &plan, replacements); e != nil {
						return plan, e
					}
				}
				// Workspace mode rejects -modfile in any spelling, including GOFLAGS.
				if e = useModuleWorkspaceFlags(&opts, &plan); e != nil {
					return plan, e
				}
				plan.Workfile, plan.workspaceLock, e = provideMiniVendorWorkspace(ctx, dir, root, temp, opts.environment.GOMODCACHE, replacements)
				if e != nil {
					return plan, e
				}
				vendorProvided = true
				opts.workfile = plan.Workfile
			}
		}
		if plan.Workfile == "" {
			if e = provideOlderModuleWorkspace(ctx, dir, &opts, &plan, replacements); e != nil {
				return plan, e
			}
		}
	}
	if plan.Workfile != "" && len(replacements) != 0 {
		if e = writeProvisionOverlay(temp, &opts, replacements); e != nil {
			return plan, e
		}
	}
	// The package query lists the named packages and, with -deps, each of their
	// dependencies once. Reachability follows Imports through that graph
	// instead of each package's Deps, a complete closure per package that grows
	// with the product of package count and dependency depth.
	listWith := func(fields string, patterns ...string) (packages []goPackage, err error) {
		phase := debug.start("resolve packages")
		defer func() {
			phase.finish(err)
			debug.printf("package query patterns=%d resolved=%d", len(patterns), len(packages))
		}()
		listArgs := append([]string{"list", "-e", "-json=" + fields}, opts.buildFlags...)
		listArgs = append(listArgs, patterns...)
		// Keep Env from os/exec, which sets PWD to dir, so go list reports
		// package directories with the spelling of dir, even through symbolic
		// links. Relative patterns are matched against that same spelling.
		cmd := goCommand(ctx, dir, listArgs...)
		if opts.workfile != "" {
			cmd.Env = append(cmd.Environ(), "GOWORK="+opts.workfile)
			if plan.moduleWorkspace {
				cmd.Env = append(cmd.Env, "GOFLAGS="+plan.workspaceGoFlags)
			}
		}
		return readPackages(ctx, cmd, "resolve packages")
	}
	list := func(patterns ...string) ([]goPackage, error) {
		return listWith("Dir,Name,ImportPath,Standard,DepOnly,GoFiles,TestGoFiles,XTestGoFiles,Imports,TestImports,XTestImports,Module,Error", patterns...)
	}
	if runtime == Mini && opts.mod != "mod" && plan.Workfile == "" {
		plan.Modfile, e = preprovideMini(ctx, dir, opts, temp, replacements, progress)
		if e != nil {
			return plan, e
		}
		if plan.Modfile != "" {
			opts.buildFlags = append(opts.buildFlags, "-modfile="+plan.Modfile)
		}
	}
	patterns := append([]string{"-deps"}, opts.packages...)
	patterns = append(patterns, "testing", runtimePackage)
	var listed []goPackage
	if opts.mod == "mod" && !plan.moduleWorkspace {
		// Resolve only native client imports, including test-only imports,
		// before probing our injected runtime. Preserve native module updates.
		_, e = list(append([]string{"-test", "-json=ImportPath,Error"}, opts.packages...)...)
		if e == nil {
			listed, e = list(append([]string{"-mod=readonly"}, patterns...)...)
		}
	} else {
		listed, e = list(patterns...)
	}
	if e != nil {
		return plan, e
	}
	for _, p := range listed {
		if p.ImportPath == runtimePackage && !p.DepOnly && p.Error != nil && plan.Modfile == "" {
			// The module does not require the runtime: provide it through a
			// temporary go.mod instead of failing or editing the module.
			if plan.Modfile, e = provideRuntime(ctx, dir, opts, runtime, temp, replacements, progress); e != nil {
				return plan, fmt.Errorf("%s: %s\nddto could not provide it: %w", p.ImportPath, p.Error.Err, e)
			}
			opts.buildFlags = append(opts.buildFlags, "-modfile="+plan.Modfile)
			if listed, e = list(patterns...); e != nil {
				return plan, e
			}
			break
		}
	}
	graph := newPackageGraph(listed)
	var packages []goPackage
	dependencies := 0
	for _, p := range listed {
		if p.DepOnly {
			// Dependency errors are Go's to report while building.
			dependencies++
			continue
		}
		if p.Error != nil {
			if p.ImportPath == "testing" || p.ImportPath == runtimePackage {
				return plan, fmt.Errorf("%s: %s", p.ImportPath, p.Error.Err)
			}
			// Preserve the original package arguments. Go reports setup failures
			// and executes the remaining packages; invalid packages receive no hook.
			continue
		}
		packages = append(packages, p)
	}
	debug.printf("package graph named=%d dependencies=%d", len(listed)-dependencies, dependencies)
	var native, selectedRuntime *goPackage
	for i := range packages {
		p := &packages[i]
		if p.ImportPath == "testing" {
			native = p
		}
		if p.ImportPath == runtimePackage {
			selectedRuntime = p
			if runtime == SDK && (p.Module == nil || p.Module.Version != SDKVersion || p.Module.Replace != nil) {
				return plan, fmt.Errorf("POC requires unmodified dd-trace-go %s", SDKVersion)
			}
			if debug != nil && p.Module != nil {
				debug.printf("runtime module_version=%q replacement=%t", p.Module.Version, p.Module.Replace != nil)
			}
		}
	}
	if selectedRuntime == nil || native == nil {
		return plan, fmt.Errorf("missing testing or selected CI runtime package")
	}
	if e := checkTestingGOROOT(native.Dir, opts.environment, plan.gorootMirror); e != nil {
		return plan, e
	}
	timeoutRefusal := "the SDK runtime"
	if plan.mini {
		timeoutRefusal = timeoutHookRefusal(listed, replacements)
	}
	rewritten, e := transformTesting(native, replacements, timeoutRefusal == "", debug)
	if e != nil {
		return plan, e
	}
	if timeoutRefusal == "" && !rewritten.Timeout {
		timeoutRefusal = "testing's -timeout alarm does not panic exactly once"
	}
	debug.printf("timeout hook installed=%t reason=%q", rewritten.Timeout, timeoutRefusal)
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
	libraries, sdkCI, testsUseMini, testPlugin, e := resolveTestLibraries(ctx, dir, opts, packages, graph)
	if e != nil {
		return plan, e
	}
	// Orchestrion may introduce SDK imports during compilation. Other builds
	// need the gate only when the already resolved client graph contains it.
	plan.sdkCI = plan.mini && (plan.orchestrion || sdkCI)
	hooks := hooksForRuntime(runtime, rewritten.ParallelStop, rewritten.Timeout)
	if plan.sdkCI {
		if warning := preflightSDKMirror(libraries[sdkTracerPackage], replacements); warning != "" {
			plan.sdkMirrorDisabled = true
			plan.Warnings = append(plan.Warnings, warning)
		}
		for _, path := range []string{sdkCIConfigPackage, sdkCIEnvironmentPackage, sdkTracerPackage} {
			if path == sdkTracerPackage && plan.sdkMirrorDisabled {
				continue
			}
			pkg := libraries[path]
			if pkg == nil {
				// Orchestrion can introduce these packages after preparation.
				pkg = &goPackage{ImportPath: path}
			}
			flag, err := packageCompilerCacheFlag(dir, opts, pkg, sdkCompilerCacheMarker(path))
			if err != nil {
				return plan, err
			}
			plan.compilerCache = append(plan.compilerCache, flag)
		}
	}
	if e = add(filepath.Join(native.Dir, "zz_dd_ci_visibility_hooks.go"), hooks); e != nil {
		return plan, e
	}
	if plan.mini && opts.coverage {
		goroots := []string{opts.environment.GOROOT}
		if plan.gorootMirror != nil {
			goroots = []string{plan.gorootMirror.root, opts.environment.GOROOT}
		}
		refusal := coverageHookRefusal(dir, opts, runtimePackage, goroots, listed, testPlugin, replacements)
		debug.printf("per-test coverage in_memory_hook=%t reason=%q", refusal == "", refusal)
		if refusal == "" {
			if e = add(filepath.Join(native.Dir, "zz_dd_ci_visibility_coverage.go"), instrument.CoverageHook); e != nil {
				return plan, e
			}
		}
	}
	// Go keys cached test results on its own environment, which holds the
	// temporary workspace, while tests see the caller's values. Keying the test
	// binaries on those values keeps a cached result from outliving them.
	if len(plan.savedEnvironment()) != 0 && !opts.execSet && !opts.compileOnly && !opts.printOnly {
		if plan.execHelper, e = crossExecHelper(opts.environment, dir); e != nil {
			return plan, &nativeGoError{"exec_helper=relative", e}
		}
	}
	// Go caches by binary content, so the fingerprint must reach the binary.
	callerEnvironment := ""
	if saved := plan.savedEnvironment(); saved != nil {
		callerEnvironment = fmt.Sprintf("func init() { __dd_ci_runtime.RegisterTestEnvironment(\"%x\") }\n", sha256.Sum256([]byte(strings.Join(saved, "\x00"))))
	}
	// The runtime import joins the package's own tests when it has no external
	// test package: a new one would be another package to compile and vet.
	// Packages that the runtime imports keep it in an external test package,
	// the only test package that may import them back.
	runtimeImports := graph.imported(selectedRuntime)
	internalImports := 0
	for _, p := range packages {
		if p.ImportPath == "testing" || p.ImportPath == runtimePackage || len(p.TestGoFiles)+len(p.XTestGoFiles) == 0 {
			continue
		}
		if p.Module == nil {
			return plan, fmt.Errorf("stdlib tests are outside this POC: %s", p.ImportPath)
		}
		testPackage := p.Name + "_test"
		if len(p.XTestGoFiles) == 0 && !runtimeImports[p.ImportPath] {
			testPackage = p.Name
			internalImports++
		}
		content := "package " + testPackage + "\nimport _ " + fmt.Sprintf("%q", runtimePackage) + "\n"
		if runtime == Mini {
			// The caller's compiler path identifies this package without baking
			// its directory into the source. Identical packages still share files.
			content = "package " + testPackage + "\nimport __dd_ci_runtime " + fmt.Sprintf("%q", runtimePackage) + "\nfunc init() { __dd_ci_runtime.RegisterTestPackage() }\n" + callerEnvironment
		}
		if e = add(filepath.Join(p.Dir, "zz_dd_ci_visibility_test.go"), content); e != nil {
			return plan, e
		}
		plan.TestPackages++
		plan.tests.Packages = append(plan.tests.Packages, utils.CLITestPackage{Dir: p.Dir, ModulePath: p.Module.Path, ModuleDir: p.Module.Dir})
	}
	debug.printf("runtime imports internal_test_packages=%d external_test_packages=%d", internalImports, plan.TestPackages-internalImports)
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
			var flag string
			if flag, e = goleakCacheFlag(dir, opts, libraries[instrument.GoleakImport], goleak.Fingerprint); e != nil {
				return plan, e
			}
			plan.compilerCache = append(plan.compilerCache, flag)
		}
	}
	// Tests that import Mini through a helper outside the package graph keep
	// it instrumented, as noraceOverlay does for the packages in the graph.
	if plan.mini && opts.race && !testsUseMini {
		racePhase := debug.start("race annotations")
		files := noraceOverlay(listed, replacements, temp)
		racePhase.finish(nil)
		debug.printf("race annotations files=%d", files)
	}
	rewrittenPackages := []goPackage{*native}
	if plan.mini {
		signal, refusal, e := prepareSignalHook(listed, replacements, temp, add)
		if e != nil {
			return plan, e
		}
		debug.printf("signal hook installed=%t reason=%q", signal != nil, refusal)
		if signal != nil {
			rewrittenPackages = append(rewrittenPackages, *signal)
		}
		if refusal, e = prepareExitHook(listed, replacements, native.Dir, add); e != nil {
			return plan, e
		}
		debug.printf("exit hook installed=%t reason=%q", refusal == "", refusal)
		refusal = "--panic-hook not set"
		if opts.panicHook {
			if refusal, e = preparePanicHook(listed, replacements, temp, add); e != nil {
				return plan, e
			}
		}
		debug.printf("panic hook installed=%t reason=%q", refusal == "", refusal)
		plan.sessionRefusal = "the runtime's civisibility/utils package not listed"
		if utils := findPackage(listed, miniUtilsPackage); utils != nil {
			plan.sessionRefusal = checkSources(utils, replacements, instrument.SessionRuntimeError)
		}
		if opts.panicHook && refusal != "" {
			plan.Warnings = append(plan.Warnings, "--panic-hook: "+refusal+"; an unrecovered panic still loses the test process's events")
		}
	}
	if opts.fileCoverage {
		phase := debug.start("file coverage")
		refusal := fileCoverageRefusal(&plan, opts, vendorProvided, listed, replacements)
		var summary fileCoverageSummary
		if refusal == "" {
			// The build with tests also links what only tests import.
			patterns := append([]string{"-deps", "-test"}, opts.packages...)
			if opts.mod == "mod" && !plan.moduleWorkspace {
				patterns = append([]string{"-mod=readonly"}, patterns...)
			}
			tested, e := listWith(fileCoverageListFields, patterns...)
			if e != nil {
				refusal = "the package query with tests failed: " + strings.SplitN(e.Error(), "\n", 2)[0]
			} else {
				excluded := []string{opts.environment.GOROOT, opts.environment.GOMODCACHE}
				if plan.gorootMirror != nil {
					excluded = append(excluded, plan.gorootMirror.root)
				}
				summary, refusal, e = prepareFileCoverage(tested, repositoryRoots(dir, listed), excluded, replacements, userOverlay, temp, add)
				if e != nil {
					phase.finish(e)
					return plan, e
				}
			}
		}
		phase.finish(nil)
		plan.fileCoverage = refusal == ""
		plan.fileCoverageParallel = plan.fileCoverage && opts.fileCoverageParallel
		if plan.fileCoverageParallel {
			// A selected runtime from before =parallel runs the tests one
			// at a time; say so instead of leaving it unnoticed.
			if parallelRefusal := checkSources(findPackage(listed, miniCoveragePackage), replacements, instrument.FileCoverageParallelRuntimeError); parallelRefusal != "" {
				plan.fileCoverageParallel = false
				plan.Warnings = append(plan.Warnings, "--experimental-file-coverage=parallel: "+parallelRefusal+"; tests run one at a time while coverage is collected")
			}
		}
		debug.printf("file coverage installed=%t reason=%q packages=%d marked_files=%d always_files=%d parallel=%t", refusal == "", refusal, summary.packages, summary.marked, summary.always, plan.fileCoverageParallel)
		if refusal != "" {
			plan.Warnings = append(plan.Warnings, "--experimental-file-coverage: "+refusal+"; per-test coverage keeps Go's coverage counters")
		}
	}
	plan.coverOverlay = needsCoverOverlay(dir, opts, packages, rewrittenPackages)
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
	debug.printf("plan ready testing_files=%d test_packages=%d overlay_entries=%d generated_backing_files=%d temporary_modfile=%t cover_bridge=%t sdk_ci_gate=%t", plan.InstrumentedFiles, plan.TestPackages, len(replacements), len(backingByContent), plan.Modfile != "", plan.coverOverlay, plan.sdkCI)
	return plan, nil
}

func transformTesting(native *goPackage, replacements map[string]string, timeout bool, debug *cliDebug) (result instrument.TestingSources, err error) {
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
	// Both Mini and the pinned SDK support fuzz seeds through this hook.
	if timeout {
		result, err = instrument.TransformWithTimeout(files)
	} else {
		result, err = instrument.TransformWithFuzz(files)
	}
	if err != nil {
		return result, err
	}
	debug.printf("testing sources=%d rewritten=%d fuzz=true parallel_stop=%t", len(files), len(result.Files), result.ParallelStop)
	return result, nil
}

// Run retains native test output, flags, working directory, and exit status.
// It deliberately preserves the user's test-result caching choice.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cli := DefaultCLIOptions()
	cli.Runtime = SDK
	return RunRuntime(ctx, args, cli, stdin, stdout, stderr)
}

// RunRuntime compiles and executes tests using one explicitly selected runtime.
// Interrupt and termination signals are forwarded to go test instead of ending
// ddto first: the plan is removed only after Go exits, and the result keeps
// go test's exit status. A signal during preparation stops it and cleans up.
func RunRuntime(ctx context.Context, args []string, cli CLIOptions, stdin io.Reader, stdout, stderr io.Writer) (exitCode int) {
	begin := time.Now()
	runtime := cli.Runtime
	ctx = withCLIDebug(ctx, stderr)
	debug := debugFromContext(ctx)
	if debug != nil {
		stderr = debug.writer
	}
	started := debug.start("ddto")
	defer func() {
		var err error
		if exitCode != 0 {
			err = errors.New("command failed")
		}
		started.finish(err)
		debug.printf("ddto exit_code=%d", exitCode)
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
	opts.panicHook = cli.PanicHook
	opts.fileCoverage, opts.fileCoverageParallel = cli.FileCoverage, cli.FileCoverageParallel
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
	gitHubJobID := handGitHubJobID(runtime, debug)
	gitWorker := startGitWorker(cli, opts, dir, debug, stderr)
	defer gitWorker.finish()
	if gitWorker == nil && cli.KeepCIState {
		fmt.Fprintln(stderr, version.BuildLogPrefix+" INFO: --keep-ci-state: this command starts no Git worker, so it has no CI state")
	}
	plan, interrupted, err := prepareInterruptibly(ctx, dir, opts, runtime, signals, stderr)
	if err == nil {
		defer func() {
			plan.Release()
			_ = os.RemoveAll(plan.Dir)
		}()
		err = wrapExecProgram(&plan, opts, dir)
	}
	if interrupted != nil {
		debug.printf("preparation interrupted")
		return interruptedStatus(interrupted)
	}
	var native *nativeGoError
	if errors.As(err, &native) && fallsBackToNativeGo(ctx, err) {
		// The toolchain is supported; only ddto's instrumentation is not.
		fmt.Fprintln(stderr, version.BuildLogPrefix+" WARN:", err, "- running go test without CI Visibility")
		debug.printf("instrumentation bypass %s", native.bypass)
		return runGo(ctx, cwd, append([]string{"test"}, args...), nil, signals, stdin, stdout, stderr)
	}
	if err != nil {
		fmt.Fprintln(stderr, version.BuildLogPrefix+" ERROR:", err)
		return 2
	}
	ctx = withGoToolchain(ctx, plan.gorootMirror)
	for _, warning := range plan.Warnings {
		fmt.Fprintln(stderr, version.BuildLogPrefix+" WARN:", warning)
	}
	if gitWorker != nil {
		// The worker requests, and reports its telemetry, as the test
		// processes, whose version includes their experiments.
		plan.goVersion = testRuntimeVersion(ctx, dir, plan.goVersion, plan.goExperiment)
	}
	handoff := gitWorker.handOff(plan.tests, plan.testLauncher, plan.goVersion)
	// When go test may take every result from its cache, the worker requests
	// for the test processes once go test links a test binary, which it does
	// only for a result that is not in its cache, unless -o or -a makes it
	// link anyway: then the worker waits for a test process.
	plan.linkSignal = len(handoff) != 0 && !plan.tests.RunsEveryTest && !opts.linksCachedResults()
	switch {
	case len(handoff) == 0:
	case plan.tests.RunsEveryTest:
		debug.printf("git worker requests_ahead=while_building reason=%q", opts.cacheRule)
	case opts.linksCachedResults():
		debug.printf("git worker requests_ahead=at_first_test reason=%q", opts.linkRule()+" links test binaries even for cached results")
	default:
		debug.printf("git worker requests_ahead=at_link reason=%q", "go test may take results from its cache")
	}
	var tool string
	var env []string
	if plan.coverOverlay || plan.testify || plan.goleak || plan.orchestrion || plan.sdkCI || plan.linkSignal {
		executable, e := os.Executable()
		if e != nil {
			fmt.Fprintln(stderr, e)
			return 2
		}
		// Building the wrapper, or waiting for another run to, is preparation:
		// a signal stops it before go test starts.
		var wrapper string
		var lock *os.File
		interrupted := interruptibly(ctx, signals, func(ctx context.Context) {
			wrapper, lock, e = ensureToolWrapper(ctx, plan.environment, dir, debug)
		})
		if interrupted != nil {
			if lock != nil {
				lock.Close()
			}
			debug.printf("preparation interrupted")
			return interruptedStatus(interrupted)
		}
		if e != nil {
			debug.printf("tool wrapper state=unavailable error=%q", e.Error())
		}
		if tool, e = selectToolCommand(&plan, executable, wrapper, lock, debug); e != nil {
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
	debug.printf("tool selection testify=%t goleak=%t cover=%t user_toolexec=%t orchestrion=%t sdk_ci_gate=%t link_signal=%t", plan.testify, plan.goleak, plan.coverOverlay, opts.toolexec != "", plan.orchestrion, plan.sdkCI, plan.linkSignal)
	forwarded := goTestArguments(plan, opts, tool)
	// The test runtime's goenv/restore package gives test processes the
	// caller's values before any client package can start a go command.
	env = append(env, plan.savedEnvironment()...)
	if plan.Workfile != "" {
		// The temporary workspace belongs to the build, including chained tools.
		env = append(env, "GOWORK="+plan.Workfile)
		if plan.moduleWorkspace {
			env = append(env, "GOFLAGS="+plan.workspaceGoFlags)
		}
	}
	// A launcher would see the handed value before the test binary removes
	// it; with one, each test process scans for itself.
	if handoff := gitHubJobID(); handoff != nil && !plan.testLauncher {
		env = append(env, handoff...)
		debug.printf("github job id handed found=%t", !strings.HasSuffix(handoff[0], "="))
	}
	env = append(env, handoff...)
	if plan.fileCoverage {
		env = append(env, "DD_CIVISIBILITY_INTERNAL_FILE_COVERAGE_PARALLEL="+strconv.FormatBool(plan.fileCoverageParallel))
	}
	if plan.launcher != nil {
		ctx = context.WithValue(ctx, goLauncherKey{}, *plan.launcher)
	}
	session, refusal := startTestSession(cli, opts, plan, dir, begin, stderr)
	debug.printf("test session owned=%t reason=%q", session != nil, refusal)
	if session != nil {
		debug.printf("test session id=%d dir=%s", session.session.ID, session.dir)
		env = append(env, session.handoff())
		if gitWorker == nil {
			// The session reports the test processes' runtime version.
			plan.goVersion = testRuntimeVersion(ctx, dir, plan.goVersion, plan.goExperiment)
		}
	}
	exitCode = runGo(ctx, dir, forwarded, env, gitWorker.relaySignals(signals), stdin, stdout, stderr)
	var coverage *float64
	if session != nil {
		if path := coverProfilePath(opts, dir); path != "" {
			if total, ok := profileCoverage(path); ok {
				coverage = &total
			}
			debug.printf("test session coverage profile=%q found=%t", path, coverage != nil)
		}
	}
	gitState := ""
	if gitWorker != nil {
		gitState = gitWorker.state
	}
	session.finish(ctx, exitCode, plan.goVersion, coverage, gitState)
	return exitCode
}

// nativeGoError is a preparation failure after which ddto runs go test
// natively, because it cannot reproduce what go test would do. bypass names
// the reason in debug logs.
type nativeGoError struct {
	bypass string
	err    error
}

func (e *nativeGoError) Error() string { return e.err.Error() }

func (e *nativeGoError) Unwrap() error { return e.err }

// fallsBackToNativeGo reports a failure after which go test runs natively.
// Cancellation stops instead.
func fallsBackToNativeGo(ctx context.Context, err error) bool {
	var native *nativeGoError
	return errors.As(err, &native) && ctx.Err() == nil &&
		!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

// wrapExecProgram sets the wrapper that replaces the plan's -exec program. A
// -exec program, or Go's cross-compilation helper, starts before the test
// binary can restore the caller's Go settings. Go already disables result
// caching for those runs, so ddto's wrapper restores them first. A launcher
// such as Orchestrion runs go with a mirror's bin first in PATH, where go
// would look the program up, so the wrapper also starts the caller's program
// by its path.
func wrapExecProgram(plan *Plan, opts options, dir string) error {
	restore := len(plan.savedEnvironment()) != 0
	pin := plan.gorootMirror != nil && plan.launcher != nil
	if !restore && !pin || opts.exec == "" && plan.execHelper == "" {
		return nil
	}
	// Only runs that start test binaries start the program.
	runs := !opts.compileOnly && !opts.printOnly
	if !restore && !runs {
		return nil
	}
	path, words := plan.execHelper, []string{plan.execHelper}
	if opts.exec != "" {
		var err error
		if words, err = splitFlags(opts.exec); err != nil {
			return fmt.Errorf("invalid -exec: %w", err)
		}
		// Without a program, go test starts test binaries itself.
		if len(words) == 0 {
			return nil
		}
		if runs {
			if path, err = resolveExecProgram(dir, words); err != nil {
				return &nativeGoError{"exec_program=unresolved", err}
			}
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if plan.execWrapper, err = quoteToolWords(append([]string{executable, "test-exec", path}, words...)); err != nil {
		return &nativeGoError{"exec_program=unquotable", err}
	}
	return nil
}

// errNoCommand is go test's error for an empty -exec program name.
var errNoCommand = errors.New("exec: no command")

// resolveExecProgram returns the path of the -exec program in words, looked up
// as go test does: on the caller's PATH from dir, go test's working directory.
// test-exec would look it up after restoring the toolchain's bin first in
// PATH, from the package directory. A relative path, which go test starts
// from the package directory, stays as given. It fails
// for a program that go test cannot run, or would run relative to dir: only go
// test itself reproduces those.
func resolveExecProgram(dir string, words []string) (string, error) {
	if words[0] == "" {
		return "", errNoCommand
	}
	path := words[0]
	// exec.Cmd finds a Windows path rooted on the current drive, or relative
	// to a drive's current directory, from go test's process, and starts it
	// from the package directory: only go test reproduces both. On Windows,
	// \ alone is also its own base name.
	if !filepath.IsAbs(path) && (filepath.VolumeName(path) != "" || os.IsPathSeparator(path[0])) {
		return "", &exec.Error{Name: path, Err: errUnknownProgram}
	}
	if filepath.Base(path) == path {
		// exec.LookPath takes a name with a slash, / alone, as a path that it
		// checks itself.
		if strings.ContainsRune(path, '/') {
			return "", &exec.Error{Name: path, Err: errUnknownProgram}
		}
		return lookPathFrom(dir, path)
	}
	return path, nil
}

// prepareInterruptibly cancels package resolution when a signal arrives. The
// watcher has stopped before returning, so later signals reach go test.
func prepareInterruptibly(ctx context.Context, dir string, opts options, runtime Runtime, signals <-chan os.Signal, progress io.Writer) (plan Plan, interrupted os.Signal, err error) {
	interrupted = interruptibly(ctx, signals, func(ctx context.Context) {
		plan, err = prepare(ctx, dir, opts, runtime, progress)
	})
	return plan, interrupted, err
}

// interruptibly runs fn with a context that the first signal from signals
// cancels, and returns that signal, or nil.
func interruptibly(ctx context.Context, signals <-chan os.Signal, fn func(context.Context)) os.Signal {
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
	fn(ctx)
	close(stop)
	<-stopped
	return interrupted
}

// goTestArguments keeps the user's arguments in order. Our overlay already
// contains the user's entries and our -toolexec chains the user's, so those
// flags are replaced wherever they appear before the test binary arguments.
// Go applies per-package flags in order: our package cache markers follow the
// last user -gcflags, retaining each package's effective compiler flags.
func goTestArguments(plan Plan, opts options, tool string) []string {
	forwarded := []string{"test", "-overlay=" + plan.File}
	if plan.Modfile != "" {
		forwarded = append(forwarded, "-modfile="+plan.Modfile)
	}
	if tool != "" {
		forwarded = append(forwarded, "-toolexec="+tool)
	}
	if plan.execWrapper != "" {
		forwarded = append(forwarded, "-exec="+plan.execWrapper)
	}
	lastGcflags := -1
	if len(plan.compilerCache) != 0 {
		for i, argument := range opts.arguments {
			if argument.flag == "gcflags" {
				lastGcflags = i
			}
		}
		if lastGcflags < 0 {
			forwarded = append(forwarded, plan.compilerCache...)
		}
	}
	for i, argument := range opts.arguments {
		if argument.flag == "overlay" || argument.flag == "toolexec" && tool != "" || argument.flag == "exec" && plan.execWrapper != "" || argument.flag == "modfile" && (plan.Modfile != "" || plan.modfileOverlay) {
			continue
		}
		if plan.moduleWorkspace && argument.flag == "mod" {
			forwarded = append(forwarded, workspaceModuleFlags(argument.raw)...)
			continue
		}
		forwarded = append(forwarded, argument.raw...)
		if i == lastGcflags {
			forwarded = append(forwarded, plan.compilerCache...)
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
	if toolchain, ok := ctx.Value(goToolchainKey{}).(goToolchain); ok {
		// A launcher finds go on PATH; go itself runs from the mirror. Test
		// processes then get the mirror's bin first in PATH from go test.
		mirrored := toolchain.env
		if command[0] == "go" {
			command = append([]string{toolchain.path}, command[1:]...)
			mirrored = mirrored[:1]
		}
		envOverrides = append(append([]string(nil), mirrored...), envOverrides...)
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
