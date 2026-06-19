package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
)

func TestTransportSendPackFilesRequestAndResponse(t *testing.T) {
	var pushed pushedShaBody
	var packfiles [][]byte
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/"+sendPackFilesURLPath {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatalf("multipart reader: %v", err)
		}
		sawPushedSha := false
		sawPackfile := false
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("next part: %v", err)
			}
			body, _ := io.ReadAll(part)
			switch part.FormName() {
			case "pushedSha":
				if part.Header.Get(HeaderContentType) != constants.ContentTypeJSON {
					t.Fatalf("pushedSha content type = %q", part.Header.Get(HeaderContentType))
				}
				if err := json.Unmarshal(body, &pushed); err != nil {
					t.Fatalf("decode pushedSha: %v", err)
				}
				sawPushedSha = true
			case "packfile":
				if part.Header.Get(HeaderContentType) != constants.ContentTypeOctetStream {
					t.Fatalf("packfile content type = %q", part.Header.Get(HeaderContentType))
				}
				packfiles = append(packfiles, append([]byte(nil), body...))
				sawPackfile = true
			default:
				t.Fatalf("unexpected multipart field %q", part.FormName())
			}
		}
		if !sawPushedSha || !sawPackfile {
			t.Fatalf("request should include pushedSha and packfile parts, saw pushedSha=%t packfile=%t", sawPushedSha, sawPackfile)
		}
		w.Header().Set(HeaderContentType, constants.ContentTypeJSON)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	tempDir := t.TempDir()
	firstPackPath := filepath.Join(tempDir, "objects-1.pack")
	secondPackPath := filepath.Join(tempDir, "objects-2.pack")
	if err := os.WriteFile(firstPackPath, []byte("pack-one"), 0o644); err != nil {
		t.Fatalf("write first packfile: %v", err)
	}
	if err := os.WriteFile(secondPackPath, []byte("pack-two"), 0o644); err != nil {
		t.Fatalf("write second packfile: %v", err)
	}

	transport := newRawResponseTestClient(server)
	bytesSent, err := transport.SendPackFiles("", []string{firstPackPath, secondPackPath})
	if err != nil {
		t.Fatalf("SendPackFiles() returned error: %v", err)
	}
	if requests != 2 {
		t.Fatalf("expected one request per packfile, got %d", requests)
	}
	if bytesSent != int64(len("pack-one")+len("pack-two")) {
		t.Fatalf("bytes sent = %d, want %d", bytesSent, len("pack-one")+len("pack-two"))
	}
	if pushed.Data.ID != transport.commitSha || pushed.Data.Type != constants.SearchCommitsType {
		t.Fatalf("unexpected pushed sha body: %#v", pushed)
	}
	if pushed.Meta.RepositoryURL != transport.repositoryURL {
		t.Fatalf("repository URL = %q, want %q", pushed.Meta.RepositoryURL, transport.repositoryURL)
	}
	if len(packfiles) != 2 || string(packfiles[0]) != "pack-one" || string(packfiles[1]) != "pack-two" {
		t.Fatalf("packfile bodies = %#v", packfiles)
	}
}

func TestTransportSendPackFilesErrors(t *testing.T) {
	bytesSent, err := (&transport{}).SendPackFiles("", nil)
	if err != nil || bytesSent != 0 {
		t.Fatalf("empty packfiles should be a noop, got bytes=%d err=%v", bytesSent, err)
	}
	if _, err := (&transport{}).SendPackFiles("", []string{"missing"}); err == nil ||
		!strings.Contains(err.Error(), "repository URL and commit SHA are required") {
		t.Fatalf("expected repository and sha error, got %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()
	transport := newRawResponseTestClient(server)

	if _, err := transport.SendPackFiles(transport.commitSha, []string{"missing"}); err == nil ||
		!strings.Contains(err.Error(), "failed to read pack file") {
		t.Fatalf("expected file read error, got %v", err)
	}

	packPath := filepath.Join(t.TempDir(), "objects.pack")
	if err := os.WriteFile(packPath, []byte("pack-data"), 0o644); err != nil {
		t.Fatalf("write packfile: %v", err)
	}
	if _, err := transport.SendPackFiles(transport.commitSha, []string{packPath}); err == nil ||
		!strings.Contains(err.Error(), "unexpected response code 400") {
		t.Fatalf("expected bad status error, got %v", err)
	}
}
