package bazeldoctor

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func newTestDoctor(t *testing.T, args Args) *doctor {
	t.Helper()
	workspace := t.TempDir()
	resolved, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	return &doctor{ctx: context.Background(), request: Request{Args: args}, out: &output{level: levels["INFO"]}, cache: newOutputs(),
		report: newReport(args), workers: 2, workspace: resolved}
}

// stopped runs f and returns the failure message or fallback reason that
// stopped it.
func stopped(f func()) (failed, fellBack string) {
	defer func() {
		switch typed := recover().(type) {
		case failure:
			failed = typed.message
		case fallback:
			fellBack = typed.reason
		case nil:
		default:
			panic(typed)
		}
	}()
	f()
	return "", ""
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeZip(t *testing.T, path string, entries map[string]string, order []string) {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range order {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(entries[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, buffer.String())
}

func TestExtractOutputsZip(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "outputs.zip")
	writeZip(t, archive, map[string]string{"payloads/": "", "payloads/tests/a.json": "{}"},
		[]string{"payloads/", "payloads/tests/a.json"})
	dst := filepath.Join(root, "dst")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := extractOutputsZip(archive, dst); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dst, "payloads", "tests", "a.json")); err != nil || string(data) != "{}" {
		t.Fatalf("extracted %q, %v", data, err)
	}
}

