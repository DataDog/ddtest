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
	// ddto go test is ddto test, as go test is.
	form, isTest := runner.TestCommand(os.Args[1:])
	switch {
	case isTest:
	case len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "-version"):
		fmt.Println("ddto " + version.Tag)
		os.Exit(0)
	case len(os.Args) == 1 || os.Args[1] == "-h" || os.Args[1] == "-help" || os.Args[1] == "--help":
		fmt.Fprintln(os.Stderr, "usage: ddto [go | orchestrion go | go tool orchestrion go] test [--runtime=mini|sdk] [--git-upload-timeout=1m] [--keep-ci-state[=file.zip]] [--panic-hook] [--session-per-binary] [go test flags] [packages] (default: mini)")
		fmt.Fprintln(os.Stderr, "       ddto command [arguments]: runs any other command unchanged, such as ddto go build ./...")
		fmt.Fprintln(os.Stderr, "       ddto --version: prints the version")
		if len(os.Args) == 1 {
			os.Exit(2)
		}
		os.Exit(0)
	default:
		// Any other command runs unchanged, before ddto sets anything: ddto
		// in front of a go command, or any other, is transparent.
		os.Exit(runner.ExecCommand(os.Args[1:]))
	}
	ctx := runner.DebugCommand(context.Background(), form.Name)
	cli, args, err := runner.ParseCLIArgs(form.Args)
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
