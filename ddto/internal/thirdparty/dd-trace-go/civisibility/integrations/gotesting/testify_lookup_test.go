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

type firstScopedSuite struct{}

func (*firstScopedSuite) TestShared() {}

type secondScopedSuite struct{}

func (*secondScopedSuite) TestShared() {}

func TestTestifyScopeRestoresAndBindsMethods(t *testing.T) {
	outer := registerTestifySuiteScope(t, new(firstScopedSuite))
	t.Run("TestShared", func(method *testing.T) {
		data := getTestifyTest(method)
		if data == nil || data.suite != reflect.TypeOf(new(firstScopedSuite)) {
			t.Fatalf("first: %+v", data)
		}
		inner := registerTestifySuiteScope(t, new(secondScopedSuite))
		method.Run("nested", func(child *testing.T) {
			fast := getTestifyTest(child)
			slow := getTestifyTestFromReflectValue(reflect.ValueOf(child))
			if fast == nil || slow == nil || fast.suite != data.suite || slow.suite != data.suite {
				t.Fatal("nested child lost bound method")
			}
		})
		inner()
	})
	nested := registerTestifySuiteScope(t, new(secondScopedSuite))
	t.Run("TestShared", func(method *testing.T) {
		data := getTestifyTest(method)
		if data == nil || data.suite != reflect.TypeOf(new(secondScopedSuite)) {
			t.Fatalf("duplicate: %+v", data)
		}
	})
	nested()
	outer()
	t.Run("TestShared", func(method *testing.T) {
		if getTestifyTest(method) != nil {
			t.Fatal("ordinary sibling inherited a finished suite")
		}
	})
}

func TestTestifyMethodBindingOutlivesSuiteRun(t *testing.T) {
	end := registerTestifySuiteScope(t, new(firstScopedSuite))
	t.Run("TestShared", func(method *testing.T) {
		want := getTestifyTest(method)
		method.Parallel()
		method.Run("nested", func(child *testing.T) {
			got := getTestifyTest(child)
			if want == nil || got == nil || got.suite != want.suite {
				child.Fatal("parallel child lost the finished suite")
			}
		})
	})
	end()
	end = registerTestifySuiteScope(t, new(secondScopedSuite))
	t.Run("TestShared", func(method *testing.T) {
		got := getTestifyTest(method)
		if got == nil || got.suite != reflect.TypeOf(new(secondScopedSuite)) {
			method.Fatal("second suite attribution lost")
		}
	})
	end()
}
