package main

import (
	"context"
	"fmt"
	"os"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/runner"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/globalconfig"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

func main() {
	// The SDK's process identity, which ddto links for its Git worker, hands
	// its root session to child processes through the environment at
	// initialization. ddto passes its caller's environment unchanged.
	globalconfig.KeepCallerEnvironment()
	// go test -exec entrypoint: restore the caller's Go settings first. The
	// program's path comes before its arguments.
	if len(os.Args) >= 3 && os.Args[1] == "test-exec" {
		os.Exit(runner.ExecWithCallerEnvironment(os.Args[2], os.Args[3:]))
	}
	// go run's -exec program with the go shim, which go run puts after
	// $GOROOT/bin on the program's PATH.
	if len(os.Args) >= 3 && os.Args[1] == runner.GoShimExecMode {
		os.Exit(runner.ExecWithGoShim(os.Args[2:]))
	}
	// The Git worker that ddto test starts once per invocation.
	if len(os.Args) >= 3 && len(os.Args) <= 5 && os.Args[1] == "ci-git-worker" {
		os.Exit(runner.RunGitWorker(context.Background(), os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	if len(os.Args) >= 5 && os.Args[1] == "tool-overlay" {
		mode, plan, args := os.Args[2], os.Args[3], os.Args[4:]
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
	args, fromGoShim := runner.GoShimArgs(os.Args)
	// --go-shim, and gotestsum in any form, run the command with the go shim
	// first on its PATH, so the go test that it runs reports.
	args, goShim := runner.GoShimCommand(args, fromGoShim)
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
		fmt.Println("ddto " + version.Tag)
		os.Exit(0)
	case !goShim && (len(args) == 0 || args[0] == "-h" || args[0] == "-help" || args[0] == "--help"):
		fmt.Fprintln(os.Stderr, "usage: ddto [go | orchestrion go | go tool orchestrion go] test [--runtime=mini|sdk] [--git-upload-timeout=1m] [--keep-ci-state[=file.zip]] [--panic-hook] [--session-per-binary] [--experimental-file-coverage] [go test flags] [packages] (default: mini)")
		fmt.Fprintln(os.Stderr, "       ddto gotestsum [gotestsum flags] [-- go test flags]: reports the tests of gotestsum's go test, also with go tool gotestsum or go run gotest.tools/gotestsum")
		fmt.Fprintln(os.Stderr, "       ddto --go-shim command [arguments]: reports the go test that command, or a command it starts, runs")
		fmt.Fprintln(os.Stderr, "       ddto command [arguments]: runs any other command unchanged, such as ddto go build ./...")
		fmt.Fprintln(os.Stderr, "       ddto --version: prints the version")
		if len(args) == 0 {
			os.Exit(2)
		}
		os.Exit(0)
	default:
		if goShim {
			os.Exit(runner.RunWithGoShim(args))
		}
		// Any other command runs unchanged, before ddto sets anything but
		// DDTO_NESTED_GO for go: ddto in front of a go command, or any other,
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
