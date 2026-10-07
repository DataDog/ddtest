package gotesting

import (
	"reflect"
	"runtime"
	"testing"
	"unsafe"
)

// F embeds the same common value as T, but its scheduler pointer has a different
// offset. Discover and validate its fields with the existing process-wide layout.
type fuzzFieldsLayout struct {
	common     unsafeField
	state      unsafeField
	fuzzCalled unsafeField
	commonOK   bool
}

func buildFuzzFieldsLayout(fType reflect.Type, testingLayout *testingInternalsLayout) fuzzFieldsLayout {
	var layout fuzzFieldsLayout
	if fType == nil || fType.Kind() != reflect.Struct || testingLayout == nil || testingLayout.disabled || !testingLayout.tCommon.available {
		return layout
	}
	common, ok := exactField(fType, "common", testingLayout.tCommon.typ, false)
	if !ok || common.offset != 0 {
		return layout
	}
	layout.common, layout.commonOK = common, true
	layout.fuzzCalled, _ = exactField(fType, "fuzzCalled", reflect.TypeFor[bool](), false)
	if testingLayout.tstate.available {
		layout.state, _ = exactField(fType, "tstate", testingLayout.tstate.typ, false)
	}
	return layout
}

func fuzzCommonBase(f *testing.F, layout *testingInternalsLayout) unsafe.Pointer {
	if f == nil || !layout.fuzz.commonOK {
		return nil
	}
	base := unsafe.Add(unsafe.Pointer(f), layout.fuzz.common.offset)
	runtime.KeepAlive(f)
	return base
}
