package bazel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSourceRlocation(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"_main/pkg/a_test.go":           "main",
		"repo+/lib/b_test.go":           "external",
		"elsewhere/_main/pkg/c_test.go": "manifest",
	}
	for name, data := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := filepath.Join(root, "MANIFEST")
	if err := os.WriteFile(manifest, []byte("_main/pkg/c_test.go "+filepath.Join(root, "elsewhere", "_main", "pkg", "c_test.go")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	read := func(path string) string {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	for _, tc := range []struct {
		env      map[string]string
		logical  string
		want     string
		resolved bool
	}{
		{map[string]string{"TEST_WORKSPACE": "_main", "RUNFILES_DIR": root}, "pkg/a_test.go", "main", true},
		{map[string]string{"TEST_WORKSPACE": "_main", "TEST_SRCDIR": root}, "pkg/a_test.go", "main", true},
		{map[string]string{"TEST_WORKSPACE": "_main", "RUNFILES_DIR": root}, "external/repo+/lib/b_test.go", "external", true},
		{map[string]string{"TEST_WORKSPACE": "_main", "RUNFILES_MANIFEST_FILE": manifest}, "pkg/c_test.go", "manifest", true},
		{map[string]string{"TEST_WORKSPACE": "_main", "RUNFILES_DIR": root}, "pkg/missing_test.go", "", false},
		{map[string]string{"TEST_WORKSPACE": "_main", "RUNFILES_DIR": root}, "../_main/pkg/a_test.go", "", false},
		{map[string]string{"TEST_WORKSPACE": "_main", "RUNFILES_DIR": root}, filepath.Join(root, "_main", "pkg", "a_test.go"), "", false},
		{map[string]string{"RUNFILES_DIR": root}, "pkg/a_test.go", "", false},
	} {
		for _, name := range []string{"TEST_WORKSPACE", "RUNFILES_DIR", "TEST_SRCDIR", "RUNFILES_MANIFEST_FILE"} {
			t.Setenv(name, tc.env[name])
		}
		got, ok := SourceRlocation(tc.logical)
		if ok != tc.resolved || ok && read(got) != tc.want {
			t.Errorf("%v %s: got %q %t", tc.env, tc.logical, got, ok)
		}
	}
}
