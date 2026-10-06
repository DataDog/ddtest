package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer"
)

// Capabilities and ITR correlation are inherited session attributes. SDK
// bootstrap discovers them asynchronously, so its session span can precede
// them while all test events carry them. Normalize only missing session values
// from consistent test attributes; retain conflicts and every test attribute.
func sessionCITags(events []map[string]any) map[string]map[string]any {
	result := map[string]map[string]any{}
	for _, event := range events {
		if event["type"] != "test" {
			continue
		}
		content := event["content"].(map[string]any)
		id := fmt.Sprint(content["test_session_id"])
		if id == "<nil>" || id == "0" {
			continue
		}
		meta, _ := content["meta"].(map[string]any)
		values := map[string]any{}
		for key, value := range meta {
			if strings.HasPrefix(key, "_dd.library_capabilities.") || key == "itr_correlation_id" {
				values[key] = value
			}
		}
		if existing, ok := result[id]; ok {
			for key, value := range existing {
				if other, present := values[key]; !present || !reflect.DeepEqual(value, other) {
					delete(existing, key)
				}
			}
		} else {
			result[id] = values
		}
	}
	return result
}

// Compare all event attributes except known APM enrichment and process-local
// identities/timings. CI attributes (including _dd.test.*) are never excluded.
func ciWireEvents(events []map[string]any) []string {
	inherited := sessionCITags(events)
	ignoredMeta := map[string]bool{"runtime-id": true, "language": true, "_dd.p.tid": true, "_dd.p.dm": true, "_dd.base_service": true, "_dd.trace_span_attribute_schema": true, "_dd.tags.process": true, "_dd.git.commit.sha": true, "_dd.git.repository_url": true, "test_session_id": true, "test_module_id": true, "test_suite_id": true}
	ignoredMetrics := map[string]bool{"process_id": true, "_sampling_priority_v1": true, "_dd.top_level": true, "_dd.agent_psr": true, "_dd.rule_psr": true, "_dd.limit_psr": true, "_dd.tracer_kr": true, "_dd.profiling.enabled": true, "_dd.trace_span_attribute_schema": true}
	var out []string
	for _, event := range events {
		c := event["content"].(map[string]any)
		row := map[string]any{"event_type": event["type"], "version": event["version"]}
		for _, key := range []string{"name", "service", "resource", "type", "error", "itr_correlation_id"} {
			row[key] = c[key]
		}
		for _, field := range []string{"meta", "metrics"} {
			attrs := map[string]any{}
			if values, ok := c[field].(map[string]any); ok {
				for k, v := range values {
					if field == "meta" && (ignoredMeta[k] || k == "test.execution.order") || field == "metrics" && ignoredMetrics[k] {
						continue
					}
					if k == "error.stack" {
						v = canonicalMiniStack(v.(string))
					}
					attrs[k] = v
				}
			}

			if field == "meta" && event["type"] == "test_session_end" {
				for key, value := range inherited[fmt.Sprint(c["test_session_id"])] {
					if _, present := attrs[key]; !present {
						attrs[key] = value
					}
				}
			}
			row[field] = attrs
		}
		raw, _ := json.Marshal(row)
		out = append(out, string(raw))
	}
	sort.Strings(out)
	return out
}

// Duplicate APM representations can be omitted only when they agree with CI.
func validateCIFieldDuplicates(t *testing.T, events []map[string]any) {
	t.Helper()
	for _, event := range events {
		content := event["content"].(map[string]any)
		meta, _ := content["meta"].(map[string]any)
		for _, key := range []string{"test_session_id", "test_module_id", "test_suite_id"} {
			if value, ok := meta[key]; ok {
				id, err := strconv.ParseUint(fmt.Sprint(value), 10, 64)
				if err != nil || fmt.Sprint(content[key]) != strconv.FormatUint(id, 10) {
					t.Fatalf("inconsistent CI identity %s: meta=%v native=%v", key, value, content[key])
				}
			}
		}
		for alias, key := range map[string]string{"_dd.git.commit.sha": "git.commit.sha", "_dd.git.repository_url": "git.repository_url"} {
			if value, ok := meta[alias]; ok && meta[key] != nil && value != meta[key] {
				t.Fatalf("inconsistent Git metadata %s: %v/%v", alias, value, meta[key])
			}
		}
	}
}
func assertMiniCIAttributes(t *testing.T, want, got []map[string]any) {
	t.Helper()
	validateCIFieldDuplicates(t, want)
	validateCIFieldDuplicates(t, got)
	a, b, err := comparableCIWireEvents(want, got)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("CI attributes SDK %v MINI %v", a, b)
	}
}

