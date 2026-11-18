package net

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

var tiaTestKey = TIAArtifactKey{RepositoryID: "https://github.com/DataDog/dd-trace-go.git", SuiteID: "suite", EnvHash: "env", Language: "go", CompatibilityVersion: 1}

const (
	tiaCommitA = "1111111111111111111111111111111111111111"
	tiaCommitB = "2222222222222222222222222222222222222222"
)

// The lookup sends a JSON:API request with the key and the commits, nearest
// first; a 404 is a miss, and a 200 the stored bytes with the matched and
// source commits.
func TestTIAArtifactLookup(t *testing.T) {
	for _, tc := range []struct {
		name            string
		status          int
		matched, source string
		want            bool
		err             bool
	}{
		{"hit", http.StatusOK, tiaCommitB, tiaCommitA, true, false},
		{"hit, gzip-encoded", http.StatusOK, tiaCommitB, tiaCommitA, true, false},
		{"miss", http.StatusNotFound, "", "", false, false},
		{"matched commit not requested", http.StatusOK, "3333333333333333333333333333333333333333", tiaCommitA, false, true},
		{"source commit not a sha", http.StatusOK, tiaCommitB, "main", false, true},
		{"server error", http.StatusBadRequest, "", "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request struct {
				Data struct {
					ID         string         `json:"id"`
					Type       string         `json:"type"`
					Attributes map[string]any `json:"attributes"`
				} `json:"data"`
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if r.Method != http.MethodPost || r.URL.Path != "/api/unstable/ci/tia/artifacts/lookup" || r.Header.Get(HeaderContentType) != ContentTypeJSONAlternative ||
					r.Header.Get("dd-api-key") != "test_api_key" || json.Unmarshal(body, &request) != nil {
					t.Errorf("request %s %s %v %s", r.Method, r.URL.Path, r.Header, body)
				}
				if tc.matched != "" {
					w.Header().Set("x-dd-tia-git-commit-sha", tc.matched)
					w.Header().Set("x-dd-tia-source-commit", tc.source)
				}
				w.Header().Set(HeaderContentType, ContentTypeJSON)
				body = []byte(`{"version":1}`)
				if strings.HasSuffix(tc.name, "gzip-encoded") {
					if r.Header.Get(HeaderAcceptEncoding) != ContentEncodingGzip {
						t.Errorf("Accept-Encoding %q", r.Header.Get(HeaderAcceptEncoding))
					}
					w.Header().Set(HeaderContentEncoding, ContentEncodingGzip)
					var compressed bytes.Buffer
					writer := gzip.NewWriter(&compressed)
					_, _ = writer.Write(body)
					_ = writer.Close()
					body = compressed.Bytes()
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write(body)
			}))
			defer server.Close()
			origEnv := saveEnv()
			defer restoreEnv(origEnv)
			setCiVisibilityEnv(os.Getenv("PATH"), server.URL)
			c := NewTIAArtifactsClient()
			if c == nil || c.RepositoryID() != tiaTestKey.RepositoryID {
				t.Fatalf("client %v", c)
			}
			artifact, err := c.LookupTIAArtifact(context.Background(), tiaTestKey, []string{tiaCommitB, tiaCommitA})
			if (err != nil) != tc.err || (artifact != nil) != tc.want {
				t.Fatalf("artifact %+v, err %v", artifact, err)
			}
			if tc.want && (string(artifact.Content) != `{"version":1}` || artifact.MatchedCommit != tiaCommitB || artifact.SourceCommit != tiaCommitA) {
				t.Fatalf("artifact %+v", artifact)
			}
			attributes := request.Data.Attributes
			commits, _ := attributes["git_commit_shas"].([]any)
			if request.Data.Type != "ci_app_tia_artifact_lookup" || request.Data.ID == "" || attributes["repository_id"] != tiaTestKey.RepositoryID ||
				attributes["suite_id"] != "suite" || attributes["env_hash"] != "env" || attributes["language"] != "go" || attributes["compatibility_version"] != float64(1) ||
				len(commits) != 2 || commits[0] != tiaCommitB || commits[1] != tiaCommitA {
				t.Fatalf("request %+v", request)
			}
		})
	}
}

// A lookup reads at most 5 MiB, as the API stores, before and after HTTP's
// gzip encoding: a larger or expanding body is an error, not a map.
func TestTIAArtifactLookupBound(t *testing.T) {
	large := append([]byte(`{"version":1}`), bytes.Repeat([]byte(" "), MaxTIAArtifactBytes)...)
	for _, encoded := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("x-dd-tia-git-commit-sha", tiaCommitA)
			w.Header().Set("x-dd-tia-source-commit", tiaCommitA)
			w.Header().Set(HeaderContentType, ContentTypeJSON)
			body := large
			if encoded {
				w.Header().Set(HeaderContentEncoding, ContentEncodingGzip)
				var compressed bytes.Buffer
				writer := gzip.NewWriter(&compressed)
				_, _ = writer.Write(large)
				_ = writer.Close()
				body = compressed.Bytes()
			}
			_, _ = w.Write(body)
		}))
		origEnv := saveEnv()
		setCiVisibilityEnv(os.Getenv("PATH"), server.URL)
		artifact, err := NewTIAArtifactsClient().LookupTIAArtifact(context.Background(), tiaTestKey, []string{tiaCommitA})
		restoreEnv(origEnv)
		server.Close()
		if artifact != nil || err == nil {
			t.Fatalf("gzip-encoded %t: %d bytes, %v", encoded, len(artifact.Content), err)
		}
	}
}

