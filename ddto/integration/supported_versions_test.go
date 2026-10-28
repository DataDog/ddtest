package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/ddto/internal/instrument"
	"github.com/DataDog/ddtest/ddto/internal/runner"
)

// supportedVersions is the window of dd-trace-go and Orchestrion versions that
// the CLI supports: scripts/supported_versions.json, or the window in
// DDTO_SUPPORTED_VERSIONS. The nightly run and the check before a release set
// it to the one that the module proxy has then, from supported_versions.py
// resolve.
type supportedVersions struct {
	SDK         supportedModule `json:"dd-trace-go"`
	Orchestrion supportedModule `json:"orchestrion"`
}

type supportedModule struct {
	Module   string   `json:"module"`
	Releases []string `json:"releases"`
	Main     string   `json:"main"`
}

// versions lists main first, then the releases from the newest.
func (m supportedModule) versions() []string {
	versions := []string{m.Main}
	for _, release := range slices.Backward(m.Releases) {
		versions = append(versions, release)
	}
	return versions
}

func loadSupportedVersions(t *testing.T) supportedVersions {
	t.Helper()
	source, data := "DDTO_SUPPORTED_VERSIONS", []byte(os.Getenv("DDTO_SUPPORTED_VERSIONS"))
	if len(data) == 0 {
		source = filepath.Join("..", "scripts", "supported_versions.json")
		var err error
		if data, err = os.ReadFile(source); err != nil {
			t.Fatal(err)
		}
	}
	var window supportedVersions
	if err := json.Unmarshal(data, &window); err != nil || window.SDK.Main == "" || len(window.SDK.Releases) == 0 || window.Orchestrion.Main == "" || len(window.Orchestrion.Releases) == 0 {
		t.Fatalf("supported versions in %s: %v", source, err)
	}
	return window
}

// fullCompatibility is the check before a release: every version of the
// window runs, even one whose edited SDK packages another version shares.
func fullCompatibility() bool { return os.Getenv("DDTO_COMPATIBILITY") == "full" }

// moduleDir downloads module@version into the module cache and returns its
// directory.
func moduleDir(t *testing.T, module, version string) string {
	t.Helper()
	out, stderr, code := command(t, t.TempDir(), testEnv(), "go", "mod", "download", "-json", module+"@"+version)
	if code != 0 {
		t.Fatal(out, stderr)
	}
	var downloaded struct{ Dir string }
	if err := json.Unmarshal([]byte(out), &downloaded); err != nil || downloaded.Dir == "" {
		t.Fatal(out, err)
	}
	return downloaded.Dir
}

// sdkEditedPackages are the SDK packages that the CLI edits.
var sdkEditedPackages = []string{"internal/config", "internal/civisibility/envconfig", "ddtrace/tracer"}

// sdkFingerprint hashes what the CLI's SDK edits and their behavior depend on:
// the non-test Go files of the packages they edit, and the module's go.mod.
func sdkFingerprint(t *testing.T, dir string) string {
	t.Helper()
	hash := sha256.New()
	for _, pkg := range sdkEditedPackages {
		entries, err := os.ReadDir(filepath.Join(dir, filepath.FromSlash(pkg)))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if name := entry.Name(); strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
				data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(pkg), name))
				if err != nil {
					t.Fatal(err)
				}
				fmt.Fprintf(hash, "%s/%s\x00%d\x00", pkg, name, len(data))
				hash.Write(data)
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	hash.Write(data)
	return hex.EncodeToString(hash.Sum(nil))
}

// sdkWindow is the pinned SDK and every supported SDK version.
func sdkWindow(t *testing.T) []string {
	return append([]string{sdkVersion}, loadSupportedVersions(t).SDK.versions()...)
}

// sdkRuntimeVersions are the SDK versions whose behavior the suite runs: the
// pinned SDK, then one version for each distinct fingerprint of the window,
// the newest. Versions with the same fingerprint compile the same edited
// packages; TestMiniSDKSupportedVersions still checks each one's edits.
func sdkRuntimeVersions(t *testing.T) []string {
	t.Helper()
	module := loadSupportedVersions(t).SDK.Module
	seen := map[string]string{}
	var versions []string
	for _, version := range sdkWindow(t) {
		fingerprint := sdkFingerprint(t, moduleDir(t, module, version))
		if same, ok := seen[fingerprint]; ok && !fullCompatibility() {
			t.Logf("SDK %s edits the same packages as %s", version, same)
			continue
		}
		seen[fingerprint] = version
		versions = append(versions, version)
	}
	return versions
}

// Every supported SDK version accepts every edit, as its compiles apply them:
// the CI guards, the span copies and the edits that keep its spans under
// tests out of APM. These checks only parse the SDK, so they run for each
// version, including those that sdkRuntimeVersions skips.
func TestMiniSDKSupportedVersions(t *testing.T) {
	module := loadSupportedVersions(t).SDK.Module
	for _, version := range sdkWindow(t) {
		t.Run(version, func(t *testing.T) {
			found, err := runner.SDKEdits(moduleDir(t, module, version))
			if want := instrument.SDKMirrorCopies | instrument.SDKMirrorAdoption; err != nil || found != want {
				t.Fatalf("edits found %d, want %d: %v", found, want, err)
			}
		})
	}
}