// Sessions without tests cannot inherit capabilities from test events.
// Validate Mini's complete set against the frozen SDK's declarations, and allow
// only missing SDK session values. Every other wire attribute stays unchanged.
func sessionOnlyCIWireEvents(events []map[string]any, allowMissingCapabilities bool) ([]string, error) {
	// Frozen SDK capability declarations; see the dd-trace-go source manifest.
	capabilities := map[string]string{
		"_dd.library_capabilities.early_flake_detection":          "1",
		"_dd.library_capabilities.auto_test_retries":              "1",
		"_dd.library_capabilities.coverage_report_upload":         "1",
		"_dd.library_capabilities.test_impact_analysis":           "1",
		"_dd.library_capabilities.test_management.quarantine":     "1",
		"_dd.library_capabilities.test_management.disable":        "1",
		"_dd.library_capabilities.test_management.attempt_to_fix": "5",
	}
	for _, event := range events {
		if event["type"] != "test_session_end" {
			return nil, fmt.Errorf("session-only execution emitted %v instead of only a session", event["type"])
		}
	}
	rows := ciWireEvents(events)
	for i, encoded := range rows {
		var row map[string]any
		if err := json.Unmarshal([]byte(encoded), &row); err != nil {
			return nil, err
		}
		meta := row["meta"].(map[string]any)
		for key, value := range meta {
			if strings.HasPrefix(key, "_dd.library_capabilities.") {
				if expected, ok := capabilities[key]; !ok || value != expected {
					return nil, fmt.Errorf("unexpected session-only capability %s=%v", key, value)
				}
			}
		}
		for key, value := range capabilities {
			if _, present := meta[key]; !present {
				if !allowMissingCapabilities {
					return nil, fmt.Errorf("missing Mini session-only capability %s", key)
				}
				meta[key] = value
			}
		}
		data, err := json.Marshal(row)
		if err != nil {
			return nil, err
		}
		rows[i] = string(data)
	}
	sort.Strings(rows)
	return rows, nil
}

func hasOnlyCISessions(events []map[string]any) bool {
	if len(events) == 0 {
		return false
	}
	for _, event := range events {
		if event["type"] != "test_session_end" {
			return false
		}
	}
	return true
}

func comparableCIWireEvents(want, got []map[string]any) ([]string, []string, error) {
	if hasOnlyCISessions(want) && hasOnlyCISessions(got) {
		a, err := sessionOnlyCIWireEvents(want, true)
		if err != nil {
			return nil, nil, err
		}
		b, err := sessionOnlyCIWireEvents(got, false)
		return a, b, err
	}
	return ciWireEvents(want), ciWireEvents(got), nil
}

func TestCIComparatorSessionOnlySelection(t *testing.T) {
	session := func(meta map[string]any) []map[string]any {
		return []map[string]any{{"type": "test_session_end", "version": uint64(1), "content": map[string]any{"test_session_id": uint64(1), "meta": meta}}}
	}
	sdk := session(map[string]any{})
	rows, err := sessionOnlyCIWireEvents(sdk, true)
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(rows[0]), &row); err != nil {
		t.Fatal(err)
	}
	mini := session(row["meta"].(map[string]any))
	a, b, err := comparableCIWireEvents(sdk, mini)
	if err != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("session-only normalization: %v %v %v", a, b, err)
	}
	if _, _, err := comparableCIWireEvents(sdk, sdk); err == nil {
		t.Fatal("missing Mini session capabilities masked")
	}
	testEvent := map[string]any{"type": "test", "version": uint64(2), "content": map[string]any{"test_session_id": uint64(1), "meta": map[string]any{}}}
	for _, sdkHasTest := range []bool{false, true} {
		want, got := sdk, mini
		if sdkHasTest {
			want = append(want, testEvent)
		} else {
			got = append(got, testEvent)
		}
		a, b, err := comparableCIWireEvents(want, got)
		if err != nil || reflect.DeepEqual(a, b) {
			t.Fatalf("missing test events masked: %v %v %v", a, b, err)
		}
	}
}

