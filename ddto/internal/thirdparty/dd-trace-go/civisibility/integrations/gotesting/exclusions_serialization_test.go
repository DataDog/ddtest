package gotesting

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestExcludedProcessDiagnosticsFitWireBudget(t *testing.T) {
	cfg := processRetryChildConfig{TestName: "Root", Attempt: 1, RetryReason: "retry", ExclusionPolicy: &processRetrySubtreeConfig{Version: processRetrySubtreeVersion, SelectedRoot: "Root", Root: processRetrySubtreeDirective{TestName: "Root", ModuleName: "fixture", SuiteName: "fixture_test.go"}}}
	result := processRetryResult{Version: 1, TestName: cfg.TestName, Attempt: cfg.Attempt, RetryReason: cfg.RetryReason, Status: processRetryStatusPass, StartUnixNano: 1, FinishUnixNano: 2, DurationNanos: 1}
	output := strings.Repeat("l", 4*1024)
	for i := 0; i < 4096; i++ {
		result.ExcludedTests = append(result.ExcludedTests, processRetrySubtreeResult{TestName: fmt.Sprintf("Root/row-%04d", i), ModuleName: "fixture", SuiteName: "fixture_test.go", Exclusions: TestExclusions{NoAutoRetry: true}, Status: processRetryStatusPass, StartUnixNano: 1, FinishUnixNano: 2, DurationNanos: 1, OutputTail: output})
	}
	if err := validateProcessRetryResult(result, cfg); err != nil {
		t.Fatalf("invalid source fixture: %v", err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= processRetryWireMaxBytes(true) {
		t.Fatalf("fixture did not exceed wire budget: %d", len(raw))
	}
	t.Logf("excluded records=%d unboundedbytes=%d limit=%d", len(result.ExcludedTests), len(raw), processRetryWireMaxBytes(true))
	payload, err := marshalProcessRetryResult(result)
	if err != nil {
		t.Fatalf("valid excluded executions must trim optional diagnostics: %v", err)
	}
	if len(payload) > processRetryWireMaxBytes(true) {
		t.Fatalf("wire budget exceeded: %d", len(payload))
	}
	var bounded processRetryResult
	if err := json.Unmarshal(payload, &bounded); err != nil {
		t.Fatal(err)
	}
	if err := validateProcessRetryResult(bounded, cfg); err != nil {
		t.Fatalf("bounded result invalid: %v", err)
	}
	if bounded.Status != processRetryStatusPass || bounded.ResultError != "" || len(bounded.ExcludedTests) != 4096 {
		t.Fatalf("required results lost: status=%s error=%s records=%d", bounded.Status, bounded.ResultError, len(bounded.ExcludedTests))
	}
	for i, row := range bounded.ExcludedTests {
		original := result.ExcludedTests[i]
		if row.TestName != original.TestName || row.Status != original.Status || row.Exclusions != original.Exclusions {
			t.Fatalf("required record%d changed: %v", i, row)
		}
		if !row.OutputTruncated || len(row.OutputTail) >= len(output) {
			t.Fatalf("optional row%d diagnostics were not bounded", i)
		}
		if original.OutputTail != output || original.OutputTruncated {
			t.Fatalf("caller row%d mutated", i)
		}
	}
	mixedCfg := cfg
	mixedCfg.Subtree = cfg.ExclusionPolicy
	withSubtree := func(value processRetryResult) processRetryResult {
		value.ModuleName, value.SuiteName = "fixture", "fixture_test.go"
		value.Subtests = []processRetrySubtreeResult{{TestName: "Root/ordinary", ModuleName: "fixture", SuiteName: "fixture_test.go", Status: processRetryStatusPass, StartUnixNano: 1, FinishUnixNano: 2, DurationNanos: 1}}
		return value
	}
	mixed := withSubtree(result)
	if err := validateProcessRetryResult(mixed, mixedCfg); err != nil {
		t.Fatalf("invalid mixed fixture: %v", err)
	}
	mixedPayload, err := marshalProcessRetryResult(mixed)
	if err != nil {
		t.Fatalf("mixed diagnostics must fit without dropping records: %v", err)
	}
	var mixedBounded processRetryResult
	if err := json.Unmarshal(mixedPayload, &mixedBounded); err != nil {
		t.Fatal(err)
	}
	if err := validateProcessRetryResult(mixedBounded, mixedCfg); err != nil {
		t.Fatalf("invalid bounded mixed result: %v", err)
	}
	if len(mixedPayload) > processRetryWireMaxBytes(true) || len(mixedBounded.ExcludedTests) != 4096 || len(mixedBounded.Subtests) != 1 || mixedBounded.Status != processRetryStatusPass || mixedBounded.ResultError != "" {
		t.Fatalf("mixed records changed: excluded=%d subtree=%d status=%s error=%s", len(mixedBounded.ExcludedTests), len(mixedBounded.Subtests), mixedBounded.Status, mixedBounded.ResultError)
	}
	for i, row := range mixedBounded.ExcludedTests {
		if row.TestName != result.ExcludedTests[i].TestName || !row.OutputTruncated || row.Status != processRetryStatusPass || result.ExcludedTests[i].OutputTail != output {
			t.Fatalf("mixed row%d changed required data or caller diagnostics", i)
		}
	}
	// Required identity data can exceed the budget even while every field fits
	// its individual bound. Never drop execution records to manufacture success.
	for i := range result.ExcludedTests {
		result.ExcludedTests[i].ModuleName = strings.Repeat("m", processRetryErrorMessageMaxBytes-2)
		result.ExcludedTests[i].OutputTail = ""
	}
	if err := validateProcessRetryResult(result, cfg); err != nil {
		t.Fatalf("invalid required-data fixture: %v", err)
	}
	if payload, err := marshalProcessRetryResult(result); err == nil {
		t.Fatalf("oversized required records were not rejected: returnedbytes=%d", len(payload))
	}
	requiredMixed := withSubtree(result)
	if err := validateProcessRetryResult(requiredMixed, mixedCfg); err != nil {
		t.Fatalf("invalid required mixed fixture: %v", err)
	}
	if payload, err := marshalProcessRetryResult(requiredMixed); err == nil {
		t.Fatalf("oversized mixed required records were dropped: returnedbytes=%d", len(payload))
	}
	ordinary := processRetryResult{Version: 1, TestName: strings.Repeat("r", processRetryResultMaxBytes), Status: processRetryStatusPass}
	if payload, err := marshalProcessRetryResult(ordinary); err == nil {
		t.Fatalf("ordinary64KiB budget expanded: returnedbytes=%d", len(payload))
	}
}
