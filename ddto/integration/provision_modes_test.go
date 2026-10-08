package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceCommandPreservesLogicalWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	physical := filepath.Join(root, "physical")
	logical := filepath.Join(root, "alias")
	client := filepath.Join(physical, "client")
	if err := os.MkdirAll(client, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(physical, logical); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	writeBuildFixture(t, physical, map[string]string{"go.work": "go 1.21\nuse ./client\n"})
	writeBuildFixture(t, client, map[string]string{"go.mod": "module example.com/logical\ngo 1.21\n", "logical_test.go": "package logical\nimport \"testing\"\nfunc TestLogical(t *testing.T){}\n"})
	for _, dir := range []string{physical, logical} {
		out, stderr, code := command(t, filepath.Join(dir, "client"), testEnv("GOWORK="+filepath.Join(dir, "go.work")), "go", "test", "-count=1", ".")
		if code != 0 {
			t.Fatalf("logical workspace failed: %s%s", out, stderr)
		}
	}
}

func TestMiniProvisionsWorkspaceWithoutChangingModules(t *testing.T) {
	_, driver := prepareMiniFixture(t)
	for _, workspaceReplacement := range []bool{false, true} {
		t.Run(fmt.Sprint(workspaceReplacement), func(t *testing.T) {
			root := t.TempDir()
			client := filepath.Join(root, "client")
			helper := filepath.Join(root, "helper")
			for _, dir := range []string{client, helper} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			writeBuildFixture(t, client, map[string]string{
				"go.mod": "module example.com/workclient\ngo 1.21\nrequire example.com/workhelper v0.0.0\n",
				"client_test.go": `package workclient
import("testing";"fmt";"time";"example.com/workhelper")
func TestWorkspace(t *testing.T){if workhelper.Value!=7{t.Fatal("workspace helper missing")};var fs []func()int;for i:=0;i<3;i++{fs=append(fs,func()int{return i})};if fs[0]()!=3{t.Fatal("language changed")};timer:=time.NewTimer(time.Hour);defer timer.Stop();fmt.Printf("TIMER_CAP=%d\n",cap(timer.C))}
`,
			})
			writeBuildFixture(t, helper, map[string]string{"go.mod": "module example.com/workhelper\ngo 1.21\n", "helper.go": "package workhelper\nconst Value=7\n"})
			work := "go 1.21\nuse(\n./client\n./helper\n)\n"
			if workspaceReplacement {
				sdkRoot, _ := filepath.Abs("..")
				work += fmt.Sprintf("replace github.com/tonyredondo/dd-ci-testing-poc => %q\n", filepath.ToSlash(sdkRoot))
			}
			writeBuildFixture(t, root, map[string]string{"go.work": work})
			beforeClient, _ := os.ReadFile(filepath.Join(client, "go.mod"))
			beforeHelper, _ := os.ReadFile(filepath.Join(helper, "go.mod"))
			capture := new(miniWireCapture)
			server := httptest.NewServer(http.HandlerFunc(capture.handler))
			defer server.Close()
			env := testEnv("GOWORK="+filepath.Join(root, "go.work"), "DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_API_KEY=fixture")
			native, nativeErr, nativeCode := command(t, client, env, "go", "test", "-v", "-count=1", ".")
			if nativeCode != 0 {
				t.Fatalf("native workspace: %s%s", native, nativeErr)
			}
			out, stderr, code := command(t, client, env, driver, "test", "-v", "-count=1", ".")
			if code != 0 {
				t.Fatalf("exit=%d\n%s%s", code, out, stderr)
			}
			assertNativeTimerCapacity(t, native, out)
			if len(capture.payloads) == 0 {
				t.Fatal("workspace tests did not report CI events")
			}
			for path, want := range map[string]string{filepath.Join(root, "go.work"): work, filepath.Join(client, "go.mod"): string(beforeClient), filepath.Join(helper, "go.mod"): string(beforeHelper)} {
				if data, err := os.ReadFile(path); err != nil || string(data) != want {
					t.Fatalf("changed %s: %s %v", path, data, err)
				}
			}
			if _, err := os.Stat(filepath.Join(root, "go.work.sum")); !os.IsNotExist(err) {
				t.Fatalf("created original workspace sum: %v", err)
			}
		})
	}
}

func TestMiniProvisionsVendorAndPreservesPatchedSources(t *testing.T) {
	_, driver := prepareMiniFixture(t)
	for _, mode := range []string{"", "-mod=vendor", "-modfile=alternate.mod"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			helper := t.TempDir()
			writeBuildFixture(t, helper, map[string]string{"go.mod": "module example.com/vendorhelper\ngo 1.21\n", "helper.go": "package vendorhelper\nconst Value=1\n"})
			mod := fmt.Sprintf("module example.com/vendorclient\ngo 1.21\nrequire example.com/vendorhelper v0.0.0\nreplace example.com/vendorhelper => %q\n", filepath.ToSlash(helper))
			if mode == "-modfile=alternate.mod" {
				mod += "godebug default=go1.25\n"
			}
			writeBuildFixture(t, dir, map[string]string{
				"go.mod":         mod,
				"client_test.go": "package vendorclient\nimport(\"testing\";\"fmt\";\"time\";\"example.com/vendorhelper\")\nfunc TestVendor(t *testing.T){if vendorhelper.Value!=7{t.Fatal(\"vendored patch lost\")};timer:=time.NewTimer(time.Hour);defer timer.Stop();fmt.Printf(\"TIMER_CAP=%d\\n\",cap(timer.C))}\n",
			})
			if mode == "-modfile=alternate.mod" {
				if err := os.WriteFile(filepath.Join(dir, "alternate.mod"), []byte(mod), 0600); err != nil {
					t.Fatal(err)
				}
			}
			env := testEnv("GOWORK=off", "GOPROXY=off", "DD_CIVISIBILITY_ENABLED=false")
			if out, stderr, code := command(t, dir, env, "go", "mod", "vendor"); code != 0 {
				t.Fatal(out, stderr)
			}
			patched := filepath.Join(dir, "vendor", "example.com", "vendorhelper", "helper.go")
			if err := os.WriteFile(patched, []byte("package vendorhelper\nconst Value=7\n"), 0600); err != nil {
				t.Fatal(err)
			}
			manifest := filepath.Join(dir, "vendor", "modules.txt")
			before, _ := os.ReadFile(manifest)
			var nativeOutput string
			for i, prefix := range [][]string{{"go", "test"}, {driver, "test"}} {
				args := append(prefix[1:], "-v", "-count=1")
				if mode != "" {
					args = append(args, mode)
				}
				out, stderr, code := command(t, dir, env, prefix[0], args...)
				if code != 0 {
					t.Fatalf("%v exit=%d\n%s%s", prefix, code, out, stderr)
				}
				if i == 0 {
					nativeOutput = out
				} else {
					assertNativeTimerCapacity(t, nativeOutput, out)
				}
			}
			if data, _ := os.ReadFile(manifest); string(data) != string(before) {
				t.Fatal("vendor manifest changed")
			}
			if data, _ := os.ReadFile(filepath.Join(dir, "go.mod")); strings.TrimSpace(string(data)) != strings.TrimSpace(mod) {
				t.Fatal("client module changed")
			}
		})
	}
}