func TestCIComparatorSessionOnlyCapabilities(t *testing.T) {
	event := func(meta map[string]any) []map[string]any {
		return []map[string]any{{"type": "test_session_end", "version": uint64(1), "content": map[string]any{"meta": meta}}}
	}
	baseline, err := sessionOnlyCIWireEvents(event(map[string]any{}), true)
	if err != nil {
		t.Fatal(err)
	}
	var normalized map[string]any
	if err := json.Unmarshal([]byte(baseline[0]), &normalized); err != nil {
		t.Fatal(err)
	}
	meta := normalized["meta"].(map[string]any)
	if len(meta) != 7 || meta["_dd.library_capabilities.test_management.attempt_to_fix"] != "5" {
		t.Fatalf("unexpected SDK capability declaration: %v", meta)
	}
	complete, err := sessionOnlyCIWireEvents(event(meta), false)
	if err != nil || !reflect.DeepEqual(complete, baseline) {
		t.Fatalf("complete Mini capabilities: %v %v", complete, err)
	}
	for key := range meta {
		for _, mode := range []string{"missing", "wrong"} {
			candidate := make(map[string]any)
			for k, v := range meta {
				candidate[k] = v
			}
			if mode == "missing" {
				delete(candidate, key)
			} else {
				candidate[key] = "wrong"
			}
			if _, err := sessionOnlyCIWireEvents(event(candidate), false); err == nil {
				t.Fatalf("Mini %s capability accepted: %s", mode, key)
			}
			if mode == "wrong" {
				if _, err := sessionOnlyCIWireEvents(event(candidate), true); err == nil {
					t.Fatalf("wrong SDK capability accepted: %s", key)
				}
			}
		}
	}
	for _, key := range []string{"custom", "itr_correlation_id", "test.status"} {
		changed, err := sessionOnlyCIWireEvents(event(map[string]any{key: "different"}), true)
		if err != nil || reflect.DeepEqual(changed, baseline) {
			t.Fatalf("listing CI attribute masked: %s", key)
		}
	}
	if _, err := sessionOnlyCIWireEvents(event(map[string]any{"_dd.library_capabilities.unknown": "1"}), true); err == nil {
		t.Fatal("unknown capability accepted")
	}
	tests := event(map[string]any{})
	tests[0]["type"] = "test"
	if _, err := sessionOnlyCIWireEvents(tests, true); err == nil {
		t.Fatal("listing test event accepted")
	}
}

func TestCIComparatorRetainsProductAttributes(t *testing.T) {
	event := func(meta map[string]any, metrics map[string]any) []map[string]any {
		return []map[string]any{{"type": "test", "version": uint64(2), "content": map[string]any{"meta": meta, "metrics": metrics}}}
	}
	base := ciWireEvents(event(map[string]any{}, map[string]any{}))
	changedType := event(map[string]any{}, map[string]any{})
	changedType[0]["type"] = "test_session_end"
	if reflect.DeepEqual(base, ciWireEvents(changedType)) {
		t.Fatal("CI event envelope type excluded")
	}
	for _, key := range []string{"version", "test.status", "test.session", "_dd.library_capabilities.auto_test_retries", "git.commit.sha", "custom"} {
		if reflect.DeepEqual(base, ciWireEvents(event(map[string]any{key: "present"}, map[string]any{}))) {
			t.Fatalf("CI attribute excluded: %s", key)
		}
	}
	if reflect.DeepEqual(base, ciWireEvents(event(map[string]any{}, map[string]any{"_dd.host.vcpu_count": 8}))) {
		t.Fatal("CI metric excluded")
	}
	if !reflect.DeepEqual(base, ciWireEvents(event(map[string]any{"_dd.p.dm": "-0"}, map[string]any{"_dd.profiling.enabled": 1}))) {
		t.Fatal("APM enrichment retained")
	}
}

