package gotesting

import (
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"
	"unsafe"
)

func TestTestingFReflectionLayout(t *testing.T) {
	t.Helper()
	f := &testing.F{}
	typ := reflect.TypeFor[testing.F]()
	common, ok := typ.FieldByName("common")
	if !ok || common.Offset != 0 || common.Type != reflect.TypeFor[testing.T]().Field(0).Type {
		t.Fatal("testing.F must embed testing.common at offset zero for shared lifecycle fields")
	}
	state, ok := typ.FieldByName("tstate")
	tState, tStateOK := reflect.TypeFor[testing.T]().FieldByName("tstate")
	if !ok || !tStateOK || state.Type.Kind() != reflect.Pointer || state.Type != tState.Type {
		t.Fatal("testing.F.tstate must have the same pointer type as testing.T.tstate")
	}
	statePointer, err := getFieldPointerFromWithType(f, "tstate", state.Type)
	if err != nil || statePointer == nil {
		t.Fatalf("testing.F.tstate is unreadable: %v", err)
	}
	wantState := &testingTestState{}
	*(**testingTestState)(statePointer) = wantState
	if getFuzzTestState(f) != wantState {
		t.Fatal("testing.F scheduler state lookup must use the F-specific field offset")
	}
	fuzzCalled, err := getFieldPointerFromWithType(f, "fuzzCalled", reflect.TypeFor[bool]())
	if err != nil || fuzzCalled == nil {
		t.Fatalf("testing.F.fuzzCalled must be a readable bool: %v", err)
	}
	if testingFFuzzCalled(f) {
		t.Fatal("a new testing.F must not have called F.Fuzz")
	}
	*(*bool)(fuzzCalled) = true
	if !testingFFuzzCalled(f) {
		t.Fatal("testing.F.fuzzCalled lookup must observe F.Fuzz completion")
	}
	if getInternalFuzzTargetArray(&testing.M{}) == nil || getInternalExampleArray(&testing.M{}) == nil {
		t.Fatal("testing.M fuzz target and example descriptors must retain their expected slice types")
	}
	for _, native := range []any{f, &testing.T{}} {
		if ptr, err := getFieldPointerFromWithType(native, "duration", reflect.TypeFor[time.Duration]()); err != nil || ptr == nil {
			t.Fatalf("%T.duration must retain its time.Duration type: %v", native, err)
		}
	}
	runtime.KeepAlive(f)
}

// Measure result collection separately from event serialization/network work.
func BenchmarkFuzzNativeResult(b *testing.B) {
	for _, kind := range []string{"root", "seed"} {
		b.Run(kind, func(b *testing.B) {
			var native testing.TB = &testing.T{}
			if kind == "root" {
				native = &testing.F{}
			}
			ptr, err := getFieldPointerFromWithType(native, "duration", reflect.TypeFor[time.Duration]())
			if err != nil {
				b.Fatal(err)
			}
			*(*time.Duration)(ptr) = 23 * time.Millisecond
			event := fuzzTestEvent{native: native}
			b.ReportAllocs()
			for b.Loop() {
				_, _, duration := event.nativeResult(true)
				if duration != 23*time.Millisecond {
					b.Fatal(duration)
				}
			}
		})
	}
}

func TestFuzzOffsetsRejectLayoutDrift(t *testing.T) {
	layout := getTestingInternalsLayout()
	if !layout.fuzz.commonOK || !layout.fuzz.state.available || !layout.fuzz.fuzzCalled.available {
		t.Fatal("supported Go toolchain must provide all F fields")
	}
	common := reflect.StructField{Name: "common", PkgPath: "gotesting", Type: layout.tCommon.typ}
	state := reflect.StructField{Name: "tstate", PkgPath: "gotesting", Type: layout.tstate.typ}
	called := reflect.StructField{Name: "fuzzCalled", PkgPath: "gotesting", Type: reflect.TypeFor[bool]()}
	for _, tc := range []struct {
		name                        string
		fields                      []reflect.StructField
		commonOK, stateOK, calledOK bool
	}{
		{"valid", []reflect.StructField{common, state, called}, true, true, true},
		{"missing-common", []reflect.StructField{state, called}, false, false, false},
		{"moved-common", []reflect.StructField{{Name: "Padding", Type: reflect.TypeFor[int]()}, common, state, called}, false, false, false},
		{"wrong-common", []reflect.StructField{{Name: "common", PkgPath: "gotesting", Type: reflect.TypeFor[int]()}, state, called}, false, false, false},
		{"wrong-state", []reflect.StructField{common, {Name: "tstate", PkgPath: "gotesting", Type: reflect.TypeFor[*int]()}, called}, true, false, true},
		{"wrong-called", []reflect.StructField{common, state, {Name: "fuzzCalled", PkgPath: "gotesting", Type: reflect.TypeFor[int]()}}, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := buildFuzzFieldsLayout(reflect.StructOf(tc.fields), layout)
			if got.commonOK != tc.commonOK || got.state.available != tc.stateOK || got.fuzzCalled.available != tc.calledOK {
				t.Fatalf("layout=%+v", got)
			}
		})
	}
	for _, typ := range []reflect.Type{nil, reflect.TypeFor[int]()} {
		if got := buildFuzzFieldsLayout(typ, layout); got.commonOK {
			t.Fatal("accepted non-struct F layout")
		}
	}
	if got := buildFuzzFieldsLayout(reflect.TypeFor[testing.F](), &testingInternalsLayout{disabled: true}); got.commonOK {
		t.Fatal("accepted invalid common layout")
	}
}

func TestFuzzFatalResultDoesNotReadActiveDuration(t *testing.T) {
	layout := getTestingInternalsLayout()
	native := &testing.F{}
	base := fuzzCommonBase(native, layout)
	duration := fieldPtr[time.Duration](base, layout.common.duration)
	mu := fieldPtr[sync.RWMutex](base, layout.common.mu)
	mu.Lock()
	*fieldPtr[bool](base, layout.common.failed) = true
	mu.Unlock()
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range 10000 {
			*duration = time.Duration(i)
		}
	})
	event := fuzzTestEvent{native: native}
	for range 10000 {
		failed, skipped, got := event.nativeResult(false)
		if !failed || skipped || got != 0 {
			t.Fatalf("fatal result=%t/%t/%s", failed, skipped, got)
		}
	}
	wg.Wait()
	runtime.KeepAlive(unsafe.Pointer(native))
}