// Unsafe names are refused with Python's message, which quotes the name as
// repr() does.
func TestExtractOutputsZipRefusesUnsafeNames(t *testing.T) {
	for _, name := range []string{"../escape.json", "/abs.json", "C:/drive.json", "a//b.json", "./a.json", `a\..\b`} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			archive := filepath.Join(root, "outputs.zip")
			writeZip(t, archive, map[string]string{name: "{}"}, []string{name})
			dst := filepath.Join(root, "dst")
			if err := os.Mkdir(dst, 0o755); err != nil {
				t.Fatal(err)
			}
			err := extractOutputsZip(archive, dst)
			want := "unsafe path in BEP outputs.zip " + archive + ": " + "'" + strings.ReplaceAll(name, `\`, `\\`) + "'"
			var staging stageError
			if !errors.As(err, &staging) || staging.message != want {
				t.Fatalf("got %v, want %s", err, want)
			}
		})
	}
}

func TestExtractOutputsZipLeavesBrokenArchivesToPython(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "outputs.zip")
	writeFile(t, archive, "not a zip")
	_, fellBack := stopped(func() { _ = extractOutputsZip(archive, root) })
	if !strings.HasPrefix(fellBack, "outputs.zip ") {
		t.Fatalf("fallback %q", fellBack)
	}
}

func TestCopyTreeContentsRefusesSymlinks(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "payloads", "tests", "a.json"), "{}")
	if err := copyTreeContents(src, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(src, "payloads", "link.json")
	if err := os.Symlink("tests/a.json", link); err != nil {
		t.Skip("symbolic links unavailable:", err)
	}
	err := copyTreeContents(src, t.TempDir())
	if err == nil || err.Error() != "refusing to stage symlink from BEP artifact source: "+link {
		t.Fatalf("got %v", err)
	}
}

func TestDedupeReferences(t *testing.T) {
	d := newTestDoctor(t, Args{})
	refs := []reference{
		{label: "//b:t", outputKey: "b/t", fetchValue: "bytestream://x/2", remoteOnly: true, stageableCarrier: true},
		{label: "//b:t", outputKey: "b/t", fetchValue: "bytestream://x/1", remoteOnly: true, stageableCarrier: true},
		{label: "//a:t", outputKey: "a/t", fetchValue: "bytestream://y", remoteOnly: true, stageableCarrier: true},
		{label: "//a:t", outputKey: "a/t", fetchValue: "bytestream://y", remoteOnly: true, stageableCarrier: true},
		{label: "//c:t", outputKey: "c/t", fetchValue: "bytestream://z", remoteOnly: true, cached: true},
	}
	selected, ambiguous := d.dedupeReferences(refs, "download")
	if len(ambiguous) != 1 || ambiguous[0] != (pair{"//b:t", "b/t"}) {
		t.Fatalf("ambiguous %v", ambiguous)
	}
	var got []string
	for _, ref := range selected {
		got = append(got, ref.label+" "+ref.fetchValue)
	}
	if strings.Join(got, ",") != "//a:t bytestream://y,//c:t bytestream://z" {
		t.Fatalf("selected %v", got)
	}
}

func TestSmallHelpers(t *testing.T) {
	for value, want := range map[float64]string{300: "300", 0.5: "0.5", 1234567: "1.23457e+06", 1e-05: "1e-05", 2.25: "2.25"} {
		if got := formatG(value); got != want {
			t.Errorf("formatG(%v) = %q, want %q", value, got, want)
		}
	}
	for value, want := range map[string]string{"//pkg:target": "pkg_target", "...": "artifact", "a/b c": "a_b_c"} {
		if got := safeDownloadFragment(value); got != want {
			t.Errorf("safeDownloadFragment(%q) = %q, want %q", value, got, want)
		}
	}
	for value, want := range map[string]bool{"https://host/x": true, "HTTP://host": true, "http:///x": false,
		"bytestream://host/x": false, "file:///x": false, "http:host": false} {
		if got := isHTTPReference(value); got != want {
			t.Errorf("isHTTPReference(%q) = %v, want %v", value, got, want)
		}
	}
}

// A local outputs.zip carrier is staged into an owned directory, and the
// cleanup removes its run directory.
func TestStageLocalCarrierAndCleanup(t *testing.T) {
	d := newTestDoctor(t, Args{RemoteArtifacts: "disabled"})
	carrier := filepath.Join(d.workspace, "bazel-testlogs", "pkg", "t", "test.outputs", "outputs.zip")
	writeZip(t, carrier, map[string]string{"payloads/tests/a.json": "{}"}, []string{"payloads/tests/a.json"})
	f := &freshness{ambiguousOutputs: map[string]bool{}, ambiguousLabels: map[string]bool{},
		references: []reference{{label: "//pkg:t", outputKey: "pkg/t/test.outputs", fetchValue: carrier,
			stageableCarrier: true}}}
	base := filepath.Join(d.workspace, ".topt", "bep-artifacts")
	staged := d.stageArtifacts(f, base, nil)
	if len(staged) != 1 {
		t.Fatalf("staged %v; stderr %s", staged, d.out.stderr.String())
	}
	item := staged[0]
	if !isFile(filepath.Join(item.outputDir, "payloads", "tests", "a.json")) ||
		!isFile(filepath.Join(item.outputDir, stagingMarker)) {
		t.Fatalf("staged output incomplete: %s", item.outputDir)
	}
	if stagedCarrier(item) != "outputs_zip" {
		t.Fatalf("carrier %s", stagedCarrier(item))
	}
	d.cleanupStagedRuns(staged, base)
	if exists(item.stagingRoot) {
		t.Fatalf("run directory %s remains", item.stagingRoot)
	}
}

// Python warns once with the same text when a local carrier is missing, and
// removes the empty run directory.
func TestStageMissingLocalCarrierWarns(t *testing.T) {
	d := newTestDoctor(t, Args{RemoteArtifacts: "disabled"})
	f := &freshness{ambiguousOutputs: map[string]bool{}, ambiguousLabels: map[string]bool{},
		references: []reference{{label: "//pkg:t", outputKey: "pkg/t/test.outputs", fetchValue: "missing/outputs.zip",
			stageableCarrier: true}}}
	base := filepath.Join(d.workspace, "stage")
	if staged := d.stageArtifacts(f, base, nil); len(staged) != 0 {
		t.Fatalf("staged %v", staged)
	}
	want := prefix + "warning: BEP artifact is not available locally: missing/outputs.zip\n"
	if d.out.stderr.String() != want {
		t.Fatalf("stderr %q", d.out.stderr.String())
	}
	if entries, _ := os.ReadDir(filepath.Join(base, "__runs")); len(entries) != 0 {
		t.Fatalf("run directories remain: %v", entries)
	}
}

func TestRunDownloader(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell scripts")
	}
	d := newTestDoctor(t, Args{BepArtifactDownloaderTimeout: 5})
	ref := reference{label: "//pkg:t", outputKey: "pkg/t/test.outputs", fetchValue: "bytestream://host/blob", name: "outputs.zip"}
	script := func(body string) string {
		path := filepath.Join(t.TempDir(), "download.sh")
		writeFile(t, path, "#!/bin/sh\n"+body+"\n")
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	runDir := t.TempDir()
	ok := script(`while [ $# -gt 0 ]; do if [ "$1" = --output ]; then printf zip > "$2"; fi; shift; done`)
	if source, err := d.runDownloader(ref, runDir, ok); err != nil || pathName(source) != "outputs.zip" || !isFile(source) {
		t.Fatalf("source %q, %v", source, err)
	}
	if source, err := d.runDownloader(ref, runDir, script("exit 3")); err != nil || source != "" {
		t.Fatalf("source %q, %v", source, err)
	}
	d.request.Args.BepArtifactDownloaderTimeout = 0.2
	if source, err := d.runDownloader(ref, runDir, script("exec sleep 5")); err != nil || source != "" {
		t.Fatalf("source %q, %v", source, err)
	}
	want := prefix + "warning: BEP artifact downloader failed for //pkg:t with exit code 3\n" +
		prefix + "warning: BEP artifact downloader timed out for //pkg:t after 0.2s\n"
	if d.out.stderr.String() != want {
		t.Fatalf("stderr %q", d.out.stderr.String())
	}
}

func TestDownloadHTTPRetries(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "datadog-rules-test-optimization" || r.Header.Get("Accept-Encoding") != "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("zip"))
	}))
	defer server.Close()
	d := newTestDoctor(t, Args{BepArtifactDownloaderTimeout: 5})
	ref := reference{label: "//pkg:t", outputKey: "pkg/t/test.outputs", fetchValue: server.URL + "/blob?token=secret"}
	source, err := d.downloadHTTP(ref, t.TempDir())
	if err != nil || requests.Load() != 2 {
		t.Fatalf("source %q, %v after %d requests", source, err, requests.Load())
	}
	if data, _ := os.ReadFile(source); string(data) != "zip" {
		t.Fatalf("downloaded %q", data)
	}
	want := prefix + "BEP HTTP artifact downloaded for //pkg:t output pkg/t/test.outputs: " + server.URL + "/blob (3 bytes)\n"
	if d.out.stderr.String() != want {
		t.Fatalf("stderr %q", d.out.stderr.String())
	}
}

// urllib's error text is Python's own, so a failed download falls back.
func TestDownloadHTTPFailureFallsBack(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	d := newTestDoctor(t, Args{BepArtifactDownloaderTimeout: 5})
	ref := reference{label: "//pkg:t", outputKey: "pkg/t/test.outputs", fetchValue: server.URL}
	if _, fellBack := stopped(func() { _, _ = d.downloadHTTP(ref, t.TempDir()) }); fellBack == "" {
		t.Fatal("no fallback")
	}
}