func TestCIComparatorSessionInheritance(t *testing.T) {
	key := "_dd.library_capabilities.auto_test_retries"
	events := func(sessionValue, testValue string) []map[string]any {
		sessionMeta, testMeta := map[string]any{}, map[string]any{}
		if sessionValue != "" {
			sessionMeta[key] = sessionValue
			sessionMeta["itr_correlation_id"] = sessionValue
		}
		if testValue != "" {
			testMeta[key] = testValue
			testMeta["itr_correlation_id"] = testValue
		}
		return []map[string]any{
			{"type": "test_session_end", "version": uint64(1), "content": map[string]any{"test_session_id": uint64(1), "meta": sessionMeta}},
			{"type": "test", "version": uint64(2), "content": map[string]any{"test_session_id": uint64(1), "meta": testMeta}},
		}
	}
	baseline := ciWireEvents(events("1", "1"))
	if !reflect.DeepEqual(baseline, ciWireEvents(events("", "1"))) {
		t.Fatal("asynchronous session inheritance differs")
	}
	for _, candidate := range [][]map[string]any{events("wrong", "1"), events("1", ""), events("unexpected", "")} {
		if reflect.DeepEqual(baseline, ciWireEvents(candidate)) {
			t.Fatal("conflicting or missing CI value masked")
		}
	}
	other := events("", "1")
	other[0]["content"].(map[string]any)["test_session_id"] = uint64(2)
	if reflect.DeepEqual(baseline, ciWireEvents(other)) {
		t.Fatal("inheritance crossed session identities")
	}
}
func ciWireMetadata(t *testing.T, c *miniWireCapture) string {
	t.Helper()
	validateCIFieldDuplicates(t, c.events)
	var expected string
	for _, payload := range c.payloads {
		metadata := payload["metadata"].(map[string]any)
		copy := map[string]any{}
		for kind, value := range metadata {
			attrs := map[string]any{}
			for key, v := range value.(map[string]any) {
				if isSharedCIKind(kind) && isLiftedCIKey(key) {
					continue // Compared after per-event inheritance, not discarded.
				}
				if kind == "*" && (key == "runtime-id" || key == "library_version") {
					if v == "" {
						t.Fatalf("missing %s", key)
					}
					continue
				}
				attrs[key] = v
			}
			if len(attrs) != 0 {
				copy[kind] = attrs
			}
		}
		raw, _ := json.Marshal(copy)
		if expected != "" && expected != string(raw) {
			t.Fatal("metadata differs across batches")
		}
		expected = string(raw)
	}
	if expected == "" {
		t.Fatal("no metadata")
	}
	return expected
}

func isLiftedCIKey(key string) bool { return minitracer.IsSharedCITag(key) }

