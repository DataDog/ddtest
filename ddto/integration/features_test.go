package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func (c *capture) featureResponse(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case strings.HasSuffix(r.URL.Path, "/setting"):
		attrs := map[string]any{"itr_enabled": c.profile == "itr", "tests_skipping": c.profile == "itr", "require_git": false, "code_coverage": false, "known_tests_enabled": c.profile == "efd", "impacted_tests_enabled": false, "flaky_test_retries_enabled": false,
			"early_flake_detection": map[string]any{"enabled": c.profile == "efd", "faulty_session_threshold": 100, "slow_test_retries": map[string]int{"5s": 2, "10s": 2, "30s": 2, "5m": 2}},
			"test_management":       map[string]any{"enabled": c.profile == "disabled" || c.profile == "quarantined" || c.profile == "attempt_to_fix", "attempt_to_fix_retries": 2}}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "poc", "type": "ci_app_test_service_libraries_settings", "attributes": attrs}})
	case strings.HasSuffix(r.URL.Path, "/test-management/tests"):
		properties := map[string]bool{"disabled": c.profile == "disabled", "quarantined": c.profile == "quarantined", "attempt_to_fix": c.profile == "attempt_to_fix"}
		attrs := map[string]any{"modules": map[string]any{"example.com/dd-ci-testing-fixture_test": map[string]any{"suites": map[string]any{"sample_test.go": map[string]any{"tests": map[string]any{"TestManaged": map[string]any{"properties": properties}}}}}}}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "poc", "type": "ci_app_libraries_tests_request", "attributes": attrs}})
	case strings.HasSuffix(r.URL.Path, "/ci/libraries/tests"):
		fmt.Fprint(w, `{"data":{"id":"poc","type":"ci_app_libraries_tests","attributes":{"tests":{"example.com/dd-ci-testing-fixture_test":{"sample_test.go":["KnownOtherTest"]}}}}}`)
	case strings.HasSuffix(r.URL.Path, "/skippable"):
		data, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var request struct {
			Data struct {
				Attributes struct {
					Configurations map[string]any `json:"configurations"`
				} `json:"attributes"`
			} `json:"data"`
		}
		_ = json.Unmarshal(data, &request)
		attrs := map[string]any{"suite": "sample_test.go", "name": "TestManaged", "parameters": "", "configurations": request.Data.Attributes.Configurations}
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{"correlation_id": "poc"}, "data": []any{map[string]any{"id": "poc", "type": "test", "attributes": attrs}}})
	default:
		return false
	}
	return true
}
func TestSDKFeaturePolicies(t *testing.T) {
	reference := os.Getenv("ORCHESTRION_BIN")
	if reference != "" {
		var e error
		reference, e = filepath.Abs(reference)
		if e != nil {
			t.Fatal(e)
		}
	}
	dir, driver := prepareFixture(t, reference != "")
	var bins []string
	for _, compiler := range []struct {
		name string
		args []string
	}{{driver, []string{"test"}}, {"go", []string{"test", "-toolexec=" + reference + " toolexec"}}} {
		if compiler.name == "go" && reference == "" {
			continue
		}
		bin := filepath.Join(t.TempDir(), executableName("fixture.test"))
		args := append(compiler.args, "-c", "-o", bin, ".")
		out, e, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), compiler.name, args...)
		if code != 0 {
			t.Fatalf("compile: %s\n%s", out, e)
		}
		bins = append(bins, bin)
	}
	for _, profile := range []string{"disabled", "quarantined", "attempt_to_fix", "efd", "itr"} {
		t.Run(profile, func(t *testing.T) {
			args := []string{"-test.run=^TestManaged$", "-mode=managed"}
			wantStatus := "skip"
			wantExit := 0
			if profile == "quarantined" {
				wantStatus = "fail"
			}
			if profile == "attempt_to_fix" {
				args[1] = "-mode=fix"
				wantExit = 1
				wantStatus = "pass"
			}
			if profile == "efd" {
				args = []string{"-test.run=^TestPass$"}
				wantStatus = "pass"
			}
			got := executeProfile(t, dir, bins[0], args, true, false, profile)
			found := false
			testEvents := 0
			for _, event := range got.events {
				if strings.Contains(event, `"type":"test"`) {
					testEvents++
					if strings.Contains(event, `"test.status":"`+wantStatus+`"`) {
						found = true
					}
				}
			}
			if got.code != wantExit || !found {
				t.Fatalf("policy %s not exercised: exit %d events %v\n%s", profile, got.code, got.events, got.stderr)
			}
			if (profile == "efd" || profile == "attempt_to_fix") && testEvents < 2 {
				t.Fatalf("policy %s did not retry: %v", profile, got.events)
			}
			if len(bins) > 1 {
				want := executeProfile(t, dir, bins[1], args, true, false, profile)
				if got.code != want.code || !reflect.DeepEqual(got.events, want.events) {
					t.Fatalf("policy mismatch\n%v\n%v", got.events, want.events)
				}
			}
		})
	}
}
