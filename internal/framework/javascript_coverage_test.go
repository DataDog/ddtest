package framework

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/coverage"
)

func TestWorkerCoverageCommands(t *testing.T) {
	for _, name := range []string{"jest", "vitest", "mocha", "cucumber", "playwright", "cypress"} {
		t.Run(name, func(t *testing.T) {
			e := &mochaCommandExecutor{}
			directory := filepath.Join(t.TempDir(), "worker with spaces")
			env := map[string]string{coverage.WorkerDirectoryEnv: directory, coverage.NYCEnv: "/project/node_modules/nyc/index.js", "DDTEST_COVERAGE_CYPRESS_HOOK": "/support/hook with spaces.cjs", "NODE_OPTIONS": "--require custom-loader.cjs"}
			args := []string{"exec", name, "--", "test/a.spec.js"}
			if err := runJavaScriptTests(context.Background(), e, name, "pnpm", args, env); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(args, []string{"exec", name, "--", "test/a.spec.js"}) {
				t.Fatal("mutated caller arguments")
			}
			switch name {
			case "jest", "vitest":
				if e.capturedName != "pnpm" {
					t.Fatal(e.capturedName)
				}
				separator := slices.Index(e.capturedArgs, "--")
				if separator < 0 || e.capturedArgs[len(e.capturedArgs)-1] != "test/a.spec.js" {
					t.Fatal(e.capturedArgs)
				}
				found := false
				for _, arg := range e.capturedArgs[:separator] {
					if strings.Contains(arg, filepath.Join(directory, "report")) {
						found = true
					}
				}
				if !found {
					t.Fatal("coverage directory option missing before --", e.capturedArgs)
				}
			case "cypress":
				if !slices.Equal(e.capturedArgs, args) {
					t.Fatal(e.capturedArgs)
				}
				if !strings.Contains(e.capturedEnv["NODE_OPTIONS"], `--require "/support/hook with spaces.cjs"`) || !strings.HasSuffix(e.capturedEnv["NODE_OPTIONS"], "--require custom-loader.cjs") {
					t.Fatal(e.capturedEnv)
				}
			default:
				if e.capturedName != "node" || e.capturedArgs[0] != "/project/node_modules/nyc/bin/nyc.js" {
					t.Fatal(e.capturedName, e.capturedArgs)
				}
				if !slices.Contains(e.capturedArgs, "--temp-dir="+filepath.Join(directory, "raw")) {
					t.Fatal(e.capturedArgs)
				}
				if !slices.Equal(e.capturedArgs[len(e.capturedArgs)-len(args):], args) {
					t.Fatal(e.capturedArgs)
				}
			}
		})
	}
}

func TestWorkerCoverageDisabledAndFailure(t *testing.T) {
	want := errors.New("worker failed")
	e := &mochaCommandExecutor{runErr: want}
	args := []string{"--coverageDirectory=custom", "a.test.js"}
	if err := runJavaScriptTests(context.Background(), e, "jest", "jest", args, nil); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if !slices.Equal(e.capturedArgs, args) {
		t.Fatal(e.capturedArgs)
	}
	env := map[string]string{coverage.WorkerDirectoryEnv: t.TempDir()}
	if err := runJavaScriptTests(context.Background(), e, "jest", "jest", args, env); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if slices.Contains(e.capturedArgs, "--coverageDirectory=custom") {
		t.Fatal(e.capturedArgs)
	}
}

func TestCoverageOptionsPreservePositionalFiles(t *testing.T) {
	got := replaceCoverageOptions("jest", []string{"--coverage=false", "--coverageDirectory", "old", "--coverageReporters=json", "--", "--coverageDirectory"}, "jest", []string{"--coverage"}, []string{"--coverageDirectory", "--coverageReporters"})
	if !slices.Equal(got, []string{"--", "--coverageDirectory"}) {
		t.Fatal(got)
	}
}

func TestCoverageOptionsSkipWrapperSeparator(t *testing.T) {
	args := []string{"--", "jest", "--coverageDirectory=old", "--coverage", "false", "--", "a.test.js"}
	got := replaceCoverageOptions("npx", args, "jest", []string{"--coverage"}, []string{"--coverageDirectory"})
	want := []string{"--", "jest", "--", "a.test.js"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
