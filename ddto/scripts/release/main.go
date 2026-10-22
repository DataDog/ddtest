// Command release builds ddto's release executables, one per platform. Each
// carries the Mini runtime's sources and the tool wrapper executable for its
// platform (see internal/bundle), so that users need neither the module's
// sources nor a build of ddto or its wrapper. Run it from the module root:
//
//	go run ./scripts/release [-version v1.2.3] [-o dist] [-target linux/amd64,darwin/arm64]
//	go run ./scripts/release -version v1.2.3 -stamp
//
// -version sets internal/version's Number for the build, through -overlay:
// the executables, their wrapper and the runtime they carry all report it,
// and the checkout is not modified. With -stamp it writes that Number into the
// checkout instead, for the release commit, and builds nothing.
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/bundle"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/toolwrap"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

const module = "github.com/tonyredondo/dd-ci-testing-poc"

// targets are the platforms of GitHub's hosted runners and of most developers.
var targets = []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64", "windows/arm64"}

// runtimeDirs hold every package that the Mini runtime and ddto's
// instrumentation import. cmd/ddto, tests, docs and scripts stay out.
var runtimeDirs = []string{"internal", "propagation", "testopt"}

// runtimeFiles are the module files that Go needs to compile those packages.
var runtimeFiles = []string{"go.mod", "LICENSE", "NOTICE"}

// versionFile declares Number, from which the version's other constants derive.
const versionFile = "internal/version/version.go"

func main() {
	out := flag.String("o", "dist", "output directory")
	list := flag.String("target", strings.Join(targets, ","), "comma-separated GOOS/GOARCH platforms")
	tag := flag.String("version", "", "version that the executables report, such as v1.2.3; empty keeps internal/version")
	stamp := flag.Bool("stamp", false, "write -version into internal/version in the checkout and build nothing")
	flag.Parse()
	var err error
	if *stamp {
		err = stampCheckout(*tag)
	} else {
		err = release(*out, strings.Split(*list, ","), *tag)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

// stampCheckout writes tag's version into versionFile in the checkout, for a
// release that go install can also build.
func stampCheckout(tag string) error {
	if err := publishable(tag); err != nil {
		return err
	}
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	path := filepath.Join(root, filepath.FromSlash(versionFile))
	source, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	stamped, _, err := stampVersion(source, tag)
	if err != nil {
		return err
	}
	return os.WriteFile(path, stamped, 0644)
}

func release(out string, platforms []string, tag string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	if err := absoluteTemporaryDirectories(); err != nil {
		return err
	}
	temp, err := os.MkdirTemp("", "ddto-release-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	// A relative TMPDIR would name another directory for go commands that run
	// elsewhere, as the wrapper's build does.
	if temp, err = filepath.Abs(temp); err != nil {
		return err
	}
	overlay := map[string]string{}
	overrides := map[string][]byte{}
	prefix := version.BuildLogPrefix
	if tag != "" {
		source, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(versionFile)))
		if err != nil {
			return err
		}
		stamped, stampedPrefix, err := stampVersion(source, tag)
		if err != nil {
			return err
		}
		path := filepath.Join(temp, "version.go")
		if err := os.WriteFile(path, stamped, 0600); err != nil {
			return err
		}
		overlay[filepath.Join(root, filepath.FromSlash(versionFile))] = path
		overrides[versionFile] = stamped
		prefix = stampedPrefix
	}
	archive, err := runtimeArchive(root, overrides)
	if err != nil {
		return err
	}
	overlay[filepath.Join(root, "internal", "bundle", bundle.RuntimeArchive)] = filepath.Join(temp, bundle.RuntimeArchive)
	if err := os.WriteFile(filepath.Join(temp, bundle.RuntimeArchive), archive, 0600); err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0755); err != nil {
		return err
	}
	for _, target := range platforms {
		goos, goarch, ok := strings.Cut(target, "/")
		if !ok {
			return fmt.Errorf("target %q is not GOOS/GOARCH", target)
		}
		path, err := build(root, temp, out, goos, goarch, prefix, overlay)
		if err != nil {
			return err
		}
		fmt.Println(path)
	}
	return nil
}

// absoluteTemporaryDirectories makes a relative TMPDIR, TMP or TEMP absolute,
// for this process and the go commands that it starts in other directories.
func absoluteTemporaryDirectories() error {
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		if dir := os.Getenv(name); dir != "" && !filepath.IsAbs(dir) {
			absolute, err := filepath.Abs(dir)
			if err != nil {
				return err
			}
			if err := os.Setenv(name, absolute); err != nil {
				return err
			}
		}
	}
	return nil
}

// semver accepts vMAJOR.MINOR.PATCH with an optional prerelease, as Go module
// versions do; build metadata is not a valid module version. A numeric
// prerelease identifier has no leading zero, as SemVer requires and Go checks.
var semver = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?$`)

var numberDeclaration = regexp.MustCompile(`(?m)^const Number = "[^"]*"`)

