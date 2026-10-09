package main

import (
	"context"
	"fmt"
	"os"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/runner"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

func main() {
	// go test -exec entrypoint: restore the caller's Go settings first.
	if len(os.Args) >= 2 && os.Args[1] == "test-exec" {
		os.Exit(runner.ExecWithCallerEnvironment(os.Args[2:]))
	}
	if len(os.Args) >= 5 && os.Args[1] == "tool-overlay" {
		mode, plan, args := os.Args[2], os.Args[3], os.Args[4:]
		if !runner.ValidToolMode(mode) {
			fmt.Fprintln(os.Stderr, version.BuildLogPrefix+" ERROR: invalid tool mode")
			os.Exit(2)
		}
		if !runner.ToolNeedsPlan(mode, args, os.Getenv("TOOLEXEC_IMPORTPATH")) {
			os.Exit(runner.ExecNativeTool(args))
		}
		os.Exit(runner.RunTool(context.Background(), plan, args, os.Stdin, os.Stdout, os.Stderr))
	}
	args := os.Args[1:]
	orchestrion, goTool := false, false
	switch {
	case len(args) > 0 && args[0] == "test":
		args = args[1:]
	case len(args) >= 3 && args[0] == "orchestrion" && args[1] == "go" && args[2] == "test":
		orchestrion = true
		args = args[3:]
	case len(args) >= 5 && args[0] == "go" && args[1] == "tool" && args[2] == "orchestrion" && args[3] == "go" && args[4] == "test":
		orchestrion = true
		goTool = true
		args = args[5:]
	default:
		fmt.Fprintln(os.Stderr, "usage: ddto [orchestrion go | go tool orchestrion go] test [--runtime=mini|sdk] [go test flags] [packages] (default: mini)")
		os.Exit(2)
	}
	runtime, args, err := runner.ParseRuntimeArgs(args)
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
	if orchestrion {
		runner.Exit(runner.RunOrchestrion(context.Background(), args, runtime, goTool, os.Stdin, os.Stdout, os.Stderr))
		return
	}
	runner.Exit(runner.RunRuntime(context.Background(), args, runtime, os.Stdin, os.Stdout, os.Stderr))
}
