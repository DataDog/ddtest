package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
)

func TestTransportGetCommitsRequestAndResponse(t *testing.T) {
	var captured searchCommits
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+searchCommitsURLPath {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set(HeaderContentType, constants.ContentTypeJSON)
		_ = json.NewEncoder(w).Encode(searchCommits{
			Data: []searchCommitsData{
				{ID: "remote-1", Type: constants.SearchCommitsType},
				{ID: "remote-2", Type: constants.SearchCommitsType},
			},
		})
	}))
	defer server.Close()

	transport := newRawResponseTestClient(server)
	commits, err := transport.GetCommits([]string{"local-1", "local-2"})
	if err != nil {
		t.Fatalf("GetCommits() returned error: %v", err)
	}
	if got, want := captured.Meta.RepositoryURL, transport.repositoryURL; got != want {
		t.Fatalf("repository URL = %q, want %q", got, want)
	}
	if len(captured.Data) != 2 || captured.Data[0].ID != "local-1" || captured.Data[1].ID != "local-2" {
		t.Fatalf("unexpected commit request: %#v", captured.Data)
	}
	for _, commit := range captured.Data {
		if commit.Type != constants.SearchCommitsType {
			t.Fatalf("commit %q type = %q, want %q", commit.ID, commit.Type, constants.SearchCommitsType)
		}
	}
	if len(commits) != 2 || commits[0] != "remote-1" || commits[1] != "remote-2" {
		t.Fatalf("unexpected remote commits: %#v", commits)
	}
}

func TestTransportGetCommitsErrors(t *testing.T) {
	if _, err := (&transport{}).GetCommits([]string{"local"}); err == nil {
		t.Fatal("expected repository URL error")
	}

	t.Run("request failure", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "backend error", http.StatusInternalServerError)
		}))
		defer server.Close()

		commits, err := newRawResponseTestClient(server).GetCommits([]string{"local"})
		if commits != nil {
			t.Fatalf("expected nil commits, got %#v", commits)
		}
		if err == nil || !strings.Contains(err.Error(), "sending search commits request") {
			t.Fatalf("expected request error, got %v", err)
		}
	})

	t.Run("unmarshal failure", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(HeaderContentType, constants.ContentTypeJSON)
			_, _ = w.Write([]byte(`{"data":`))
		}))
		defer server.Close()

		commits, err := newRawResponseTestClient(server).GetCommits([]string{"local"})
		if commits != nil {
			t.Fatalf("expected nil commits, got %#v", commits)
		}
		if err == nil || !strings.Contains(err.Error(), "unmarshalling search commits response") {
			t.Fatalf("expected unmarshal error, got %v", err)
		}
	})
}
