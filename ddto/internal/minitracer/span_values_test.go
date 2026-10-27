package minitracer

import "testing"

func pointerTo[T any](value T) *T { return &value }

// Tag values follow the SDK: every numeric type is a metric, but a 64-bit
// integer beyond float64's exact range is not; a pointer to a basic value
// records the value, and a nil one its zero value.
func TestTagValueConversions(t *testing.T) {
	for _, value := range []any{byte(1), float32(1), float64(1), int(1), int8(1), int16(1), int32(1), int64(1), uint(1), uint16(1), uint32(1), uint64(1)} {
		if got, ok := toFloat64(value); !ok || got != 1 {
			t.Errorf("toFloat64(%T) = %v, %t", value, got, ok)
		}
	}
	for _, value := range []any{int64(1 << 53), int64(-(1 << 53)), uint64(1 << 53), "1", true} {
		if got, ok := toFloat64(value); ok {
			t.Errorf("toFloat64(%T %v) = %v, a metric", value, value, got)
		}
	}
	for _, tc := range []struct{ pointer, want any }{
		{pointerTo(true), true}, {(*bool)(nil), false},
		{pointerTo("x"), "x"}, {(*string)(nil), ""},
		{pointerTo(byte(2)), byte(2)}, {(*byte)(nil), byte(0)},
		{pointerTo(float32(2)), float32(2)}, {(*float32)(nil), float32(0)},
		{pointerTo(2.5), 2.5}, {(*float64)(nil), float64(0)},
		{pointerTo(2), 2}, {(*int)(nil), 0},
		{pointerTo(int8(2)), int8(2)}, {(*int8)(nil), int8(0)},
		{pointerTo(int16(2)), int16(2)}, {(*int16)(nil), int16(0)},
		{pointerTo(int32(2)), int32(2)}, {(*int32)(nil), int32(0)},
		{pointerTo(int64(2)), int64(2)}, {(*int64)(nil), int64(0)},
		{pointerTo(uint(2)), uint(2)}, {(*uint)(nil), uint(0)},
		{pointerTo(uint16(2)), uint16(2)}, {(*uint16)(nil), uint16(0)},
		{pointerTo(uint32(2)), uint32(2)}, {(*uint32)(nil), uint32(0)},
		{pointerTo(uint64(2)), uint64(2)}, {(*uint64)(nil), uint64(0)},
	} {
		if got := dereference(tc.pointer); got != tc.want {
			t.Errorf("dereference(%T) = %#v, want %#v", tc.pointer, got, tc.want)
		}
	}
	other := pointerTo(struct{}{})
	if got := dereference(other); got != any(other) {
		t.Errorf("dereference kept no other pointer: %#v", got)
	}
}
