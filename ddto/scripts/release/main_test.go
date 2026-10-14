package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

// The runtime archive holds the module files and the non-test sources of the
// runtime's directories, and the same sources give the same bytes.
func TestRuntimeArchiveContents(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	first, err := runtimeArchive(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtimeArchive(root, nil)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("archive not reproducible: %v", err)
	}
	files := archiveFiles(t, first)
	for _, want := range []string{"go.mod", "LICENSE", "testopt/testopt.go", "internal/toolwrap/toolwrap.go", "internal/bundle/bundle.go"} {
		if !files[want] {
			t.Errorf("archive lacks %s", want)
		}
	}
	// Every license text of the incorporated sources travels with them.
	for _, dir := range runtimeDirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			upper := strings.ToUpper(entry.Name())
			if rel, _ := filepath.Rel(root, path); (strings.HasPrefix(upper, "LICENSE") || strings.HasPrefix(upper, "NOTICE")) &&
				!strings.Contains(filepath.ToSlash(rel), "/testdata/") && !files[filepath.ToSlash(rel)] {
				t.Errorf("archive lacks %s", filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for name := range files {
		if strings.HasSuffix(name, "_test.go") || strings.Contains(name, "/testdata/") || strings.HasPrefix(name, "cmd/") {
			t.Errorf("archive holds %s", name)
		}
	}
}

// Every file that Go compiles for a runtime package is in the archive, and no
// runtime package lies outside it. Tests can target any platform that Go
// supports, not only those of the release executables.
func TestRuntimeArchiveCoversEveryPlatform(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	archive, err := runtimeArchive(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	files := archiveFiles(t, archive)
	patterns := make([]string, 0, len(runtimeDirs))
	for _, dir := range runtimeDirs {
		patterns = append(patterns, "./"+dir+"/...")
	}
	ports, err := exec.Command("go", "tool", "dist", "list").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range strings.Fields(string(ports)) {
		goos, goarch, _ := strings.Cut(target, "/")
		cmd := exec.Command("go", append([]string{"list", "-deps", "-json=ImportPath,Dir,Standard,GoFiles,CgoFiles,SFiles,HFiles,CFiles,EmbedFiles"}, patterns...)...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0", "GOFLAGS=", "GOWORK=off")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: go list: %v", target, err)
		}
		decoder := json.NewDecoder(bytes.NewReader(out))
		for decoder.More() {
			var p struct {
				ImportPath, Dir                                       string
				Standard                                              bool
				GoFiles, CgoFiles, SFiles, HFiles, CFiles, EmbedFiles []string
			}
			if err := decoder.Decode(&p); err != nil {
				t.Fatal(err)
			}
			if p.Standard {
				continue
			}
			rel, err := filepath.Rel(root, p.Dir)
			if err != nil || !strings.HasPrefix(p.ImportPath, module+"/") {
				t.Fatalf("%s: package %s outside the module", target, p.ImportPath)
			}
			for _, group := range [][]string{p.GoFiles, p.CgoFiles, p.SFiles, p.HFiles, p.CFiles, p.EmbedFiles} {
				for _, name := range group {
					if path := filepath.ToSlash(filepath.Join(rel, name)); !files[path] {
						t.Errorf("%s: archive lacks %s", target, path)
					}
				}
			}
		}
	}
}

// -version sets Number in the version package that the executables and the
// runtime archive carry, and the tool wrapper's build log prefix follows it.
func TestStampVersion(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(versionFile)))
	if err != nil {
		t.Fatal(err)
	}
	// Stamping the current version changes nothing.
	same, prefix, err := stampVersion(source, version.Tag)
	if err != nil || !bytes.Equal(same, source) || prefix != version.BuildLogPrefix {
		t.Fatalf("stamping %s: prefix %q, %v", version.Tag, prefix, err)
	}
	for _, tag := range []string{"v1.2.3", "v0.1.0-rc.1"} {
		stamped, prefix, err := stampVersion(source, tag)
		if err != nil {
			t.Fatal(err)
		}
		if want := `const Number = "` + tag[1:] + `"`; !bytes.Contains(stamped, []byte(want)) || prefix != "TestOptimization.build "+tag {
			t.Fatalf("%s: prefix %q, source lacks %s", tag, prefix, want)
		}
		archive, err := runtimeArchive(root, map[string][]byte{versionFile: stamped})
		if err != nil {
			t.Fatal(err)
		}
		reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			t.Fatal(err)
		}
		file, err := reader.Open(versionFile)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(file)
		file.Close()
		if err != nil || !bytes.Equal(data, stamped) {
			t.Fatalf("%s: the archive does not carry the stamped %s: %v", tag, versionFile, err)
		}
	}
	for _, tag := range []string{"1.2.3", "v1.2", "v1.2.3+build", "v01.2.3", "v1.2.3-"} {
		if _, _, err := stampVersion(source, tag); err == nil {
			t.Errorf("accepted %q", tag)
		}
	}
	// A published release must be a version that go install resolves for
	// this module path.
	for tag, ok := range map[string]bool{"v0.1.0": true, "v1.4.2-rc.1": true, "v2.0.0": false, "v10.0.0": false, "1.0.0": false} {
		if err := publishable(tag); (err == nil) != ok {
			t.Errorf("publishable(%q) = %v", tag, err)
		}
	}
}

func archiveFiles(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]bool{}
	for _, file := range reader.File {
		files[file.Name] = true
	}
	return files
}
