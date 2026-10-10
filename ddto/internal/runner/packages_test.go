package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPackageListHelperProcess(t *testing.T) {
	if os.Getenv("DDTO_PACKAGE_LIST_HELPER") != "1" {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "success":
		for _, part := range []string{`{"Import`, `Path":"example.com/one","Imports":["fmt"]}`, "\n", `{"ImportPath":"example.com/two"}`} {
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
	case "descendant":
		cmd := exec.Command(os.Args[0], "-test.run=^TestPackageListHelperProcess$", "--", "hold-stdout")
		cmd.Env = os.Environ()
		cmd.Stdout = os.Stdout
		if err := cmd.Start(); err != nil {
			panic(err)
		}
		time.Sleep(10 * time.Second)
	case "hold-stdout":
		control := os.Getenv("DDTO_PACKAGE_LIST_CONTROL")
		if err := os.WriteFile(filepath.Join(control, "ready"), nil, 0600); err != nil {
			panic(err)
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(control, "release")); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
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
			cmd.Env = append(os.Environ(), "DDTO_PACKAGE_LIST_HELPER=1")
			packages, err := readPackages(ctx, cmd, "resolve fixture")
			if mode == "success" {
				if err != nil || len(packages) != 2 || packages[0].ImportPath != "example.com/one" || packages[1].ImportPath != "example.com/two" || !slices.Equal(packages[0].Imports, []string{"fmt"}) {
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
	if packages, err := readPackages(context.Background(), cmd, "resolve fixture"); err == nil || packages != nil {
		t.Fatalf("missing executable = %+v, %v", packages, err)
	}
}

func TestReadPackagesCancelsAnInheritedStdout(t *testing.T) {
	control := t.TempDir()
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(control, "release"), nil, 0600) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPackageListHelperProcess$", "--", "descendant")
	cmd.Env = append(os.Environ(), "DDTO_PACKAGE_LIST_HELPER=1", "DDTO_PACKAGE_LIST_CONTROL="+control)
	result := make(chan error, 1)
	go func() { _, err := readPackages(ctx, cmd, "resolve descendant"); result <- err }()
	deadline := time.Now().Add(8 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(control, "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stdout holder did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-result:
		if err == nil || cmd.ProcessState == nil {
			t.Fatalf("canceled command was not joined: %v", err)
		}
	case <-time.After(2 * time.Second):
		_ = os.WriteFile(filepath.Join(control, "release"), nil, 0600)
		<-result
		t.Fatal("cancellation waited for the descendant to close stdout")
	}
}

func TestTestifyDependencyImports(t *testing.T) {
	for _, tc := range []struct {
		name          string
		packages, dep []goPackage
		want          []string
		suite         bool
	}{
		{"known production closure", []goPackage{{ImportPath: "testing", Imports: []string{"fmt"}}, {ImportPath: "example.com/client", Imports: []string{"example.com/helper", "fmt"}, TestImports: []string{"testing", "fmt", "example.com/helper", "example.com/client"}}}, []goPackage{{ImportPath: "fmt"}, {ImportPath: "example.com/helper"}}, nil, false},
		{"transitive production closure", []goPackage{{ImportPath: "example.com/client", Imports: []string{"example.com/helper"}, TestImports: []string{"example.com/deep"}}}, []goPackage{{ImportPath: "example.com/helper", Imports: []string{"example.com/deep"}}, {ImportPath: "example.com/deep"}}, nil, false},
		{"unknown external helper", []goPackage{{ImportPath: "example.com/client", TestImports: []string{"example.com/testkit"}, XTestImports: []string{"example.com/testkit"}}}, nil, []string{"example.com/testkit"}, false},
		{"test-only standard import", []goPackage{{ImportPath: "testing", Imports: []string{"fmt"}}, {ImportPath: "example.com/client", TestImports: []string{"net/http/httptest"}}}, []goPackage{{ImportPath: "fmt"}}, []string{"net/http/httptest"}, false},
		{"suite before pruning", []goPackage{{ImportPath: "example.com/client", Imports: []string{"github.com/stretchr/testify/suite"}, XTestImports: []string{"github.com/stretchr/testify/suite"}}}, []goPackage{{ImportPath: "github.com/stretchr/testify/suite"}}, nil, true},
		{"direct suite", []goPackage{{ImportPath: "example.com/client", TestImports: []string{"github.com/stretchr/testify/suite"}}}, nil, []string{"github.com/stretchr/testify/suite"}, true},
		{"runtime closure is unrelated", []goPackage{{ImportPath: miniPackage, Imports: []string{"example.com/testkit", "github.com/stretchr/testify/suite"}}, {ImportPath: sdkPackage, Imports: []string{"example.com/other"}}, {ImportPath: "example.com/client", TestImports: []string{"example.com/testkit", "example.com/other"}}}, []goPackage{{ImportPath: "example.com/testkit"}, {ImportPath: "example.com/other"}, {ImportPath: "github.com/stretchr/testify/suite"}}, []string{"example.com/other", "example.com/testkit"}, false},
		{"assert-only", []goPackage{{ImportPath: "example.com/client", TestImports: []string{"github.com/stretchr/testify/assert"}}}, nil, []string{"github.com/stretchr/testify/assert"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			graph := newPackageGraph(append(slices.Clone(tc.packages), tc.dep...))
			paths, suite := testifyDependencyImports(tc.packages, clientImports(tc.packages, graph))
			if !slices.Equal(paths, tc.want) || suite != tc.suite {
				t.Fatalf("imports = %v, suite = %v; want %v, %v", paths, suite, tc.want, tc.suite)
			}
		})
	}
}

// The graph closure equals the union of the Deps that go list computes for
// each package, which the package query no longer requests.
func TestPackageGraphMatchesDeps(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	patterns := []string{"./internal/runner", "./testopt", "os/user", "net/http", "testing"}
	// Linux with cgo adds the implicit imports of net and os/user, which other
	// platforms build without cgo; go list needs no C compiler for them.
	for _, env := range [][]string{nil, {"GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=1"}} {
		t.Run(strings.Join(append([]string{"host"}, env...), ","), func(t *testing.T) {
			goList := func(args ...string) *exec.Cmd {
				cmd := exec.Command("go", append(args, patterns...)...)
				cmd.Dir = root
				cmd.Env = append(os.Environ(), env...)
				return cmd
			}
			listed, err := readPackages(t.Context(), goList("list", "-e", "-deps", "-json=ImportPath,DepOnly,Imports"), "graph")
			if err != nil {
				t.Fatal(err)
			}
			graph := newPackageGraph(listed)
			out, err := goList("list", "-e", "-f", "{{.ImportPath}} {{join .Deps \" \"}}").Output()
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				fields := strings.Fields(line)
				p := graph[fields[0]]
				if p == nil || p.DepOnly {
					t.Fatalf("%s is not a named package of the graph", fields[0])
				}
				got := slices.Sorted(maps.Keys(graph.imported(p)))
				want := slices.Sorted(slices.Values(fields[1:]))
				if !slices.Equal(got, want) {
					t.Fatalf("%s: graph closure differs from Deps\n%v\n%v", fields[0], got, want)
				}
			}
		})
	}
}
