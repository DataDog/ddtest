// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"
)

// TestifyTest is a struct that stores the information about a test method from a Testify suite.
type TestifyTest struct {
	methodName string
	suiteName  string
	moduleName string
	methodFunc *runtime.Func
	suite      reflect.Type
}

// moduleAndSuite returns the suite's module and suite names, or the given
// names for a test outside a Testify suite.
func (t *TestifyTest) moduleAndSuite(moduleName, suiteName string) (string, string) {
	if t == nil {
		return moduleName, suiteName
	}
	return t.moduleName, t.suiteName
}

var (
	// testifyTestsByParentT is a map that stores the TestifyTest structs for each parent T.
	testifyTestsByParentT = map[unsafe.Pointer][]TestifyTest{}

	// testifyTestsByParentTMutex is a mutex to protect the testifyTestsByParentT map.
	testifyTestsByParentTMutex sync.RWMutex
)

// getTestifyTest returns the TestifyTest struct for the given *testing.T.
// Tests skip the lookup until a suite registers. The parent walk uses the
// validated testing offsets, like the other private field accesses; reflection
// remains the fallback for unsupported testing layouts.
func getTestifyTest(t *testing.T) *TestifyTest {
	if !testifySuitesRegistered.Load() {
		return nil
	}
	layout := getTestingInternalsLayout()
	if layout == nil || layout.disabled || !layout.testFieldsOK {
		return getTestifyTestFromReflectValue(reflect.ValueOf(t))
	}
	var found *TestifyTest
	for base := commonBaseForTest(t, layout); base != nil; {
		parent := pointerWord(base, layout.common.parent)
		if parent == nil {
			break
		}
		// suite.Run registers the parent T; this level is the suite method test.
		if tests, ok := getTestifyTestsByParentT(parent); ok {
			found = findTestifyTest(tests, *fieldPtr[string](base, layout.common.name))
			break
		}
		base = parent
	}
	runtime.KeepAlive(t)
	return found
}

// testifySuitesRegistered lets ordinary tests skip the parent walk.
var testifySuitesRegistered atomic.Bool

// findTestifyTest matches the test name's final "/<method>" element without
// building the suffix string.
func findTestifyTest(tests []TestifyTest, name string) *TestifyTest {
	for i := range tests {
		method := tests[i].methodName
		if len(name) > len(method) && strings.HasSuffix(name, method) && name[len(name)-len(method)-1] == '/' {
			test := tests[i]
			return &test
		}
	}
	return nil
}

// getTestifyTestFromReflectValue returns the TestifyTest struct for the given reflect.Value (*testing.T or *common for the parent T).
func getTestifyTestFromReflectValue(tValue reflect.Value) *TestifyTest {
	// check if the reflect.Value is valid
	if !tValue.IsValid() || tValue.IsZero() || tValue.IsNil() {
		return nil
	}
	// get the parent field for testing.T or common
	member := reflect.Indirect(tValue).FieldByName("parent")
	if !member.IsValid() && !member.IsNil() {
		return nil
	}
	memberPtr := unsafe.Pointer(member.UnsafeAddr())

	// let's check if the test parent was registered before (`suite.Run(*testing.T, TestSuite)` auto-instrumentation should register the parent T with the suite instance)
	if tests, ok := getTestifyTestsByParentT(*(*unsafe.Pointer)(memberPtr)); ok {
		// get the name of the test (not the parent)
		var tName string
		if ptr, err := getFieldPointerFromValue(reflect.Indirect(tValue), "name"); err == nil && ptr != nil {
			tName = *(*string)(ptr)
		} else {
			return nil
		}

		// let's find the TestifyTest struct for the current test
		return findTestifyTest(tests, tName)
	} else if member.IsValid() && !member.IsZero() && !member.IsNil() {
		// if the parent T was not registered, let's try to find the TestifyTest struct for the parent T
		// this is required for subtests
		return getTestifyTestFromReflectValue(member)
	}

	return nil
}

// registerTestifySuite registers the Testify suite with the given *testing.T.
func registerTestifySuite(t *testing.T, suite any) {
	// check if the *testing.T and the suite are valid
	if t == nil || suite == nil {
		return
	}

	// get the reflect.Type of the suite
	methodFinder := reflect.TypeOf(suite)
	suiteReflect := methodFinder.Elem()

	// get the suite name and module name
	suiteName := suiteReflect.Name()
	moduleName := suiteReflect.PkgPath()

	// get the parent T pointer
	tPtr := reflect.ValueOf(t).UnsafePointer()

	// get the TestifyTest structs for the parent T in case is not the first Suite registration for the test
	var tests []TestifyTest
	if tmpTests, ok := getTestifyTestsByParentT(tPtr); ok {
		tests = tmpTests
	}

	// check if we already processed the suite
	for _, test := range tests {
		if test.suite == methodFinder {
			// the suite was already registered
			return
		}
	}

	// iterate over the methods of the suite to find the Test methods
	for method := range methodFinder.Methods() {

		// get the name for the method
		methodName := method.Name

		// filter out non Test methods
		if !strings.HasPrefix(methodName, "Test") {
			continue
		}

		// get the file for the method
		methodFunc := runtime.FuncForPC(uintptr(method.Func.UnsafePointer()))
		var methodFile string
		if methodFunc != nil {
			methodFile, _ = methodFunc.FileLine(methodFunc.Entry())
		}

		// append the TestifyTest struct to the tests slice
		tests = append(tests, TestifyTest{
			methodName: methodName,
			suiteName:  fmt.Sprintf("%s/%s", filepath.Base(methodFile), suiteName),
			moduleName: moduleName,
			methodFunc: methodFunc,
			suite:      methodFinder,
		})
	}

	// store the TestifyTest structs for the parent T
	setTestifyTestsByParentT(tPtr, tests)
}

func getTestifyTestsByParentT(ptr unsafe.Pointer) ([]TestifyTest, bool) {
	testifyTestsByParentTMutex.RLock()
	defer testifyTestsByParentTMutex.RUnlock()
	if tests, ok := testifyTestsByParentT[ptr]; ok {
		return tests, true
	}
	return nil, false
}

func setTestifyTestsByParentT(ptr unsafe.Pointer, tests []TestifyTest) {
	testifyTestsByParentTMutex.Lock()
	defer testifyTestsByParentTMutex.Unlock()
	testifyTestsByParentT[ptr] = tests
	testifySuitesRegistered.Store(true)
}
