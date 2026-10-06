package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/runner"
)

func main() {
	if len(os.Args) >= 5 && os.Args[1] == "tool-overlay" {
		mode, plan, args := os.Args[2], os.Args[3], os.Args[4:]
		if mode != "testify" && mode != "cover" && mode != "testify-cover" && mode != "goleak" && mode != "goleak-cover" && mode != "testify-goleak" && mode != "testify-goleak-cover" {
			fmt.Fprintln(os.Stderr, "ddtest: invalid tool mode")
			os.Exit(2)
		}
		if !runner.ToolNeedsPlan(mode, args, os.Getenv("TOOLEXEC_IMPORTPATH")) {
			os.Exit(runner.ExecNativeTool(args))
		}
		os.Exit(runner.RunTool(context.Background(), plan, args, os.Stdin, os.Stdout, os.Stderr))
	}
	if len(os.Args) < 2 || os.Args[1] != "test" {
		fmt.Fprintln(os.Stderr, "usage: ddtest test [--runtime=sdk|mini] [go test flags] [packages]")
		os.Exit(2)
	}
	if _, defined := os.LookupEnv("DD_CIVISIBILITY_ENABLED"); !defined {
		if err := os.Setenv("DD_CIVISIBILITY_ENABLED", "parent"); err != nil {
			fmt.Fprintln(os.Stderr, "ddtest:", err)
			os.Exit(2)
		}
	}
	args := os.Args[2:]
	runtime := runner.SDK
	if len(args) > 0 && strings.HasPrefix(args[0], "--runtime=") {
		runtime = runner.Runtime(strings.TrimPrefix(args[0], "--runtime="))
		args = args[1:]
		if runtime != runner.SDK && runtime != runner.Mini {
			fmt.Fprintln(os.Stderr, "ddtest: runtime must be sdk or mini")
			os.Exit(2)
		}
	}
	runner.Exit(runner.RunRuntime(context.Background(), args, runtime, os.Stdin, os.Stdout, os.Stderr))
}
