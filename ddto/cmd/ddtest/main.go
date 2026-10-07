package main

import (
	"context"
	"fmt"
	"os"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/runner"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

func main() {
	if len(os.Args) >= 5 && os.Args[1] == "tool-overlay" {
		mode, plan, args := os.Args[2], os.Args[3], os.Args[4:]
		if mode != "testify" && mode != "cover" && mode != "testify-cover" && mode != "goleak" && mode != "goleak-cover" && mode != "testify-goleak" && mode != "testify-goleak-cover" {
			fmt.Fprintln(os.Stderr, version.BuildLogPrefix+" ERROR: invalid tool mode")
			os.Exit(2)
		}
		if !runner.ToolNeedsPlan(mode, args, os.Getenv("TOOLEXEC_IMPORTPATH")) {
			os.Exit(runner.ExecNativeTool(args))
		}
		os.Exit(runner.RunTool(context.Background(), plan, args, os.Stdin, os.Stdout, os.Stderr))
	}
	if len(os.Args) < 2 || os.Args[1] != "test" {
		fmt.Fprintln(os.Stderr, "usage: ddtest test [--runtime=mini|sdk] [go test flags] [packages] (default: mini)")
		os.Exit(2)
	}
	runtime, args, err := runner.ParseRuntimeArgs(os.Args[2:])
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
	runner.Exit(runner.RunRuntime(context.Background(), args, runtime, os.Stdin, os.Stdout, os.Stderr))
}
