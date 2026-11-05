package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T, files map[string]string) string {
	t.Helper()
	t.Setenv("GOFLAGS", "") // The reference fixture must use the native compiler.
	t.Setenv("GOWORK", "off")
	root := t.TempDir()
	files["go.mod"] = "module example.com/inventory\n\ngo 1.24\n"
	for path, source := range files {
		require.NoError(t, os.WriteFile(filepath.Join(root, path), []byte(source), 0600))
	}
	return root
}

func TestSourceInventoryMatchesGoTest(t *testing.T) {
	otherOS := "windows"
	if runtime.GOOS == otherOS {
		otherOS = "linux"
	}
	root := fixture(t, map[string]string{
		"inventory.go": "package inventory\nfunc Value() {}\nfunc WithoutOutput() {}\ntype Widget struct{}\n",
		"inventory_test.go": `package inventory
import ("fmt"; "os"; tst "testing")
func TestMain(m *tst.M) { os.Exit(m.Run()) }
func Test(t *tst.T) {}; func TestSecond(t *tst.T) {}
func TestÉ(t *tst.T) {}
func Testlowercase(t *tst.T) {}
func (Widget) TestMethod(t *tst.T) {}
func BenchmarkIgnored(b *tst.B) {}
func FuzzValue(f *tst.F) { f.Add("seed"); f.Fuzz(func(t *tst.T, s string) {}) }
func ExampleValue() {
 fmt.Println("value")
 // Output: value
}
func ExampleValue_empty() {
 // Output:
}
func ExampleWithoutOutput() {}
var decoy = "func TestNotADeclaration(t *testing.T) {}"
`,
		"external_test.go": `package inventory_test
import . "testing"
func TestExternal(t *T) {}
func TestMain(t *T) {}
`,
		"race_test.go":                     "//go:build race\n\npackage inventory\nimport \"testing\"\nfunc TestRace(t *testing.T) {}\n",
		"native_test.go":                   "//go:build !race\n\npackage inventory\nimport \"testing\"\nfunc TestNotRace(t *testing.T) {}\n",
		"tagged_test.go":                   "//go:build ddtest_extra_fixture\n\npackage inventory\nimport \"testing\"\nfunc TestTagged(t *testing.T) {}\n",
		"platform_" + otherOS + "_test.go": "package inventory\nimport \"testing\"\nfunc TestOtherOS(t *testing.T) {}\n",
	})
	for _, tags := range []string{"", "-tags=ddtest_extra_fixture"} {
		t.Run(tags, func(t *testing.T) {
			t.Setenv("GOFLAGS", tags)
			names, err := discover(t.Context(), root)
			require.NoError(t, err)
			// Vet warns about the intentionally ignored Testlowercase declaration.
			command := exec.CommandContext(t.Context(), "go", "test", "-vet=off", "-race", "-list", ".", "./...")
			command.Dir = root
			output, err := command.CombinedOutput()
			require.NoError(t, err, "%s", output)
			var compiled []string
			for _, line := range strings.Split(string(output), "\n") {
				if len(strings.Fields(line)) == 1 && (strings.HasPrefix(line, "Test") || strings.HasPrefix(line, "Fuzz") || strings.HasPrefix(line, "Example")) {
					compiled = append(compiled, line)
				}
			}
			slices.Sort(compiled)
			require.Equal(t, compiled, names)
			require.Contains(t, names, "TestRace")
			require.Contains(t, names, "TestMain")
			require.Contains(t, names, "ExampleValue_empty")
			require.NotContains(t, names, "TestNotRace")
			require.NotContains(t, names, "TestOtherOS")
			require.Equal(t, tags != "", slices.Contains(names, "TestTagged"))
		})
	}
}

func TestDiscoveryDoesNotBuildOrRunTheTestBinary(t *testing.T) {
	root := fixture(t, map[string]string{
		"inventory.go":      "package inventory\nfunc broken() { notDefined() }\nfunc init() { panic(\"initializer ran\") }\n",
		"inventory_test.go": "package inventory\nimport \"testing\"\nfunc TestMain(m *testing.M) { panic(\"TestMain ran\") }\nfunc TestSelected(t *testing.T) {}\n",
	})
	names, err := discover(t.Context(), root)
	require.NoError(t, err)
	require.Equal(t, []string{"TestSelected"}, names)
}

func TestInvalidTestDeclarationsFailDiscovery(t *testing.T) {
	for _, declaration := range []string{
		"func TestMissingArgument() {}",
		"func TestWrongArgument(t *testing.B) {}",
		"func TestResult(t *testing.T) int { return 0 }",
		"func TestGeneric[T any](t *testing.T) {}",
		"func FuzzWrongArgument(t *testing.T) {}",
	} {
		t.Run(declaration, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid_test.go")
			require.NoError(t, os.WriteFile(path, []byte("package inventory\nimport \"testing\"\n"+declaration), 0600))
			_, err := fileTests(path)
			require.ErrorContains(t, err, "invalid signature")
		})
	}
}

func TestGoListFailureCannotProduceAnEmptySuccessfulInventory(t *testing.T) {
	root := fixture(t, map[string]string{"inventory.go": "package inventory\nimport \"unterminated\n"})
	_, err := discover(t.Context(), root)
	require.Error(t, err)
}