// A response that breaks off in its body is retried once, as an error
// before the response is.
func TestTIAArtifactLookupRetriesABrokenBody(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		body := []byte(`{"version":1}`)
		w.Header().Set("x-dd-tia-git-commit-sha", tiaCommitA)
		w.Header().Set("x-dd-tia-source-commit", tiaCommitA)
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		if requests == 1 {
			// Headers and part of the body, then the connection closes.
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body[:4])
			w.(http.Flusher).Flush()
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()
	origEnv := saveEnv()
	defer restoreEnv(origEnv)
	setCiVisibilityEnv(os.Getenv("PATH"), server.URL)
	artifact, err := NewTIAArtifactsClient().LookupTIAArtifact(context.Background(), tiaTestKey, []string{tiaCommitA})
	if err != nil || artifact == nil || string(artifact.Content) != `{"version":1}` || requests != 2 {
		t.Fatalf("artifact %+v, err %v, %d requests", artifact, err, requests)
	}
}

// The upload sends the map's JSON as it is, as dd-trace-py does, with the key
// and the commit in the query, and succeeds with a 201; a body over 5 MiB is
// not sent.
func TestTIAArtifactUpload(t *testing.T) {
	var got []byte
	var query map[string][]string
	status := http.StatusCreated
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		query = r.URL.Query()
		if r.Method != http.MethodPut || r.URL.Path != "/api/unstable/ci/tia/artifacts" || r.Header.Get(HeaderContentType) != ContentTypeOctetStream || r.Header.Get(HeaderContentEncoding) != "" {
			t.Errorf("request %s %s %v", r.Method, r.URL.Path, r.Header)
		}
		w.Header().Set(HeaderContentType, ContentTypeJSONAlternative)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"data":{"type":"ci_app_tia_artifact_upload","attributes":{"blob_id":"b","deduplicated":false}}}`))
	}))
	defer server.Close()
	origEnv := saveEnv()
	defer restoreEnv(origEnv)
	setCiVisibilityEnv(os.Getenv("PATH"), server.URL)
	c := NewTIAArtifactsClient()
	content := []byte(`{"version":1}`)
	if err := c.StoreTIAArtifact(context.Background(), tiaTestKey, tiaCommitA, content); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(got) || string(got) != `{"version":1}` {
		t.Fatalf("body %q", got)
	}
	for key, want := range map[string]string{"repository_id": tiaTestKey.RepositoryID, "suite_id": "suite", "env_hash": "env", "language": "go", "compatibility_version": "1", "git_commit_sha": tiaCommitA} {
		if len(query[key]) != 1 || query[key][0] != want {
			t.Errorf("%s: %v, want %q", key, query[key], want)
		}
	}
	status = http.StatusOK
	if err := c.StoreTIAArtifact(context.Background(), tiaTestKey, tiaCommitA, content); err == nil {
		t.Fatal("a 200 stored the map")
	}
	got = nil
	if err := c.StoreTIAArtifact(context.Background(), tiaTestKey, tiaCommitA, make([]byte, MaxTIAArtifactBytes+1)); err == nil || got != nil {
		t.Fatalf("an oversized map: %v, %d bytes sent", err, len(got))
	}
	if err := c.StoreTIAArtifact(context.Background(), tiaTestKey, "HEAD", content); err == nil {
		t.Fatal("a commit that is not a sha")
	}
	if err := c.StoreTIAArtifact(context.Background(), tiaTestKey, tiaCommitA, nil); err == nil {
		t.Fatal("an empty body")
	}
}

// Through the Agent, requests take the EVP proxy with the api subdomain.
func TestTIAArtifactsThroughTheAgent(t *testing.T) {
	var path, subdomain string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, subdomain = r.URL.Path, r.Header.Get("X-Datadog-EVP-Subdomain")
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	origEnv := saveEnv()
	defer restoreEnv(origEnv)
	setCiVisibilityEnv(os.Getenv("PATH"), server.URL)
	os.Setenv("DD_CIVISIBILITY_AGENTLESS_ENABLED", "false")
	os.Setenv("DD_TRACE_AGENT_URL", server.URL)
	c := NewTIAArtifactsClient()
	artifact, err := c.LookupTIAArtifact(context.Background(), tiaTestKey, []string{tiaCommitA})
	if artifact != nil || err != nil || path != "/evp_proxy/v2/api/unstable/ci/tia/artifacts/lookup" || subdomain != "api" {
		t.Fatalf("%v %v %s %s", artifact, err, path, subdomain)
	}
}

// Agentless without an API key has no client; a request that the context
// stops ends at once.
func TestTIAArtifactsWithoutCredentials(t *testing.T) {
	origEnv := saveEnv()
	defer restoreEnv(origEnv)
	setCiVisibilityEnv(os.Getenv("PATH"), "http://127.0.0.1:1")
	os.Unsetenv("DD_API_KEY")
	if c := NewTIAArtifactsClient(); c != nil {
		t.Fatal("a client without an API key")
	}
	os.Setenv("DD_API_KEY", "test_api_key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(300 * time.Millisecond):
		}
	}))
	defer server.Close()
	os.Setenv("DD_CIVISIBILITY_AGENTLESS_URL", server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := NewTIAArtifactsClient().LookupTIAArtifact(ctx, tiaTestKey, []string{tiaCommitA})
	if err == nil || time.Since(started) > 900*time.Millisecond || !strings.Contains(err.Error(), "context") && !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("%v after %s", err, time.Since(started))
	}
}
