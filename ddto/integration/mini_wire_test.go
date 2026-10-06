package integration

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

type miniWireCapture struct {
	capture
	wireMu    sync.Mutex
	payloads  []map[string]any
	coverages []map[string]any
}

func (c *miniWireCapture) handler(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/citestcycle") && !strings.HasSuffix(r.URL.Path, "/citestcov") {
		c.capture.handler(w, r)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		c.fail(err)
		w.WriteHeader(400)
		return
	}
	data := raw
	if r.Header.Get("Content-Encoding") == "gzip" {
		gz, e := gzip.NewReader(bytes.NewReader(raw))
		if e != nil {
			c.fail(e)
			w.WriteHeader(400)
			return
		}
		data, err = io.ReadAll(io.LimitReader(gz, 16<<20))
		gz.Close()
		if err != nil {
			c.fail(err)
			w.WriteHeader(400)
			return
		}
	}
	if strings.HasSuffix(r.URL.Path, "/citestcov") {
		_, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil {
			c.fail(e)
			w.WriteHeader(400)
			return
		}
		parts := multipart.NewReader(bytes.NewReader(data), params["boundary"])
		for {
			part, e := parts.NextPart()
			if e == io.EOF {
				break
			}
			if e != nil {
				c.fail(e)
				break
			}
			body, e := io.ReadAll(part)
			part.Close()
			if e != nil {
				c.fail(e)
				break
			}
			if part.FormName() != "coveragex" {
				continue
			}
			payload, e := decodeMsgpack(body)
			if e != nil {
				c.fail(e)
				break
			}
			envelope, ok := payload.(map[string]any)
			if !ok || envelope["version"] != uint64(2) {
				c.fail(fmt.Errorf("invalid coverage envelope"))
				break
			}
			rows, ok := envelope["coverages"].([]any)
			if !ok {
				c.fail(fmt.Errorf("missing coverages"))
				break
			}
			c.wireMu.Lock()
			for _, row := range rows {
				c.coverages = append(c.coverages, row.(map[string]any))
			}
			c.wireMu.Unlock()
		}
		w.WriteHeader(202)
		return
	}
	payload, e := decodeMsgpack(data)
	if e != nil {
		c.fail(e)
		w.WriteHeader(400)
		return
	}
	c.wireMu.Lock()
	c.payloads = append(c.payloads, payload.(map[string]any))
	c.wireMu.Unlock()
	r.Body = io.NopCloser(bytes.NewReader(raw))
	c.capture.handler(w, r)
}
func runMiniWire(t *testing.T, dir, bin string, agentless, coverage bool) *miniWireCapture {
	t.Helper()
	c := &miniWireCapture{}
	if coverage {
		c.profile = "coverage"
	}
	server := httptest.NewServer(http.HandlerFunc(c.handler))
	defer server.Close()
	scratch := t.TempDir()
	env := testEnv("DD_CIVISIBILITY_ENABLED=true", fmt.Sprintf("DD_CIVISIBILITY_AGENTLESS_ENABLED=%t", agentless), "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=fixture-key", "XDG_CACHE_HOME="+scratch, "TMPDIR="+scratch, "DD_CIVISIBILITY_CODE_COVERAGE_ENABLED=true")
	args := []string{"-test.run=^Test(Pass|Skip)$"}
	out, stderr, code := command(t, dir, env, bin, args...)
	if code != 0 {
		t.Fatalf("wire fixture: %d %s %s", code, out, stderr)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.failures) != 0 {
		t.Fatal(c.failures)
	}
	return c
}
func validateMiniHierarchy(t *testing.T, c *miniWireCapture) {
	t.Helper()
	if len(c.payloads) == 0 {
		t.Fatal("no wire payload")
	}
	for _, p := range c.payloads {
		if p["version"] != uint64(1) {
			t.Fatalf("wrong payload version: %v", p["version"])
		}
		metadata := p["metadata"].(map[string]any)["*"].(map[string]any)
		if metadata["language"] != "go" || metadata["runtime-id"] == "" {
			t.Fatalf("missing metadata: %v", metadata)
		}
	}
	ids := map[string]uint64{}
	for _, e := range c.events {
		content := e["content"].(map[string]any)
		switch e["type"] {
		case "test_session_end":
			ids["test_session_id"] = content["test_session_id"].(uint64)
		case "test_module_end":
			ids["test_module_id"] = content["test_module_id"].(uint64)
		case "test_suite_end":
			ids["test_suite_id"] = content["test_suite_id"].(uint64)
		}
	}
	if len(ids) != 3 {
		t.Fatalf("incomplete hierarchy: %v", ids)
	}
	for _, e := range c.events {
		content := e["content"].(map[string]any)
		if e["type"] != "test" {
			if _, ok := content["trace_id"]; ok {
				t.Fatal("hierarchy has distributed ID")
			}
			continue
		}
		if e["version"] != uint64(2) || content["trace_id"] == uint64(0) || content["span_id"] == uint64(0) {
			t.Fatal("invalid test identifiers")
		}
		for key, id := range ids {
			if id == 0 || content[key] != id {
				t.Fatalf("broken %s: %v/%d", key, content[key], id)
			}
		}
	}
}
func normalizedMiniCoverage(t *testing.T, c *miniWireCapture) []string {
	t.Helper()
	tests := map[uint64]string{}
	for _, e := range c.events {
		if e["type"] == "test" {
			v := e["content"].(map[string]any)
			tests[v["span_id"].(uint64)] = v["meta"].(map[string]any)["test.name"].(string)
		}
	}
	var rows []string
	for _, coverage := range c.coverages {
		name, ok := tests[coverage["span_id"].(uint64)]
		if !ok {
			t.Fatal("coverage without matching test")
		}
		for _, file := range coverage["files"].([]any) {
			v := file.(map[string]any)
			rows = append(rows, fmt.Sprintf("%s %s %x", name, filepath.Base(v["filename"].(string)), v["bitmap"]))
		}
	}
	sort.Strings(rows)
	return rows
}
func TestMiniWireHierarchyAgentAndCoverage(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	bins := compileMiniPair(t, dir, driver, "-cover", "-covermode=atomic", "-coverpkg=./...")
	for _, agentless := range []bool{false, true} {
		t.Run(fmt.Sprintf("agentless=%t", agentless), func(t *testing.T) {
			want := runMiniWire(t, dir, bins[0], agentless, true)
			got := runMiniWire(t, dir, bins[1], agentless, true)
			validateMiniHierarchy(t, got)
			assertMiniCIAttributes(t, want.events, got.events)
			assertSessionCommonMetadataPlacement(t, got)
			expected, actual := normalizedMiniCoverage(t, want), normalizedMiniCoverage(t, got)
			if len(actual) == 0 || !reflect.DeepEqual(expected, actual) {
				t.Fatalf("coverage differs: SDK %v MINI %v", expected, actual)
			}
			found := false
			for _, row := range actual {
				found = found || row == "TestPass sample.go 20"
				if strings.HasPrefix(row, "TestSkip sample.go ") {
					t.Fatalf("coverage leaked into skipped test: %v", actual)
				}
			}
			if !found {
				t.Fatalf("application coverage missing: %v", actual)
			}
		})
	}
}
