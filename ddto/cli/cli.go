// Package cli is ddto's command line, for a program that runs it as one of
// its commands, such as ddtest instrument.
package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/ddto/internal/runner"
	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/globalconfig"
	"github.com/DataDog/ddtest/ddto/internal/version"
)

// Handles reports whether args, a process's arguments, run ddto in a program
// whose ddto command is name: that command, which also starts the modes in
// which ddto runs its own executable again, or the go shim that ddto made of
// this executable. The program's other arguments stay its own.
func Handles(args []string, name string) bool {
	if len(args) == 0 {
		return false
	}
	if runner.RunsAsGoShim(args[0]) {
		return true
	}
	if len(args) < 2 {
		return false
	}
	return args[1] == name
}

// KeepCallerEnvironment restores the environment that this process received,
// for a program that links ddto and runs other commands: the SDK that ddto
// incorporates hands its root session to child processes when it starts.
func KeepCallerEnvironment() { globalconfig.KeepCallerEnvironment() }

// Main runs ddto with args, the process's arguments, and exits the process.
// In a program whose ddto command is name, Main drops that command; os.Args
// stays as the process received it, so a command that ddto runs unchanged
// keeps the caller's exact command line on Windows.
func Main(args []string, name string) {
	argv, prog := args, name
	if len(args) > 0 {
		prog = strings.TrimSuffix(filepath.Base(args[0]), ".exe") + " " + name
	}
	if len(args) > 1 && args[1] == name {
		argv = append([]string{args[0]}, args[2:]...)
	}
	// ddto's own modes run again as this command.
	runner.SelfCommand = []string{name}

	// The SDK's process identity, which ddto links for its Git worker, hands
	// its root session to child processes through the environment at
	// initialization. ddto passes its caller's environment unchanged.
	globalconfig.KeepCallerEnvironment()
	// go test -exec entrypoint: restore the caller's Go settings first. The
	// program's path comes before its arguments.
	if len(argv) >= 3 && argv[1] == "test-exec" {
		os.Exit(runner.ExecWithCallerEnvironment(argv[2], argv[3:]))
	}
	// go run's -exec program with the go shim, which go run puts after
	// $GOROOT/bin on the program's PATH.
	if len(argv) >= 3 && argv[1] == runner.GoShimExecMode {
		os.Exit(runner.ExecWithGoShim(argv[2:]))
	}
	// The Git worker that ddto test starts once per invocation.
	if len(argv) >= 3 && len(argv) <= 5 && argv[1] == "ci-git-worker" {
		os.Exit(runner.RunGitWorker(context.Background(), argv[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	if len(argv) >= 5 && argv[1] == "tool-overlay" {
		mode, plan, args := argv[2], argv[3], argv[4:]
		if !runner.ValidToolMode(mode) {
			fmt.Fprintln(os.Stderr, version.BuildLogPrefix+" ERROR: invalid tool mode")
			os.Exit(2)
		}
		runner.NoteTestLink(mode, args)
		if !runner.ToolNeedsPlan(mode, args, os.Getenv("TOOLEXEC_IMPORTPATH")) {
			os.Exit(runner.ExecNativeTool(args))
		}
		os.Exit(runner.RunTool(context.Background(), plan, args, os.Stdin, os.Stdout, os.Stderr))
	}
	// The go that ddto puts first on PATH for gotestsum is ddto: it runs as
	// ddto go.
	args, fromGoShim := runner.GoShimArgs(argv)
	// --go-shim, and gotestsum in any form, run the command with the go shim
	// first on its PATH, so the go test that it runs reports.
	args, goShimReason := runner.GoShimCommand(args, fromGoShim)
	goShim := goShimReason != ""
	// ddto go test is ddto test, as go test is.
	form, isTest := runner.TestCommand(args)
	if isTest || runner.IsGoCommand(args) {
		if err := runner.NestGoCommand(); err != nil {
			fmt.Fprintln(os.Stderr, version.BuildLogPrefix+" ERROR:", err)
			os.Exit(2)
		}
	}
	switch {
	case isTest:
	case goShim && len(args) == 0:
		fmt.Fprintln(os.Stderr, version.BuildLogPrefix+" ERROR: --go-shim needs a command")
		os.Exit(2)
	case !goShim && len(args) == 1 && (args[0] == "--version" || args[0] == "-version"):
		fmt.Println(prog + " " + version.Tag)
		os.Exit(0)
	case !goShim && (len(args) == 0 || args[0] == "-h" || args[0] == "-help" || args[0] == "--help"):
		fmt.Fprintln(os.Stderr, "usage: "+prog+" [go | orchestrion go | go tool orchestrion go] test [--runtime=mini|sdk] [--git-upload-timeout=1m] [--keep-ci-state[=file.zip]] [--panic-hook] [--session-per-binary] [--experimental-file-coverage] [go test flags] [packages] (default: mini)")
		fmt.Fprintln(os.Stderr, "       "+prog+" gotestsum [gotestsum flags] [-- go test flags]: reports the tests of gotestsum's go test, also with go tool gotestsum or go run gotest.tools/gotestsum")
		fmt.Fprintln(os.Stderr, "       "+prog+" --go-shim command [arguments]: reports the go test that command, or a command it starts, runs")
		fmt.Fprintln(os.Stderr, "       "+prog+" command [arguments]: runs any other command unchanged, such as "+prog+" go build ./...")
		fmt.Fprintln(os.Stderr, "       "+prog+" --version: prints the version")
		if len(args) == 0 {
			os.Exit(2)
		}
		os.Exit(0)
	default:
		if goShim {
			os.Exit(runner.RunWithGoShim(args, goShimReason))
		}
		// Any other command runs unchanged, before ddto sets anything but
		// DDTEST_INSTRUMENT_NESTED_GO for go: ddto in front of a go command, or any other,
		// is transparent.
		os.Exit(runner.ExecCommand(args))
	}
	ctx := runner.DebugCommand(context.Background(), form.Name)
	// gotestsum's go test arguments are go test's: ddto's options are ddto's.
	cli, args, err := runner.DefaultCLIOptions(), form.Args, error(nil)
	if !fromGoShim {
		cli, args, err = runner.ParseCLIArgs(form.Args)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, version.BuildLogPrefix+" ERROR:", err)
		os.Exit(2)
	}

	if _, defined := os.LookupEnv("DD_CIVISIBILITY_ENABLED"); !defined {
		if err := os.Setenv("DD_CIVISIBILITY_ENABLED", "parent"); err != nil {
			fmt.Fprintln(os.Stderr, version.BuildLogPrefix+" ERROR:", err)
			os.Exit(2)
		}
	}
	if form.Orchestrion {
		runner.Exit(runner.RunOrchestrion(ctx, args, cli, form.GoTool, os.Stdin, os.Stdout, os.Stderr))
		return
	}
	runner.Exit(runner.RunRuntime(ctx, args, cli, os.Stdin, os.Stdout, os.Stderr))
}