func isSharedCIKind(kind string) bool {
	switch kind {
	case "test", "test_session_end", "test_module_end", "test_suite_end":
		return true
	}
	return false
}
func runCIWireCase(t *testing.T, dir, bin string, args, overrides []string, unsetSession, retry bool) *miniWireCapture {
	t.Helper()
	c := &miniWireCapture{}
	c.profile = "coverage"
	c.retry = retry
	server := httptest.NewServer(http.HandlerFunc(c.handler))
	defer server.Close()
	scratch := t.TempDir()
	env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=fixture", "TMPDIR="+scratch, "XDG_CACHE_HOME="+scratch, "POC_RETRY_COUNTER="+filepath.Join(scratch, "counter"), "DD_CIVISIBILITY_CODE_COVERAGE_ENABLED=true")
	if unsetSession {
		var filtered []string
		for _, v := range env {
			if !strings.HasPrefix(v, "DD_TEST_SESSION_NAME=") {
				filtered = append(filtered, v)
			}
		}
		env = filtered
	}
	if retry {
		env = append(env, "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=1", "DD_CIVISIBILITY_TOTAL_FLAKY_RETRY_COUNT=2", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=process")
	}
	env = append(env, overrides...)
	out, stderr, code := command(t, dir, env, bin, args...)
	if code != 0 {
		t.Fatalf("fixture: %d %s %s", code, out, stderr)
	}
	if len(c.failures) > 0 {
		t.Fatal(c.failures)
	}
	return c
}
func TestMiniCIConfigurationWireParity(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	bins := compileMiniPair(t, dir, driver)
	for _, tc := range []struct {
		name  string
		extra []string
		unset bool
	}{
		{"explicit", []string{"DD_VERSION=app-1.2.3", "DD_TAGS=team:ci,custom:preserved"}, false},
		{"automatic-command", nil, true},
		{"automatic-job", []string{"GITHUB_ACTIONS=true", "GITHUB_JOB=unit-tests", "GITHUB_REPOSITORY=tonyredondo/dd-ci-testing-poc", "GITHUB_RUN_ID=123"}, true},
		{"explicit-empty", []string{"DD_TEST_SESSION_NAME="}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := runCIWireCase(t, dir, bins[0], []string{"-test.run=^TestPass$"}, tc.extra, tc.unset, false)
			got := runCIWireCase(t, dir, bins[1], []string{"-test.run=^TestPass$"}, tc.extra, tc.unset, false)
			if a, b := ciWireMetadata(t, want), ciWireMetadata(t, got); a != b {
				t.Fatalf("metadata SDK %s MINI %s", a, b)
			}
			if a, b := ciWireEvents(want.events), ciWireEvents(got.events); !reflect.DeepEqual(a, b) {
				t.Fatalf("CI events SDK %v MINI %v", a, b)
			}
			validateMiniHierarchy(t, want)
			validateMiniHierarchy(t, got)
		})
	}
}

const parallelCoverageSource = `package fixture

func CoverageFirst() int {
 return 11
}

func CoverageSecond() int {
 return 22
}

func CoverageRetryFirst() int {
 return 33
}

func CoverageRetrySecond() int {
 return 44
}
`
const parallelCoverageTests = `
func TestCoverageParallelFirst(t *testing.T) {t.Parallel();if fixture.CoverageFirst()!=11 {t.Fatal("first")}}
func TestCoverageParallelSecond(t *testing.T) {t.Parallel();if fixture.CoverageSecond()!=22 {t.Fatal("second")}}
func TestCoverageRetry(t *testing.T) {
 path:=os.Getenv("POC_RETRY_COUNTER")
 if _,err:=os.Stat(path);os.IsNotExist(err) {
  _=fixture.CoverageRetryFirst()
  if err=os.WriteFile(path,[]byte("attempt"),0600);err!=nil {t.Fatal(err)}
  t.Error("first attempt fails")
 } else {_=fixture.CoverageRetrySecond()}
}
`

func coverageAttemptRows(t *testing.T, c *miniWireCapture) []string {
	t.Helper()
	names := map[uint64]string{}
	for _, event := range c.events {
		if event["type"] == "test" {
			v := event["content"].(map[string]any)
			m := v["meta"].(map[string]any)
			names[v["span_id"].(uint64)] = fmt.Sprintf("%s retry=%v status=%s", m["test.name"], m["test.is_retry"] == "true", m["test.status"])
		}
	}
	var rows []string
	for _, entry := range c.coverages {
		name, ok := names[entry["span_id"].(uint64)]
		if !ok {
			t.Fatal("coverage has no test event")
		}
		for _, item := range entry["files"].([]any) {
			v := item.(map[string]any)
			if filepath.Base(v["filename"].(string)) == "coverage_cases.go" {
				rows = append(rows, fmt.Sprintf("%s %x", name, v["bitmap"]))
			}
		}
	}
	sort.Strings(rows)
	return rows
}

