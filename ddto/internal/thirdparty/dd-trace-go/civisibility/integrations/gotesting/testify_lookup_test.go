package gotesting

import (
	"reflect"
	"testing"
	"unsafe"
)

// The offset-based Testify lookup must resolve exactly what the reflective
// lookup resolves: the suite method, its nested subtests, an unmatched sibling
// and an ordinary test.
func TestTestifyLookupMatchesReflection(t *testing.T) {
	if layout := getTestingInternalsLayout(); layout == nil || layout.disabled || !layout.testFieldsOK {
		t.Skip("testing layout unsupported; only the reflective lookup runs")
	}
	check := func(t *testing.T, want string) {
		t.Helper()
		fast := getTestifyTest(t)
		slow := getTestifyTestFromReflectValue(reflect.ValueOf(t))
		name := func(test *TestifyTest) string {
			if test == nil {
				return ""
			}
			return test.methodName
		}
		if name(fast) != want || name(slow) != want {
			t.Fatalf("%s: offsets=%q reflection=%q want=%q", t.Name(), name(fast), name(slow), want)
		}
	}
	t.Run("Suite", func(suiteT *testing.T) {
		parent := unsafe.Pointer(suiteT)
		setTestifyTestsByParentT(parent, []TestifyTest{{methodName: "TestMethod", suiteName: "Suite", moduleName: "module"}})
		defer func() {
			testifyTestsByParentTMutex.Lock()
			delete(testifyTestsByParentT, parent)
			testifyTestsByParentTMutex.Unlock()
		}()
		suiteT.Run("TestMethod", func(methodT *testing.T) {
			check(methodT, "TestMethod")
			methodT.Run("nested", func(nestedT *testing.T) {
				check(nestedT, "TestMethod")
			})
		})
		suiteT.Run("XTestMethod", func(otherT *testing.T) { check(otherT, "") })
	})
	t.Run("ordinary", func(plainT *testing.T) { check(plainT, "") })
}

func TestFindTestifyTestMatchesFinalElement(t *testing.T) {
	tests := []TestifyTest{{methodName: "TestA"}, {methodName: "TestAB"}}
	for name, want := range map[string]string{
		"Suite/TestA":        "TestA",
		"Suite/TestAB":       "TestAB",
		"Suite/XTestA":       "",
		"TestA":              "",
		"Suite/TestA/nested": "",
	} {
		got := ""
		if test := findTestifyTest(tests, name); test != nil {
			got = test.methodName
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}
