package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func prepareCodeOwnersFixture(t *testing.T) (string, string) {
	t.Helper()
	dir, driver := prepareMiniFixture(t)
	if err := os.MkdirAll(filepath.Join(dir, ".github"), 0755); err != nil {
		t.Fatal(err)
	}
	owners := "* @example/fallback\n/**/alpha @example/alpha-team @example/second\n/beta/ @another/beta-team\n/unowned/\n"
	if err := os.WriteFile(filepath.Join(dir, ".github", "CODEOWNERS"), []byte(owners), 0600); err != nil {
		t.Fatal(err)
	}
	for _, pkg := range []string{"alpha", "beta", "unowned"} {
		if err := os.MkdirAll(filepath.Join(dir, pkg), 0755); err != nil {
			t.Fatal(err)
		}
		source := "package same\nimport \"testing\"\nfunc TestService(t *testing.T) { t.Log(\"package log\") }\n"
		if pkg == "alpha" {
			source = `package same
import (
    "os"
    "testing"
    "github.com/stretchr/testify/suite"
    "go.uber.org/goleak"
)
func TestMain(m *testing.M) {
    if os.Getenv("POC_CHANGE_TEST_DIRECTORY")=="true" {
        if err:=os.Chdir(os.Getenv("GITHUB_WORKSPACE"));err!=nil { panic(err) }
    }
    os.Exit(m.Run())
}
func TestService(t *testing.T) { t.Log("package log"); goleak.VerifyNone(t) }
type OwnedSuite struct { suite.Suite }
func (s *OwnedSuite) TestPass() { s.T().Log("suite log"); goleak.VerifyNone(s.T()) }
func TestOwnedSuite(t *testing.T) { suite.Run(t, new(OwnedSuite)) }
`
		}
		if err := os.WriteFile(filepath.Join(dir, pkg, "value_test.go"), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		if pkg == "beta" {
			external := "package same_test\nimport \"testing\"\nfunc TestExternal(t *testing.T) { t.Log(\"external log\") }\n"
			if err := os.WriteFile(filepath.Join(dir, pkg, "external_test.go"), []byte(external), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir, driver
}

type codeOwnersReceiver struct {
	parityReceiver
	settingsMu       sync.Mutex
	settingsServices []string
}

func (c *codeOwnersReceiver) handler(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/setting") {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			c.fail(err)
			w.WriteHeader(400)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		var request struct {
			Data struct {
				Attributes struct {
					Service string `json:"service"`
				} `json:"attributes"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			c.fail(err)
			w.WriteHeader(400)
			return
		}
		c.settingsMu.Lock()
		c.settingsServices = append(c.settingsServices, request.Data.Attributes.Service)
		c.settingsMu.Unlock()
	}
	c.parityReceiver.handler(w, r)
}
func codeOwnersTestEnv(dir, url string, extra ...string) []string {
	env := testEnv("DD_SERVICE=", "GITHUB_SHA=1111111111111111111111111111111111111111", "GITHUB_WORKSPACE="+dir,
		"DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=false", "DD_TRACE_AGENT_URL="+url,
		"DD_CIVISIBILITY_SERVICE_FROM_CODEOWNERS=true", "DD_HOSTNAME=ci-service-fixture")
	return append(env, extra...)
}
func checkCodeOwnersServices(t *testing.T, c *codeOwnersReceiver, want map[string]bool) {
	t.Helper()
	if len(c.failures) != 0 {
		t.Fatal(c.failures)
	}
	seen := map[string]bool{}
	for _, event := range c.events {
		content := event["content"].(map[string]any)
		service, _ := content["service"].(string)
		if !want[service] {
			t.Errorf("%v service=%q, want one of %v", event["type"], service, want)
		}
		seen[service] = true
	}
	if len(seen) != len(want) {
		t.Fatalf("services=%v, want %v", seen, want)
	}
	if len(c.settingsServices) == 0 {
		t.Fatal("settings service was not checked")
	}
	for _, service := range c.settingsServices {
		if !want[service] {
			t.Errorf("settings service=%q, want %v", service, want)
		}
	}
	if err := validateEventGraph(c.events); err != nil {
		t.Fatal(err)
	}
	for path, bodies := range c.side {
		for _, body := range bodies {
			if strings.Contains(path, "telemetry") {
				var payload struct {
					Application struct {
						Service string `json:"service_name"`
					} `json:"application"`
				}
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatal(err)
				}
				if !want[payload.Application.Service] {
					t.Errorf("telemetry service=%q, want %v", payload.Application.Service, want)
				}
			} else if strings.HasSuffix(path, "/logs") {
				var logs []map[string]any
				if err := json.Unmarshal(body, &logs); err != nil {
					t.Fatal(err)
				}
				for _, entry := range logs {
					if !want[fmt.Sprint(entry["service"])] {
						t.Errorf("log service=%v, want %v", entry["service"], want)
					}
				}
			}
		}
	}
}
func newCodeOwnersReceiver() *codeOwnersReceiver {
	return &codeOwnersReceiver{parityReceiver: parityReceiver{side: map[string][][]byte{}, requests: map[string]int{}}}
}

// Equal package names continue sharing generated backing files. Settings and
// all event hierarchy levels must retain each package's own service.
func TestMiniCodeOwnersPackageServices(t *testing.T) {
	dir, driver := prepareCodeOwnersFixture(t)
	c := newCodeOwnersReceiver()
	server := httptest.NewServer(http.HandlerFunc(c.handler))
	out, stderr, code := command(t, dir, codeOwnersTestEnv(dir, server.URL), driver, "test", "--runtime=mini", "-mod=mod", "-count=1", "-run=^Test(Service|External)$", "./alpha", "./beta", "./unowned")
	server.Close()
	if code != 0 {
		t.Fatalf("package services: %d\n%s\n%s", code, out, stderr)
	}
	checkCodeOwnersServices(t, c, map[string]bool{"service-alpha-team": true, "service-beta-team": true, "service-not-owned": true})
	packages := map[string]string{
		"example.com/dd-ci-testing-fixture/alpha":     "service-alpha-team",
		"example.com/dd-ci-testing-fixture/beta":      "service-beta-team",
		"example.com/dd-ci-testing-fixture/beta_test": "service-beta-team",
		"example.com/dd-ci-testing-fixture/unowned":   "service-not-owned",
	}
	for _, event := range c.events {
		content := event["content"].(map[string]any)
		meta, _ := content["meta"].(map[string]any)
		if module, _ := meta["test.module"].(string); module != "" {
			if want := packages[module]; want == "" || content["service"] != want {
				t.Errorf("module=%q service=%v, want %q", module, content["service"], want)
			}
		}
	}
}

func TestMiniCodeOwnersCompiledServices(t *testing.T) {
	dir, driver := prepareCodeOwnersFixture(t)
	for _, build := range []struct {
		name  string
		flags []string
	}{
		{"normal", nil},
		{"race-trimpath-coverage", []string{"-race", "-trimpath", "-cover", "-covermode=atomic", "-coverpkg=./..."}},
	} {
		t.Run(build.name, func(t *testing.T) {
			output := t.TempDir()
			args := append([]string{"test", "--runtime=mini", "-mod=mod", "-c", "-o", output + string(filepath.Separator)}, build.flags...)
			args = append(args, "./alpha", "./beta", "./unowned")
			// Runtime configuration is applied after compilation. Preparing with
			// the feature disabled must still produce a configurable binary.
			out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_SERVICE_FROM_CODEOWNERS=false"), driver, args...)
			if code != 0 {
				t.Fatalf("compile %d\n%s\n%s", code, out, stderr)
			}
			for _, tc := range []struct {
				name, pkg, service string
				runDirectory       string
				env, args          []string
			}{
				{name: "default-format", pkg: "alpha", service: "service-alpha-team"},
				{name: "custom-format", pkg: "alpha", service: "dd-go-alpha-team", env: []string{"DD_CIVISIBILITY_SERVICE_FROM_CODEOWNERS_FORMAT=dd-go-$(owner)"}},
				{name: "explicit-service", pkg: "alpha", service: "explicit", env: []string{"DD_SERVICE=explicit"}},
				{name: "disabled", pkg: "alpha", service: "dd-ci-testing-poc", env: []string{"DD_CIVISIBILITY_SERVICE_FROM_CODEOWNERS=false"}},
				{name: "external", pkg: "beta", service: "service-beta-team", args: []string{"-test.run=^TestExternal$"}},
				{name: "unowned", pkg: "unowned", service: "service-not-owned"},
				{name: "other-working-directory", pkg: "alpha", service: "service-alpha-team", runDirectory: "beta"},
				{name: "chdir-TestMain", pkg: "alpha", service: "service-alpha-team", env: []string{"POC_CHANGE_TEST_DIRECTORY=true"}},
				{name: "Testify-and-goleak", pkg: "alpha", service: "service-alpha-team", args: []string{"-test.run=^TestOwnedSuite$"}},
				{name: "deferred-goleak", pkg: "alpha", service: "service-alpha-team", env: []string{"DD_CIVISIBILITY_DEFERRED_DELIVERY=true"}},
				{name: "telemetry-logs", pkg: "alpha", service: "service-alpha-team", env: []string{"DD_INSTRUMENTATION_TELEMETRY_ENABLED=true", "DD_CIVISIBILITY_LOGS_ENABLED=true"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					c := newCodeOwnersReceiver()
					server := httptest.NewServer(http.HandlerFunc(c.handler))
					args := []string{"-test.count=1", "-test.run=^TestService$"}
					if len(tc.args) != 0 {
						args = append([]string{"-test.count=1"}, tc.args...)
					}
					bin := filepath.Join(output, executableName(tc.pkg+".test"))
					// Run from the repository root, rather than the package
					// directory used by go test; this exercises -c identity.
					runDir := dir
					if tc.runDirectory != "" {
						runDir = filepath.Join(dir, tc.runDirectory)
					}
					out, stderr, code := command(t, runDir, codeOwnersTestEnv(dir, server.URL, tc.env...), bin, args...)
					server.Close()
					if code != 0 {
						t.Fatalf("run %d\n%s\n%s", code, out, stderr)
					}
					checkCodeOwnersServices(t, c, map[string]bool{tc.service: true})
					if tc.name == "telemetry-logs" {
						telemetry, logs := false, false
						for path, bodies := range c.side {
							if len(bodies) > 0 {
								telemetry = telemetry || strings.Contains(path, "telemetry")
								logs = logs || strings.HasSuffix(path, "/logs")
							}
						}
						if !telemetry || !logs {
							t.Fatalf("telemetry=%t logs=%t", telemetry, logs)
						}
					}
				})
			}
		})
	}
}

// These expectations come from the host specifications, not the older SDK
// parser. Verify ownership and service on actual events received over HTTP.
func TestMiniCodeOwnersSpecificationOnWire(t *testing.T) {
	for _, dialect := range []string{"github", "gitlab"} {
		t.Run(dialect, func(t *testing.T) {
			dir, driver := prepareCodeOwnersFixture(t)
			if dialect == "github" {
				if err := os.WriteFile(filepath.Join(dir, "CODEOWNERS"), []byte("* @wrong-priority\n"), 0600); err != nil {
					t.Fatal(err)
				}
				rules := "* @fallback\n/**/alpha @example/alpha-team @example/second # @ignored\n/beta/ @example/beta-team\n/unowned/\n"
				if err := os.WriteFile(filepath.Join(dir, ".github", "CODEOWNERS"), []byte(rules), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				rules := "[Packages] @example/alpha-team\n/alpha/\n/beta/ @example/beta-team\n!/unowned/\n"
				if err := os.WriteFile(filepath.Join(dir, "CODEOWNERS"), []byte(rules), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, deferred := range []string{"false", "true"} {
				t.Run("deferred="+deferred, func(t *testing.T) {
					c := newCodeOwnersReceiver()
					server := httptest.NewServer(http.HandlerFunc(c.handler))
					extra := []string{"DD_CIVISIBILITY_DEFERRED_DELIVERY=" + deferred}
					if dialect == "gitlab" {
						extra = append(extra, "DD_GIT_REPOSITORY_URL=https://gitlab.com/example/repo.git")
					}
					out, stderr, code := command(t, dir, codeOwnersTestEnv(dir, server.URL, extra...), driver, "test", "--runtime=mini", "-mod=mod", "-count=1", "-run=^TestService$", "./alpha", "./beta", "./unowned")
					server.Close()
					if code != 0 {
						t.Fatalf("exit=%d\n%s\n%s", code, out, stderr)
					}
					checkCodeOwnersServices(t, c, map[string]bool{"service-alpha-team": true, "service-beta-team": true, "service-not-owned": true})
					count := 0
					for _, event := range c.events {
						if event["type"] != "test" {
							continue
						}
						content := event["content"].(map[string]any)
						meta := content["meta"].(map[string]any)
						service, _ := content["service"].(string)
						want := ""
						switch service {
						case "service-alpha-team":
							want = `["@example/alpha-team","@example/second"]`
							if dialect == "gitlab" {
								want = `["@example/alpha-team"]`
							}
						case "service-beta-team":
							want = `["@example/beta-team"]`
						}
						got, _ := meta["test.codeowners"].(string)
						if got != want {
							t.Errorf("service=%q codeowners=%q want=%q", service, got, want)
						}
						count++
					}
					if count != 3 {
						t.Fatalf("test events=%d, want 3", count)
					}
				})
			}
		})
	}
}
