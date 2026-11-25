package bazeldoctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Without roots in the request, the doctor computes the Python doctor's
// _execroot_roots() and _output_base_roots().
func TestDefaultArtifactRoots(t *testing.T) {
	posixOnly(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outputBase := filepath.Join(root, "output_base")
	execroot := filepath.Join(outputBase, "execroot", "_main")
	cwd := filepath.Join(execroot, "bazel-out", "k8-fastbuild", "bin", "pkg")
	for _, dir := range []string{filepath.Join(execroot, "external"), cwd, filepath.Join(root, "configured")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The execroot is found through a link, as Bazel's convenience symlinks lead to it.
	link := filepath.Join(root, "bazel-workspace")
	if err := os.Symlink(execroot, link); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(link, "bazel-out", "k8-fastbuild", "bin", "pkg"))
	t.Setenv("DD_TEST_OPTIMIZATION_DOCTOR_EXECROOT", filepath.Join(root, "configured"))
	t.Setenv("DD_TEST_OPTIMIZATION_BAZEL_OUTPUT_BASE", filepath.Join(root, "missing"))
	d := newTestDoctor(t, Args{})
	if got, want := strings.Join(d.execrootRoots(), ","), strings.Join([]string{filepath.Join(root, "configured"), cwd, execroot}, ","); got != want {
		t.Fatalf("execroot roots %s, want %s", got, want)
	}
	if got := strings.Join(d.outputBaseRoots(), ","); got != outputBase {
		t.Fatalf("output base roots %s, want %s", got, outputBase)
	}
	d.request.ExecrootRoots, d.request.OutputBaseRoots = []string{"/given"}, []string{"/base"}
	if d.execrootRoots()[0] != "/given" || d.outputBaseRoots()[0] != "/base" {
		t.Fatal("the request's roots were replaced")
	}
}