// pseudoVersion is golang.org/x/mod/module's pseudo-version pattern.
var pseudoVersion = regexp.MustCompile(`^v[0-9]+\.(0\.0-|\d+\.\d+-([^+]*\.)?0\.)\d{14}-[A-Za-z0-9]+(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

// publishable rejects a tag that go install could not resolve: the module path
// has no /vN suffix, so only v0 and v1 tags belong to it, and go reads a
// pseudo-version's commit from its name instead of the tag.
func publishable(tag string) error {
	if !semver.MatchString(tag) {
		return fmt.Errorf("version %q is not vMAJOR.MINOR.PATCH[-PRERELEASE]", tag)
	}
	if pseudoVersion.MatchString(tag) {
		return fmt.Errorf("version %q is a Go pseudo-version, which names another commit", tag)
	}
	if !strings.HasPrefix(tag, "v0.") && !strings.HasPrefix(tag, "v1.") {
		return fmt.Errorf("version %q needs the module path to end in /%s", tag, strings.SplitN(tag, ".", 2)[0])
	}
	return nil
}

// stampVersion returns versionFile's source with Number set to tag's version,
// and the build log prefix that the stamped package declares, which is part of
// the tool wrapper's key.
func stampVersion(source []byte, tag string) ([]byte, string, error) {
	if !semver.MatchString(tag) {
		return nil, "", fmt.Errorf("version %q is not vMAJOR.MINOR.PATCH[-PRERELEASE]", tag)
	}
	if len(numberDeclaration.FindAll(source, -1)) != 1 {
		return nil, "", fmt.Errorf("%s does not declare Number once", versionFile)
	}
	if !strings.HasSuffix(version.BuildLogPrefix, version.Tag) {
		return nil, "", fmt.Errorf("version.BuildLogPrefix does not end with version.Tag")
	}
	stamped := numberDeclaration.ReplaceAll(source, []byte(`const Number = "`+strings.TrimPrefix(tag, "v")+`"`))
	return stamped, strings.TrimSuffix(version.BuildLogPrefix, version.Tag) + tag, nil
}

func moduleRoot() (string, error) {
	gomod, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", err
	}
	root := filepath.Dir(strings.TrimSpace(string(gomod)))
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	// A Windows checkout can end the line with a carriage return.
	line, _, _ := bytes.Cut(data, []byte("\n"))
	if err != nil || strings.TrimSpace(string(line)) != "module "+module {
		return "", fmt.Errorf("run from the %s module", module)
	}
	return root, nil
}

// runtimeArchive zips the runtime's sources: the module files, and the non-test
// sources and license texts of runtimeDirs, without testdata. overrides
// replaces files by their slash-separated path. Entries are sorted and carry no
// times, so the same sources give the same archive and the same cache key.
func runtimeArchive(root string, overrides map[string][]byte) ([]byte, error) {
	names := slices.Clone(runtimeFiles)
	for _, dir := range runtimeDirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := entry.Name()
			if entry.IsDir() {
				if name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			// Third-party sources keep their license texts.
			upper := strings.ToUpper(name)
			switch filepath.Ext(name) {
			case ".go", ".s", ".h", ".c":
			default:
				if !strings.HasPrefix(upper, "LICENSE") && !strings.HasPrefix(upper, "NOTICE") {
					return nil
				}
			}
			rel, err := filepath.Rel(root, path)
			names = append(names, filepath.ToSlash(rel))
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	slices.Sort(names)
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	for _, name := range names {
		data, ok := overrides[name]
		if !ok {
			var err error
			if data, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(name))); err != nil {
				return nil, err
			}
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)}
		w, err := archive.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// build writes ddto-GOOS-GOARCH to out. The wrapper comes from the sources
// that ddto would build with prefix, so its key matches; -overlay supplies it,
// the runtime archive and any stamped version without writing to the checkout.
func build(root, temp, out, goos, goarch, prefix string, overlay map[string]string) (string, error) {
	suffix := ""
	if goos == "windows" {
		suffix = ".exe"
	}
	src := filepath.Join(temp, "toolwrap-src")
	if err := os.RemoveAll(src); err != nil {
		return "", err
	}
	files, _ := toolwrap.Sources(prefix)
	for name, data := range files {
		path := filepath.Join(src, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			return "", err
		}
	}
	wrapper := filepath.Join(temp, "toolwrap-"+goos+"-"+goarch)
	env := append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0", "GOFLAGS=", "GOWORK=off")
	if err := run(src, env, "go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w", "-o", wrapper, "."); err != nil {
		return "", err
	}
	replace := map[string]string{filepath.Join(root, "internal", "bundle", bundle.ToolWrapperFile): wrapper}
	for from, to := range overlay {
		replace[from] = to
	}
	data, err := json.Marshal(map[string]map[string]string{"Replace": replace})
	if err != nil {
		return "", err
	}
	overlayFile := filepath.Join(temp, "overlay.json")
	if err := os.WriteFile(overlayFile, data, 0600); err != nil {
		return "", err
	}
	path, err := filepath.Abs(filepath.Join(out, "ddto-"+goos+"-"+goarch+suffix))
	if err != nil {
		return "", err
	}
	err = run(root, env, "go", "build", "-trimpath", "-tags=ddto_release", "-ldflags=-s -w", "-overlay="+overlayFile, "-o", path, "./cmd/ddto")
	return path, err
}

func run(dir string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, env, os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}
