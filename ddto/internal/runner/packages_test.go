package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPackageListHelperProcess(t *testing.T) {
	if os.Getenv("DDTEST_PACKAGE_LIST_HELPER") != "1" {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "success":
		for _, part := range []string{`{"Import`, `Path":"example.com/one","Deps":["fmt"]}`, "\n", `{"ImportPath":"example.com/two"}`} {
			fmt.Fprint(os.Stdout, part)
		}
	case "partial":
		fmt.Fprint(os.Stdout, `{"ImportPath":"example.com/partial"}`)
		fmt.Fprint(os.Stderr, "package resolution failed")
		os.Exit(17)
	case "invalid", "invalid-exit":
		fmt.Fprint(os.Stdout, "invalid JSON\n")
		// More than a pipe buffer: readPackages must drain after Decode fails.
		io.Copy(os.Stdout, strings.NewReader(strings.Repeat("x", 2<<20)))
		if os.Args[len(os.Args)-1] == "invalid-exit" {
			fmt.Fprint(os.Stderr, "package resolution failed")
			os.Exit(17)
		}
	case "cancel":
		time.Sleep(10 * time.Second)
	}
	os.Exit(0)
}

func TestReadPackages(t *testing.T) {
	for _, mode := range []string{"success", "partial", "invalid", "invalid-exit", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			timeout := 5 * time.Second
			if mode == "cancel" {
				timeout = 50 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPackageListHelperProcess$", "--", mode)
			cmd.Env = append(os.Environ(), "DDTEST_PACKAGE_LIST_HELPER=1")
			packages, err := readPackages(cmd, "resolve fixture")
			if mode == "success" {
				if err != nil || len(packages) != 2 || packages[0].ImportPath != "example.com/one" || packages[1].ImportPath != "example.com/two" || !slices.Equal(packages[0].Deps, []string{"fmt"}) {
					t.Fatalf("decoded packages = %+v, error = %v", packages, err)
				}
				return
			}
			if err == nil || packages != nil {
				t.Fatalf("accepted failed/invalid output: %+v, %v", packages, err)
			}
			if mode == "partial" || mode == "invalid-exit" {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 17 || !strings.Contains(err.Error(), "resolve fixture:") || !strings.Contains(err.Error(), "package resolution failed") {
					t.Fatalf("lost command failure or stderr: %v", err)
				}
			}
			if mode == "cancel" && ctx.Err() != context.DeadlineExceeded {
				t.Fatalf("command was not cancelled: %v", err)
			}
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() && mode != "cancel" {
				t.Fatal("command was not waited for")
			}
		})
	}
	cmd := exec.Command(t.TempDir()+"/missing-go", "list")
	if packages, err := readPackages(cmd, "resolve fixture"); err == nil || packages != nil {
		t.Fatalf("missing executable = %+v, %v", packages, err)
	}
}

func TestTestifyDependencyImports(t *testing.T) {
	for _, tc := range []struct {
		name     string
		packages []goPackage
		want     []string
		suite    bool
	}{
		{"known production closure", []goPackage{{ImportPath: "testing", Deps: []string{"fmt"}}, {ImportPath: "example.com/client", Deps: []string{"example.com/helper", "fmt"}, TestImports: []string{"testing", "fmt", "example.com/helper", "example.com/client"}}}, nil, false},
		{"unknown external helper", []goPackage{{ImportPath: "example.com/client", TestImports: []string{"example.com/testkit"}, XTestImports: []string{"example.com/testkit"}}}, []string{"example.com/testkit"}, false},
		{"test-only standard import", []goPackage{{ImportPath: "testing", Deps: []string{"fmt"}}, {ImportPath: "example.com/client", TestImports: []string{"net/http/httptest"}}}, []string{"net/http/httptest"}, false},
		{"suite before pruning", []goPackage{{ImportPath: "example.com/client", Deps: []string{"github.com/stretchr/testify/suite"}, XTestImports: []string{"github.com/stretchr/testify/suite"}}}, nil, true},
		{"direct suite", []goPackage{{ImportPath: "example.com/client", TestImports: []string{"github.com/stretchr/testify/suite"}}}, []string{"github.com/stretchr/testify/suite"}, true},
		{"runtime closure is unrelated", []goPackage{{ImportPath: miniPackage, Deps: []string{"example.com/testkit", "github.com/stretchr/testify/suite"}}, {ImportPath: sdkPackage, Deps: []string{"example.com/other"}}, {ImportPath: "example.com/client", TestImports: []string{"example.com/testkit", "example.com/other"}}}, []string{"example.com/other", "example.com/testkit"}, false},
		{"assert-only", []goPackage{{ImportPath: "example.com/client", TestImports: []string{"github.com/stretchr/testify/assert"}}}, []string{"github.com/stretchr/testify/assert"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths, suite := testifyDependencyImports(tc.packages)
			if !slices.Equal(paths, tc.want) || suite != tc.suite {
				t.Fatalf("imports = %v, suite = %v; want %v, %v", paths, suite, tc.want, tc.suite)
			}
		})
	}
}
