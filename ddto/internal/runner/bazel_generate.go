package runner

import (
	"archive/zip"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/DataDog/ddtest/ddto/internal/bazelgen"
	"github.com/DataDog/ddtest/ddto/internal/bundle"
	"github.com/DataDog/ddtest/ddto/internal/version"
)

// bazelCommands are Bazel's commands: ddtest instrument bazel writes files
// for Bazel and runs none of them, as ddtest instrument bazel test //... did
// before the command existed.
var bazelCommands = map[string]bool{
	"aquery": true, "build": true, "canonicalize-flags": true, "clean": true, "coverage": true, "cquery": true,
	"dump": true, "fetch": true, "help": true, "info": true, "license": true, "mobile-install": true, "mod": true,
	"print_action": true, "query": true, "run": true, "shutdown": true, "sync": true, "test": true, "vendor": true, "version": true,
}

const bazelUsage = `usage: %[1]s bazel --mini [--rules-go=NAME] DIR
       %[1]s bazel --goroot=SDK --compiler=EXECUTABLE [--panic-hook] DIR

Writes what Bazel needs to instrument Go tests with this release:
  --mini      Mini's packages as a Bazel repository, with BUILD.bazel files
              that load rules_go as @NAME (default io_bazel_rules_go).
  --goroot    a copy of the Go SDK at SDK whose compiler is EXECUTABLE, this
              release's executable for the SDK's platform; --panic-hook edits
              the runtime as ddtest instrument's --panic-hook does.
It runs no Bazel command: rules_test_optimization runs it while it fetches.
`

// RunBazel runs ddtest instrument bazel with its arguments, after the word
// bazel, and returns the exit status. prog names the command in messages.
func RunBazel(prog string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(prog+" bazel", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	mini := flags.Bool("mini", false, "")
	rulesGo := flags.String("rules-go", "io_bazel_rules_go", "")
	goroot := flags.String("goroot", "", "")
	compiler := flags.String("compiler", "", "")
	panicHook := flags.Bool("panic-hook", false, "")
	usage := func(message string) int {
		if message != "" {
			fmt.Fprintln(stderr, version.BuildLogPrefix+" ERROR:", message)
		}
		fmt.Fprintf(stderr, bazelUsage, prog)
		return 2
	}
	if len(args) > 0 && bazelCommands[args[0]] {
		return usage(fmt.Sprintf("%s bazel writes the files that Bazel needs and runs no Bazel command; run bazel %s directly", prog, args[0]))
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(stdout, bazelUsage, prog)
			return 0
		}
		return usage(err.Error())
	}
	if flags.NArg() != 1 || flags.Arg(0) == "" {
		return usage("give exactly one output directory")
	}
	dir := flags.Arg(0)
	if entries, err := os.ReadDir(dir); err == nil && len(entries) != 0 {
		return usage(dir + " is not empty")
	}
	var err error
	switch {
	case *mini && *goroot == "" && *compiler == "" && !*panicHook:
		var src fs.FS
		if src, err = miniSources(); err == nil {
			err = bazelgen.WriteMini(dir, src, bazelgen.MiniOptions{RulesGo: *rulesGo, Version: version.Tag})
		}
	case !*mini && *goroot != "" && *compiler != "":
		err = bazelgen.WriteSDK(dir, bazelgen.SDKOptions{GOROOT: *goroot, Compiler: *compiler, PanicHook: *panicHook, Version: version.Tag})
	default:
		return usage("give either --mini, or --goroot and --compiler")
	}
	if err != nil {
		fmt.Fprintln(stderr, version.BuildLogPrefix+" ERROR:", err)
		return 1
	}
	return 0
}

// miniSources returns Mini's module: the sources that a release carries, or
// those that built this executable, from its checkout or GOMODCACHE.
func miniSources() (fs.FS, error) {
	if data := bundle.Runtime(); data != nil {
		return zip.NewReader(bytes.NewReader(data), int64(len(data)))
	}
	// No go command runs here: Bazel's repository rules may have none.
	_, source, _, _ := runtime.Caller(0)
	cache := os.Getenv("GOMODCACHE")
	if cache == "" {
		if home, err := os.UserHomeDir(); err == nil {
			cache = filepath.Join(home, "go", "pkg", "mod")
		}
	}
	if root := miniSourceRoot(source, cache, cliModuleVersion()); root != "" {
		return os.DirFS(root), nil
	}
	return nil, errors.New("this executable carries no runtime sources and finds none of its own")
}
