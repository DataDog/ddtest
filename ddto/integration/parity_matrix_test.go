package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

// policySettings deliberately keeps remote settings independent from the
// process environment. Cases can therefore exercise overrides and precedence.
type policySettings struct {
	Retry               bool
	EFD                 bool
	ITR                 bool
	Coverage            bool
	CoverageReport      bool
	Known               bool
	Disabled            bool
	Quarantined         bool
	AttemptToFix        bool
	ManagementTarget    string
	ManagementSuite     string
	SettingsDelay       time.Duration
	SettingsFailure     bool
	Impacted            bool
	RequireGit          bool
	FaultyThreshold     *int
	MissingLineCoverage bool
	SkippableTarget     string
	SkippableSuite      string
}

type parityCase struct {
	Name            string
	Features        []string
	Args            []string
	Env             []string
	Policy          policySettings
	WantExit        int
	MinTests        int
	RequiredTag     string
	RequiredValue   string
	Coverage        bool
	Logs            bool
	Git             bool
	RequireEndpoint string
	RequiredEvent   string
}

func parityCases() []parityCase {
	run := func(name string) []string { return []string{"-test.run=^" + name + "$"} }
	cases := []parityCase{
		{Name: "pass", Features: []string{"lifecycle"}, Args: run("TestPass"), MinTests: 1},
		{Name: "skip-reasons", Features: []string{"skip"}, Args: run("Test(Skip|Skipf|SkipNow)"), MinTests: 3},
		{Name: "nested-cleanup-context", Features: []string{"subtests", "cleanup", "context"}, Args: run("Test(Nested|Cleanup|Context)"), MinTests: 6},
		{Name: "parallel-count-shuffle", Features: []string{"parallel", "count", "shuffle"}, Args: append(run("TestParallel"), "-test.count=2", "-test.shuffle=42", "-test.parallel=8"), MinTests: 18},
		{Name: "multiple-suites", Features: []string{"hierarchy", "source"}, Args: run("Test(Pass|ParityOtherSuite)"), MinTests: 2},
		{Name: "empty-selection", Features: []string{"lifecycle", "empty"}, Args: run("DoesNotExist")},
		{Name: "list", Features: []string{"lifecycle", "list"}, Args: []string{"-test.list=TestPass"}},
		// Fuzz and Examples are covered against SDK PR #5442 in TestFuzzExampleParity.
		{Name: "settings-unavailable", Features: []string{"settings", "failure"}, Args: run("TestPass"), Policy: policySettings{SettingsFailure: true}, MinTests: 1},
		{Name: "tags-version-ci", Features: []string{"metadata", "version", "ci"}, Args: run("TestPass"), Env: []string{"DD_VERSION=parity-1", "DD_TAGS=team:ci,custom:retained", "GITHUB_ACTIONS=true", "GITHUB_JOB=unit-tests", "GITHUB_REPOSITORY=tonyredondo/dd-ci-testing-poc", "GITHUB_RUN_ID=123"}, MinTests: 1},
		{Name: "coverage-pass-skip", Features: []string{"coverage", "skip"}, Args: run("Test(Pass|Skip)"), Policy: policySettings{Coverage: true}, Coverage: true, MinTests: 2},
		{Name: "coverage-parallel-shuffle", Features: []string{"coverage", "parallel", "shuffle"}, Args: append(run("Test(Pass|Parallel)"), "-test.shuffle=42", "-test.parallel=8"), Policy: policySettings{Coverage: true}, Coverage: true, MinTests: 10},
		{Name: "coverage-report", Features: []string{"coverage-report"}, Args: run("TestPass"), Policy: policySettings{CoverageReport: true}, Env: []string{"DD_CIVISIBILITY_CODE_COVERAGE_REPORT_UPLOAD_ENABLED=true", "DD_CODE_COVERAGE_FLAGS=unit,parity"}, MinTests: 1},
		{Name: "logs", Features: []string{"logs"}, Args: append(run("TestPass"), "-test.v"), Logs: true, MinTests: 1},
	}
	for _, method := range []string{"Fail", "FailNow", "Error", "Errorf", "Fatal", "Fatalf"} {
		cases = append(cases, parityCase{Name: "error-" + method, Features: []string{"errors"}, Args: []string{"-test.run=^TestFailures$/^" + method + "$", "-mode=fail"}, WantExit: 1, MinTests: 2})
	}
	for _, mode := range []string{"in_process", "process"} {
		suffix := "-" + mode
		retryEnv := []string{"DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2", "DD_CIVISIBILITY_TOTAL_FLAKY_RETRY_COUNT=4"}
		cases = append(cases,
			parityCase{Name: "atr-recovery" + suffix, Features: []string{"atr", mode}, Args: []string{"-test.run=^TestRetry$", "-mode=retry"}, Env: retryEnv, Policy: policySettings{Retry: true}, MinTests: 2, RequiredTag: "test.is_retry", RequiredValue: "true"},
			parityCase{Name: "atr-exhaustion" + suffix, Features: []string{"atr", mode, "failure"}, Args: []string{"-test.run=^TestManaged$", "-mode=managed"}, Env: retryEnv, Policy: policySettings{Retry: true}, WantExit: 1, MinTests: 3},
			parityCase{Name: "efd-new" + suffix, Features: []string{"efd", mode}, Args: run("TestPass"), Env: []string{"DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=true"}, Policy: policySettings{EFD: true}, MinTests: 2, RequiredTag: "test.is_new", RequiredValue: "true"},
			parityCase{Name: "efd-atr" + suffix, Features: []string{"efd", "atr", mode}, Args: []string{"-test.run=^TestRetry$", "-mode=retry"}, Env: append(append([]string{}, retryEnv...), "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=true"), Policy: policySettings{EFD: true, Retry: true}, MinTests: 2},
			parityCase{Name: "attempt-to-fix" + suffix, Features: []string{"attempt-to-fix", mode}, Args: []string{"-test.run=^TestManaged$", "-mode=fix"}, Env: []string{"DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode}, Policy: policySettings{AttemptToFix: true}, WantExit: 1, MinTests: 2, RequiredTag: "test.test_management.is_attempt_to_fix", RequiredValue: "true"},
			parityCase{Name: "disabled-atf-quarantine" + suffix, Features: []string{"disabled", "attempt-to-fix", "quarantine", mode}, Args: []string{"-test.run=^TestManaged$", "-mode=managed"}, Env: []string{"DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode}, Policy: policySettings{Disabled: true, Quarantined: true, AttemptToFix: true}, MinTests: 2},
			parityCase{Name: "atr-coverage" + suffix, Features: []string{"atr", "coverage", mode}, Args: []string{"-test.run=^TestRetry$", "-mode=retry"}, Env: retryEnv, Policy: policySettings{Retry: true, Coverage: true}, Coverage: true, MinTests: 2},
		)
	}
	cases = append(cases,
		parityCase{Name: "atr-env-enables-remote-off", Features: []string{"atr", "override"}, Args: []string{"-test.run=^TestRetry$", "-mode=retry"}, Env: []string{"DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=1"}, MinTests: 2},
		parityCase{Name: "atr-env-disables-remote-on", Features: []string{"atr", "override"}, Args: []string{"-test.run=^TestManaged$", "-mode=managed"}, Policy: policySettings{Retry: true}, WantExit: 1, MinTests: 1},
		parityCase{Name: "efd-known", Features: []string{"efd", "known-tests"}, Args: run("TestPass"), Policy: policySettings{EFD: true, Known: true}, Env: []string{"DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=true"}, MinTests: 1},
		parityCase{Name: "efd-cap-zero", Features: []string{"efd", "override"}, Args: run("TestPass"), Policy: policySettings{EFD: true}, Env: []string{"DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=true", "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_MAX_RETRIES=0"}, MinTests: 1},
		parityCase{Name: "itr", Features: []string{"itr"}, Args: []string{"-test.run=^TestManaged$", "-mode=managed"}, Policy: policySettings{ITR: true}, MinTests: 1, RequiredTag: "test.skipped_by_itr", RequiredValue: "true"},
		parityCase{Name: "itr-atr-efd", Features: []string{"itr", "atr", "efd"}, Args: []string{"-test.run=^TestManaged$", "-mode=managed"}, Policy: policySettings{ITR: true, Retry: true, EFD: true}, Env: []string{"DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=true"}, MinTests: 1, RequiredTag: "test.skipped_by_itr", RequiredValue: "true"},
		parityCase{Name: "itr-disabled-quarantine", Features: []string{"itr", "disabled", "quarantine"}, Args: []string{"-test.run=^TestManaged$", "-mode=managed"}, Policy: policySettings{ITR: true, Disabled: true, Quarantined: true}, MinTests: 1},
		parityCase{Name: "itr-coverage", Features: []string{"itr", "coverage"}, Args: run("Test(Pass|Managed)"), Policy: policySettings{ITR: true, Coverage: true}, Coverage: true, MinTests: 2},
		parityCase{Name: "disabled-atr-efd", Features: []string{"disabled", "atr", "efd"}, Args: []string{"-test.run=^TestManaged$", "-mode=managed"}, Policy: policySettings{Disabled: true, Retry: true, EFD: true}, Env: []string{"DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=true"}, MinTests: 1, RequiredTag: "test.test_management.is_test_disabled", RequiredValue: "true"},
		parityCase{Name: "quarantine-atr-efd", Features: []string{"quarantine", "atr", "efd"}, Args: []string{"-test.run=^TestManaged$", "-mode=managed"}, Policy: policySettings{Quarantined: true, Retry: true, EFD: true}, Env: []string{"DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=true"}, MinTests: 1, RequiredTag: "test.test_management.is_quarantined", RequiredValue: "true"},
		parityCase{Name: "subtest-disabled", Features: []string{"subtests", "disabled"}, Args: run("TestParityPolicy"), Policy: policySettings{Disabled: true, ManagementTarget: "TestParityPolicy/child"}, MinTests: 2, RequiredTag: "test.test_management.is_test_disabled", RequiredValue: "true"},
		parityCase{Name: "subtest-gate-off", WantExit: 1, Features: []string{"subtests", "disabled", "override"}, Args: run("TestParityPolicy"), Policy: policySettings{Disabled: true, ManagementTarget: "TestParityPolicy/child"}, Env: []string{"DD_CIVISIBILITY_SUBTEST_FEATURES_ENABLED=false"}, MinTests: 2},
		parityCase{Name: "subtest-quarantine", Features: []string{"subtests", "quarantine"}, Args: run("TestParityPolicy"), Policy: policySettings{Quarantined: true, ManagementTarget: "TestParityPolicy/child"}, MinTests: 2, RequiredTag: "test.test_management.is_quarantined", RequiredValue: "true"},
		parityCase{Name: "management-env-off", Features: []string{"disabled", "override"}, Args: []string{"-test.run=^TestManaged$", "-mode=managed"}, Policy: policySettings{Disabled: true}, Env: []string{"DD_TEST_MANAGEMENT_ENABLED=false"}, WantExit: 1, MinTests: 1},
	)
	cases = append(cases,
		parityCase{Name: "benchmarks", Features: []string{"benchmarks"}, Args: []string{"-test.run=^$", "-test.bench=BenchmarkWork", "-test.benchtime=1x"}, MinTests: 2},
		parityCase{Name: "impacted-known-efd", Features: []string{"impacted-tests", "known-tests", "efd"}, Args: run("TestPass"), Policy: policySettings{Impacted: true, EFD: true, Known: true}, Env: []string{"DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=true"}, Git: true, MinTests: 2, RequiredTag: "test.is_modified", RequiredValue: "true"},
		parityCase{Name: "impacted-env-off", Features: []string{"impacted-tests", "override"}, Args: run("TestPass"), Policy: policySettings{Impacted: true}, Env: []string{"DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED=false"}, Git: true, MinTests: 1},
		parityCase{Name: "git-upload-require-git", Features: []string{"git-upload", "settings"}, Args: run("TestPass"), Policy: policySettings{RequireGit: true}, Env: []string{"DD_CIVISIBILITY_GIT_UPLOAD_ENABLED=true"}, Git: true, MinTests: 1, RequireEndpoint: "/packfile"},
		parityCase{Name: "logs-atr", Features: []string{"logs", "atr"}, Args: []string{"-test.run=^TestRetry$", "-mode=retry", "-test.v"}, Policy: policySettings{Retry: true}, Env: []string{"DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=1"}, Logs: true, MinTests: 2},
		parityCase{Name: "report-itr-coverage", Features: []string{"coverage-report", "itr", "coverage"}, Args: run("Test(Pass|Managed)"), Policy: policySettings{CoverageReport: true, ITR: true, Coverage: true}, Env: []string{"DD_CIVISIBILITY_CODE_COVERAGE_REPORT_UPLOAD_ENABLED=true"}, Coverage: true, MinTests: 2},
	)
	threshold := 0
	cases = append(cases, parityCase{Name: "efd-faulty-session", Features: []string{"efd", "faulty-session"}, Args: run("TestPass"), Policy: policySettings{EFD: true, FaultyThreshold: &threshold}, Env: []string{"DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=true"}, MinTests: 1, RequiredTag: "test.early_flake.abort_reason", RequiredValue: "faulty", RequiredEvent: "test_session_end"})
	cases = append(cases,
		parityCase{Name: "itr-missing-line-coverage", Features: []string{"itr", "coverage", "missing-line-coverage"}, Args: []string{"-test.run=^TestManaged$", "-mode=managed"}, Policy: policySettings{ITR: true, Coverage: true, MissingLineCoverage: true}, Coverage: true, WantExit: 1, MinTests: 1, RequiredTag: "test.status", RequiredValue: "fail"},
		parityCase{Name: "itr-unskippable", Features: []string{"itr", "unskippable"}, Args: run("TestParityUnskippable"), Policy: policySettings{ITR: true, SkippableTarget: "TestParityUnskippable", SkippableSuite: "parity_cases_test.go"}, MinTests: 1, RequiredTag: "test.itr.forced_run", RequiredValue: "true"},
		parityCase{Name: "itr-impacted-efd-known", Features: []string{"itr", "impacted-tests", "efd", "known-tests"}, Args: run("TestPass"), Policy: policySettings{ITR: true, Impacted: true, EFD: true, Known: true, SkippableTarget: "TestPass"}, Env: []string{"DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=true"}, Git: true, MinTests: 2, RequiredTag: "test.is_modified", RequiredValue: "true"},
		parityCase{Name: "itr-attempt-to-fix", Features: []string{"itr", "attempt-to-fix"}, Args: []string{"-test.run=^TestManaged$", "-mode=fix"}, Policy: policySettings{ITR: true, AttemptToFix: true}, WantExit: 1, MinTests: 2, RequiredTag: "test.test_management.is_attempt_to_fix", RequiredValue: "true"},
		parityCase{Name: "atr-budget-zero", Features: []string{"atr", "budget"}, Args: []string{"-test.run=^TestManaged$", "-mode=managed"}, Policy: policySettings{Retry: true}, Env: []string{"DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_TOTAL_FLAKY_RETRY_COUNT=0"}, WantExit: 1, MinTests: 1},
		parityCase{Name: "efd-parallel-execution", Features: []string{"efd", "parallel-execution"}, Args: run("TestPass"), Policy: policySettings{EFD: true}, Env: []string{"DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=true", "DD_CIVISIBILITY_INTERNAL_PARALLEL_EARLY_FLAKE_DETECTION_ENABLED=true", "DD_CIVISIBILITY_RETRY_PROCESS_MAX_CONCURRENCY=2"}, MinTests: 2},
	)
	for _, mode := range []string{"in_process", "process"} {
		cases = append(cases, parityCase{Name: "atr-parallel-" + mode, Features: []string{"atr", "parallel", mode}, Args: []string{"-test.run=^TestParityParallelRetry$", "-test.parallel=4"}, Policy: policySettings{Retry: true}, Env: []string{"DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode}, MinTests: 2})
	}
	cases = append(cases,
		parityCase{Name: "agent-coverage-itr", Features: []string{"agent", "coverage", "itr"}, Args: run("Test(Pass|Managed)"), Policy: policySettings{ITR: true, Coverage: true}, Env: []string{"DD_CIVISIBILITY_AGENTLESS_ENABLED=false"}, Coverage: true, MinTests: 2, RequireEndpoint: "/evp_proxy/v2/api/v2/citestcycle"},
		parityCase{Name: "agent-retry", Features: []string{"agent", "atr"}, Args: []string{"-test.run=^TestRetry$", "-mode=retry"}, Policy: policySettings{Retry: true}, Env: []string{"DD_CIVISIBILITY_AGENTLESS_ENABLED=false", "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=1"}, MinTests: 2, RequireEndpoint: "/evp_proxy/v2/api/v2/citestcycle"},
	)
	return cases
}

// parityReceiver shares the actual MessagePack/coverage decoder with existing
// tests, but supplies composable settings instead of mutually exclusive profiles.
type parityReceiver struct {
	miniWireCapture
	policy   policySettings
	sideMu   sync.Mutex
	side     map[string][][]byte
	requests map[string]int
}

func (c *parityReceiver) handler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	c.sideMu.Lock()
	c.requests[path]++
	c.sideMu.Unlock()
	switch {
	case strings.HasSuffix(path, "/setting"):
		if c.policy.SettingsDelay > 0 {
			time.Sleep(c.policy.SettingsDelay)
		}
		if c.policy.SettingsFailure {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		p := c.policy
		attributes := map[string]any{"itr_enabled": p.ITR, "tests_skipping": p.ITR, "require_git": p.RequireGit, "code_coverage": p.Coverage, "coverage_report_upload_enabled": p.CoverageReport, "known_tests_enabled": p.EFD, "impacted_tests_enabled": p.Impacted, "flaky_test_retries_enabled": p.Retry,
			"early_flake_detection": map[string]any{"enabled": p.EFD, "faulty_session_threshold": faultyThreshold(p), "slow_test_retries": map[string]int{"5s": 2, "10s": 2, "30s": 2, "5m": 2}},
			"test_management":       map[string]any{"enabled": p.Disabled || p.Quarantined || p.AttemptToFix, "attempt_to_fix_retries": 2}}
		writePolicyJSON(w, map[string]any{"data": map[string]any{"id": "parity", "type": "ci_app_test_service_libraries_settings", "attributes": attributes}})
	case strings.HasSuffix(path, "/test-management/tests"):
		target := c.policy.ManagementTarget
		suite := "sample_test.go"
		if target == "" {
			target = "TestManaged"
		} else {
			suite = "parity_cases_test.go"
		}
		if c.policy.ManagementSuite != "" {
			suite = c.policy.ManagementSuite
		}
		properties := map[string]bool{"disabled": c.policy.Disabled, "quarantined": c.policy.Quarantined, "attempt_to_fix": c.policy.AttemptToFix}
		modules := map[string]any{"example.com/dd-ci-testing-fixture_test": map[string]any{"suites": map[string]any{suite: map[string]any{"tests": map[string]any{target: map[string]any{"properties": properties}}}}}}
		writePolicyJSON(w, map[string]any{"data": map[string]any{"id": "parity", "type": "ci_app_libraries_tests_request", "attributes": map[string]any{"modules": modules}}})
	case strings.HasSuffix(path, "/ci/libraries/tests"):
		names := []string{"KnownOtherTest"}
		if c.policy.Known {
			names = []string{"TestPass"}
		}
		writePolicyJSON(w, map[string]any{"data": map[string]any{"id": "parity", "type": "ci_app_libraries_tests", "attributes": map[string]any{"tests": map[string]any{"example.com/dd-ci-testing-fixture_test": map[string]any{"sample_test.go": names}}}}})
	case strings.HasSuffix(path, "/skippable"):
		var request struct {
			Data struct {
				Attributes struct {
					Configurations map[string]any `json:"configurations"`
				} `json:"attributes"`
			} `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			c.fail(err)
			w.WriteHeader(400)
			return
		}
		writePolicyJSON(w, map[string]any{"meta": map[string]any{"correlation_id": "parity"}, "data": []any{map[string]any{"id": "parity", "type": "test", "attributes": map[string]any{"suite": skippableSuite(c.policy), "name": skippableTarget(c.policy), "parameters": "", "_is_missing_line_code_coverage": c.policy.MissingLineCoverage, "configurations": request.Data.Attributes.Configurations}}}})
	case strings.HasSuffix(path, "/search_commits"):
		writePolicyJSON(w, map[string]any{"data": []any{}})
	case strings.HasSuffix(path, "/packfile"):
		_, err := io.Copy(io.Discard, io.LimitReader(r.Body, 16<<20))
		if err != nil {
			c.fail(err)
			w.WriteHeader(400)
			return
		}
		w.WriteHeader(202)
	case strings.HasSuffix(path, "/logs") || strings.HasSuffix(path, "/cicovreprt") || strings.Contains(path, "telemetry"):
		body, err := decodedRequestBody(r)
		if err != nil {
			c.fail(err)
			w.WriteHeader(400)
			return
		}
		if strings.HasSuffix(path, "/cicovreprt") {
			body, err = decodedCoverageReport(body, r.Header.Get("Content-Type"))
			if err != nil {
				c.fail(err)
				w.WriteHeader(400)
				return
			}
		}
		c.sideMu.Lock()
		c.side[path] = append(c.side[path], body)
		c.sideMu.Unlock()
		w.WriteHeader(202)
	default:
		c.miniWireCapture.handler(w, r)
	}
}
func skippableSuite(p policySettings) string {
	if p.SkippableSuite != "" {
		return p.SkippableSuite
	}
	return "sample_test.go"
}
func skippableTarget(p policySettings) string {
	if p.SkippableTarget != "" {
		return p.SkippableTarget
	}
	return "TestManaged"
}

func faultyThreshold(p policySettings) int {
	if p.FaultyThreshold != nil {
		return *p.FaultyThreshold
	}
	return 100
}

func writePolicyJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

type eventCounts struct {
	Sessions int `json:"sessions"`
	Modules  int `json:"modules"`
	Suites   int `json:"suites"`
	Tests    int `json:"tests"`
	Spans    int `json:"spans"`
}

func countCIEvents(events []map[string]any) (eventCounts, error) {
	var counts eventCounts
	for _, event := range events {
		switch event["type"] {
		case "test_session_end":
			counts.Sessions++
		case "test_module_end":
			counts.Modules++
		case "test_suite_end":
			counts.Suites++
		case "test":
			counts.Tests++
		case "span":
			counts.Spans++
		default:
			return counts, fmt.Errorf("unrecognized event type %v", event["type"])
		}
	}
	return counts, nil
}

// Every emitted test must refer to emitted hierarchy end events. Multiple
// suites/modules are validated independently; a last-seen ID cannot stand in
// for the whole hierarchy. Generic spans retain their distributed parentage.
func validateEventGraph(events []map[string]any) error {
	ends := map[string]map[uint64]map[string]any{}
	fields := map[string]string{"test_session_end": "test_session_id", "test_module_end": "test_module_id", "test_suite_end": "test_suite_id"}
	spanIDs := map[uint64]bool{}
	for _, event := range events {
		content, ok := event["content"].(map[string]any)
		if !ok {
			return fmt.Errorf("missing event content")
		}
		if field := fields[fmt.Sprint(event["type"])]; field != "" {
			id, ok := content[field].(uint64)
			if !ok || id == 0 {
				return fmt.Errorf("invalid %s", field)
			}
			if ends[field] == nil {
				ends[field] = map[uint64]map[string]any{}
			}
			if ends[field][id] != nil {
				return fmt.Errorf("duplicate %s end event", field)
			}
			ends[field][id] = content
			if content["trace_id"] != nil || content["span_id"] != nil || content["parent_id"] != nil {
				return fmt.Errorf("distributed IDs on hierarchy event")
			}
		} else {
			id, ok := content["span_id"].(uint64)
			if !ok || id == 0 || spanIDs[id] {
				return fmt.Errorf("invalid or duplicate span ID")
			}
			spanIDs[id] = true
			if content["trace_id"] == nil || content["trace_id"] == uint64(0) {
				return fmt.Errorf("missing distributed identity")
			}
		}
	}
	for _, event := range events {
		content := event["content"].(map[string]any)
		kind := fmt.Sprint(event["type"])
		required := []string{}
		switch kind {
		case "test":
			required = []string{"test_session_id", "test_module_id", "test_suite_id"}
		case "test_suite_end":
			required = []string{"test_session_id", "test_module_id"}
		case "test_module_end":
			required = []string{"test_session_id"}
		}
		for _, field := range required {
			id, ok := content[field].(uint64)
			parent := ends[field][id]
			if !ok || parent == nil {
				return fmt.Errorf("%s has unresolved %s", kind, field)
			}
			if field == "test_suite_id" && (parent["test_module_id"] != content["test_module_id"] || parent["test_session_id"] != content["test_session_id"]) {
				return fmt.Errorf("test crossed suite ancestry")
			}
			if field == "test_module_id" && parent["test_session_id"] != content["test_session_id"] {
				return fmt.Errorf("event crossed module ancestry")
			}
		}
	}
	return nil
}

// These observations include initialization, settings, test/retry execution and
// final flush. Compilation, receiver setup and comparisons are not timed.
const binaryTimingScope = "prebuilt binary: startup, settings, execution and shutdown/flush"

type parityTiming struct {
	Scope      string `json:"scope"`
	SDKWallNS  int64  `json:"sdk_wall_ns"`
	MiniWallNS int64  `json:"mini_wall_ns"`
}

type parityResult struct {
	Scenario string       `json:"scenario"`
	Features []string     `json:"features"`
	Status   string       `json:"status"`
	SDK      eventCounts  `json:"sdk"`
	Mini     eventCounts  `json:"mini"`
	SDKExit  int          `json:"sdk_exit"`
	MiniExit int          `json:"mini_exit"`
	Timing   parityTiming `json:"timing"`
}

func runParityCase(t *testing.T, dir, bin string, tc parityCase) (*parityReceiver, execution) {
	t.Helper()
	receiver := &parityReceiver{policy: tc.Policy, side: map[string][][]byte{}, requests: map[string]int{}}
	server := httptest.NewServer(http.HandlerFunc(receiver.handler))
	defer server.Close()
	// The temporary directory is separate from the cache and the retry counter,
	// so a run's leftovers there can be checked.
	scratch, tmp := t.TempDir(), t.TempDir()
	env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=fixture", "TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp, "XDG_CACHE_HOME="+scratch, "POC_RETRY_COUNTER="+filepath.Join(scratch, "attempt"), "DD_TEST_MANAGEMENT_ATTEMPT_TO_FIX_RETRIES=2", "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_MAX_RETRIES=2")
	if tc.Coverage {
		env = append(env, "DD_CIVISIBILITY_CODE_COVERAGE_ENABLED=true")
	}
	if tc.Logs {
		env = append(env, "DD_CIVISIBILITY_LOGS_ENABLED=true")
	}
	env = append(env, tc.Env...)
	out, stderr, code, wall := commandWithTiming(t, dir, env, bin, tc.Args...)
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	if len(receiver.failures) != 0 {
		t.Fatal(receiver.failures)
	}
	return receiver, execution{code: code, out: normalizedOutput(out), stderr: stderr, wireEvents: receiver.events, wall: wall, leftovers: directoryEntries(t, tmp)}
}

// directoryEntries lists the names in dir, which must exist.
func directoryEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// requireNoMiniLeftovers enforces that Mini leaves nothing in the temporary
// directory: no traces outside the CI payloads it sends.
func requireNoMiniLeftovers(t *testing.T, mini execution) {
	t.Helper()
	if len(mini.leftovers) != 0 {
		t.Fatalf("Mini left temporary files: %v", mini.leftovers)
	}
}

// The report contains counts, scenario outcomes and process walltimes, not raw logs or credentials.
// CI uploads one file per invocation; race and non-race runs use separate paths.
func writeParityReport(t *testing.T, results []parityResult, block *parityTiming, order []string) {
	t.Helper()
	path := os.Getenv("PARITY_REPORT_PATH")
	if path == "" {
		return
	}
	if strings.Contains(t.Name(), "DeferredDelivery") {
		path = strings.TrimSuffix(path, filepath.Ext(path)) + "-deferred.json"
	}
	schema := 4
	data, err := json.MarshalIndent(struct {
		SchemaVersion      int            `json:"schema_version"`
		SDKVersion         string         `json:"sdk_version"`
		SDKCommit          string         `json:"sdk_commit"`
		Go                 string         `json:"go"`
		OS                 string         `json:"os"`
		Architecture       string         `json:"architecture"`
		SDKInstrumentation string         `json:"sdk_instrumentation"`
		Scenarios          []parityResult `json:"scenarios"`
		ExecutionBlock     *parityTiming  `json:"execution_block,omitempty"`
		ExecutionOrder     []string       `json:"execution_order,omitempty"`
	}{schema, sdkVersion, version.SDKCommit, runtime.Version(), runtime.GOOS, runtime.GOARCH, sdkOracleName(), results, block, order}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
}

func sdkOracleName() string {
	if os.Getenv("ORCHESTRION_BIN") != "" {
		return "orchestrion"
	}
	return "poc-sdk (Orchestrion reference not supplied)"
}

func TestCIVisibilityParityMatrix(t *testing.T) {
	runCIVisibilityParityMatrix(t, false)
}

// Reuse the SDK oracle and strict wire/coverage comparisons for the delivery
// mode. These cases cover the scheduling and shutdown paths it changes.
func TestDeferredDeliveryParityMatrix(t *testing.T) {
	runCIVisibilityParityMatrix(t, true)
}

func buildTestingParityFixture(t *testing.T, tempDir func() string) *parityFixture {
	t.Helper()
	dir, driver := prepareMiniFixtureWithTempDir(t, tempDir)
	reference := os.Getenv("ORCHESTRION_BIN")
	if reference != "" {
		var err error
		reference, err = filepath.Abs(reference)
		if err != nil {
			t.Fatal(err)
		}
		configureReferenceFixture(t, dir)
	}
	source := `package fixture_test

import (
	"os"
	"testing"
)

func TestParityOtherSuite(t *testing.T) { t.Log("second suite") }
func TestParityPolicy(t *testing.T) {
	t.Run("child", func(t *testing.T) { t.Error("managed child failure") })
}
func TestParityParallelRetry(t *testing.T) {
	t.Parallel()
	file, err := os.OpenFile(os.Getenv("POC_RETRY_COUNTER"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		file.Close()
		t.Error("parallel first attempt fails")
	} else if !os.IsExist(err) {
		t.Fatal(err)
	}
}

//dd:test.unskippable
func TestParityUnskippable(t *testing.T) { t.Log("must run") }
`
	if err := os.WriteFile(filepath.Join(dir, "parity_cases_test.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	base, head := prepareParityGit(t, dir)
	bins, builds := miniPairBuilds(dir, driver, tempDir, "-cover", "-covermode=atomic", "-coverpkg=./...")
	oracle := bins[0]
	if reference != "" {
		oracle = filepath.Join(tempDir(), executableName("fixture.test"))
		builds = append(builds, fixtureBuild{name: "SDK oracle compile", dir: dir, tool: reference, env: testEnv("DD_CIVISIBILITY_ENABLED=false"), args: []string{"go", "test", "-cover", "-covermode=atomic", "-coverpkg=./...", "-c", "-o", oracle, "."}})
	}
	buildConcurrently(t, builds...)
	return &parityFixture{dir: dir, sdk: bins[0], mini: bins[1], oracle: oracle, base: base, head: head}
}

func runCIVisibilityParityMatrix(t *testing.T, deferred bool) {
	// Delivery mode is a runtime setting. Both matrices use the same covered
	// binaries while keeping receivers, retry counters and processes independent.
	fixture := sharedTestingParity.get(t, "testing", buildTestingParityFixture)
	dir, oracle := fixture.dir, fixture.oracle
	cases := parityCases()
	if deferred {
		selected := map[string]bool{
			"pass": true, "nested-cleanup-context": true, "parallel-count-shuffle": true,
			"empty-selection": true, "list": true,
			"error-Fatal": true, "coverage-parallel-shuffle": true, "coverage-report": true,
			"logs-atr": true, "atr-coverage-in_process": true, "atr-coverage-process": true,
			"atr-parallel-in_process": true, "atr-parallel-process": true,
			"efd-parallel-execution": true, "benchmarks": true, "git-upload-require-git": true,
		}
		kept := cases[:0]
		for _, tc := range cases {
			if selected[tc.Name] {
				tc.Env = append(tc.Env, "DD_CIVISIBILITY_DEFERRED_DELIVERY=true")
				tc.Features = append(tc.Features, "deferred-delivery")
				kept = append(kept, tc)
			}
		}
		cases = kept
	}
	for i := range cases {
		if cases[i].Git {
			cases[i].Env = append(cases[i].Env, "DD_GIT_COMMIT_SHA="+fixture.head, "DD_GIT_PULL_REQUEST_BASE_BRANCH_SHA="+fixture.base)
		}
	}
	var results []parityResult
	var block *parityTiming
	var order []string
	defer func() { writeParityReport(t, results, block, order) }()
	if mode := os.Getenv("PARITY_EXECUTION_ORDER"); mode != "" {
		switch mode {
		case "sdk-first":
			order = []string{"sdk", "mini"}
		case "mini-first":
			order = []string{"mini", "sdk"}
		default:
			t.Fatal("PARITY_EXECUTION_ORDER must be sdk-first or mini-first")
		}
		results, block = runGroupedParityCases(t, dir, oracle, fixture.mini, cases, order)
		return
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			row := parityResult{Scenario: tc.Name, Features: tc.Features, Status: "failed"}
			defer func() { results = append(results, row) }()
			want, sdk := runParityCase(t, dir, oracle, tc)
			got, mini := runParityCase(t, dir, fixture.mini, tc)
			requireNoMiniLeftovers(t, mini)
			row = assertParityCase(t, tc, want, got, sdk, mini)
		})
	}
}

func assertParityCase(t *testing.T, tc parityCase, want, got *parityReceiver, sdk, mini execution) parityResult {
	t.Helper()
	row := parityResult{Scenario: tc.Name, Features: tc.Features, Status: "failed"}
	row.SDKExit, row.MiniExit = sdk.code, mini.code
	row.Timing = parityTiming{binaryTimingScope, sdk.wall.Nanoseconds(), mini.wall.Nanoseconds()}
	var err error
	row.SDK, err = countCIEvents(want.events)
	if err != nil {
		t.Fatal(err)
	}
	row.Mini, err = countCIEvents(got.events)
	if err != nil {
		t.Fatal(err)
	}
	if row.SDK != row.Mini || sdk.code != mini.code {
		t.Fatalf("counts/exit differ: %+v\nSDK:%s\nMini:%s", row, sdk.stderr, mini.stderr)
	}
	if sdk.code != tc.WantExit || row.SDK.Tests < tc.MinTests || row.SDK.Sessions != 1 {
		t.Fatalf("scenario not exercised: %+v; want exit %d and >=%d tests\n%s", row, tc.WantExit, tc.MinTests, sdk.stderr)
	}
	for _, receiver := range []*parityReceiver{want, got} {
		if err := validateEventGraph(receiver.events); err != nil {
			t.Fatal(err)
		}
		if tc.RequireEndpoint != "" {
			found := false
			for path, n := range receiver.requests {
				found = found || strings.HasSuffix(path, tc.RequireEndpoint) && n > 0
			}
			if !found {
				t.Fatalf("missing %s request: %v", tc.RequireEndpoint, receiver.requests)
			}
		}
		if tc.RequiredTag != "" {
			found := false
			for _, event := range receiver.events {
				content := event["content"].(map[string]any)
				meta, _ := content["meta"].(map[string]any)
				kind := tc.RequiredEvent
				if kind == "" {
					kind = "test"
				}
				found = found || event["type"] == kind && meta[tc.RequiredTag] == tc.RequiredValue
			}
			if !found {
				t.Fatalf("scenario never emitted %s=%s", tc.RequiredTag, tc.RequiredValue)
			}
		}
		if tc.Logs || tc.Policy.CoverageReport {
			endpoint := "/logs"
			if tc.Policy.CoverageReport {
				endpoint = "/cicovreprt"
			}
			found := false
			for path, bodies := range receiver.side {
				found = found || strings.HasSuffix(path, endpoint) && len(bodies) > 0
			}
			if !found {
				t.Fatalf("no %s upload: %v\n%s", endpoint, receiver.requests, sdk.stderr)
			}
		}
	}
	assertMiniCIAttributes(t, comparableOracleEvents(t, want.events), comparableOracleEvents(t, got.events))
	assertSidePayloadParity(t, want, got)
	if len(want.payloads) > 0 && ciWireMetadata(t, &want.miniWireCapture) != ciWireMetadata(t, &got.miniWireCapture) {
		t.Fatal("envelope metadata differs")
	}
	if tc.Coverage {
		a, b := normalizedMiniCoverage(t, &want.miniWireCapture), normalizedMiniCoverage(t, &got.miniWireCapture)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("coverage differs: SDK %v Mini %v", a, b)
		}
	}
	row.Status = "passed"
	t.Logf("SDK = Mini: sessions=%d modules=%d suites=%d tests=%d spans=%d", row.SDK.Sessions, row.SDK.Modules, row.SDK.Suites, row.SDK.Tests, row.SDK.Spans)
	return row
}

// Group each runtime into one continuous execution block. Compile and compare
// outside these timers; do not approximate a block by summing child timings.
func runGroupedParityCases(t *testing.T, dir, sdkBin, miniBin string, cases []parityCase, order []string) ([]parityResult, *parityTiming) {
	t.Helper()
	type observation struct {
		receiver *parityReceiver
		result   execution
	}
	sdk := make([]observation, len(cases))
	mini := make([]observation, len(cases))
	block := &parityTiming{Scope: fmt.Sprintf("%d-scenario continuous block: receiver setup, process execution and shutdown/flush; excludes compilation and comparisons", len(cases))}
	for _, backend := range order {
		bin, observations := sdkBin, sdk
		if backend == "mini" {
			bin, observations = miniBin, mini
		}
		start := time.Now()
		ok := t.Run("execute-"+backend, func(t *testing.T) {
			for i, tc := range cases {
				t.Run(tc.Name, func(t *testing.T) {
					observations[i].receiver, observations[i].result = runParityCase(t, dir, bin, tc)
				})
			}
		})
		wall := time.Since(start).Nanoseconds()
		if backend == "sdk" {
			block.SDKWallNS = wall
		} else {
			block.MiniWallNS = wall
		}
		if !ok {
			return nil, block
		}
	}
	var results []parityResult
	for i, tc := range cases {
		t.Run("compare-"+tc.Name, func(t *testing.T) {
			row := parityResult{Scenario: tc.Name, Features: tc.Features, Status: "failed"}
			defer func() { results = append(results, row) }()
			requireNoMiniLeftovers(t, mini[i].result)
			row = assertParityCase(t, tc, sdk[i].receiver, mini[i].receiver, sdk[i].result, mini[i].result)
		})
	}
	return results, block
}

func TestParityGraphRejectsLostEvents(t *testing.T) {
	event := func(kind string, content map[string]any) map[string]any {
		return map[string]any{"type": kind, "content": content}
	}
	complete := []map[string]any{
		event("test_session_end", map[string]any{"test_session_id": uint64(1)}),
		event("test_module_end", map[string]any{"test_session_id": uint64(1), "test_module_id": uint64(2)}),
		event("test_suite_end", map[string]any{"test_session_id": uint64(1), "test_module_id": uint64(2), "test_suite_id": uint64(3)}),
		event("test", map[string]any{"trace_id": uint64(4), "span_id": uint64(5), "test_session_id": uint64(1), "test_module_id": uint64(2), "test_suite_id": uint64(3)}),
	}
	if err := validateEventGraph(complete); err != nil {
		t.Fatal(err)
	}
	for missing := 0; missing < 3; missing++ {
		broken := append(append([]map[string]any{}, complete[:missing]...), complete[missing+1:]...)
		if validateEventGraph(broken) == nil {
			t.Fatalf("missing end event %d accepted", missing)
		}
	}
	if validateEventGraph(append(complete, complete[3])) == nil {
		t.Fatal("duplicate test accepted")
	}
	a, _ := countCIEvents(complete)
	b, _ := countCIEvents(complete[:3])
	if a == b {
		t.Fatal("lost test count hidden")
	}
	// Multiplicity is retained by the attribute comparator too.
	x := ciWireEvents(complete)
	y := ciWireEvents(append(complete, complete[3]))
	sort.Strings(x)
	sort.Strings(y)
	if reflect.DeepEqual(x, y) {
		t.Fatal("comparator dropped multiplicity")
	}
}