// Use the toolchain's independent coverage ranges as the oracle. Go 1.26
// includes the function declaration line; Go 1.27 starts at the first statement.
// The fixture has one block per function, in declaration order.
func nativeCoverageBitmaps(t *testing.T, dir string) map[string]string {
	t.Helper()
	generated := filepath.Join(t.TempDir(), "covered.go")
	out, stderr, code := command(t, dir, testEnv(), "go", "tool", "cover", "-mode=atomic", "-var=POCCover", "-o", generated, filepath.Join(dir, "coverage_cases.go"))
	if code != 0 {
		t.Fatalf("native coverage oracle: %d %s %s", code, out, stderr)
	}
	source, err := os.ReadFile(generated)
	if err != nil {
		t.Fatal(err)
	}
	blocks := regexp.MustCompile(`(?m)^\s*(\d+),\s*(\d+),\s*0x[0-9a-f]+,\s*// \[(\d+)\]`).FindAllStringSubmatch(string(source), -1)
	names := []string{"CoverageFirst", "CoverageSecond", "CoverageRetryFirst", "CoverageRetrySecond"}
	if len(blocks) != len(names) {
		t.Fatalf("unexpected native coverage blocks: %s", source)
	}
	result := map[string]string{}
	for i, block := range blocks {
		start, _ := strconv.Atoi(block[1])
		end, _ := strconv.Atoi(block[2])
		index, _ := strconv.Atoi(block[3])
		if index != i || start <= 0 || end < start {
			t.Fatalf("invalid native coverage range: %v", block)
		}
		bitmap := make([]byte, (end+7)/8)
		for line := start; line <= end; line++ {
			bitmap[(line-1)/8] |= 128 >> ((line - 1) % 8)
		}
		result[names[i]] = fmt.Sprintf("%x", bitmap)
	}
	return result
}
func TestMiniParallelAndRetryCoverageAttribution(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "coverage_cases.go"), []byte(parallelCoverageSource), 0600); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "sample_test.go")
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, append(body, []byte(parallelCoverageTests)...), 0600); err != nil {
		t.Fatal(err)
	}
	expectedBitmaps := nativeCoverageBitmaps(t, dir)
	bins := compileMiniPair(t, dir, driver, "-race", "-cover", "-covermode=atomic", "-coverpkg=./...")
	for _, tc := range []struct {
		name  string
		args  []string
		retry bool
	}{
		{"parallel", []string{"-test.run=^TestCoverageParallel", "-test.shuffle=42", "-test.parallel=8", "-test.count=2"}, false},
		{"retry", []string{"-test.run=^TestCoverageRetry$"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := runCIWireCase(t, dir, bins[0], tc.args, nil, false, tc.retry)
			got := runCIWireCase(t, dir, bins[1], tc.args, nil, false, tc.retry)
			assertMiniCIAttributes(t, want.events, got.events)
			a, b := coverageAttemptRows(t, want), coverageAttemptRows(t, got)
			if len(b) == 0 || !reflect.DeepEqual(a, b) {
				t.Fatalf("attribution SDK %v MINI %v", a, b)
			}
			if tc.retry {
				if len(b) != 1 || b[0] != "TestCoverageRetry retry=false status=fail "+expectedBitmaps["CoverageRetryFirst"] {
					t.Fatalf("retry coverage leaked or initial attempt missing: %v", b)
				}
				assertMiniEquivalent(t, execution{code: 0, events: normalizedEvents(want.events)}, execution{code: 0, events: normalizedEvents(got.events)})
			} else {
				first := "TestCoverageParallelFirst retry=false status=pass " + expectedBitmaps["CoverageFirst"]
				second := "TestCoverageParallelSecond retry=false status=pass " + expectedBitmaps["CoverageSecond"]
				expected := []string{first, first, second, second}
				if !reflect.DeepEqual(b, expected) {
					t.Fatalf("cross-test attribution: %v", b)
				}
			}
		})
	}
}
