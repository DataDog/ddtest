package integration

import (
	"reflect"
	"testing"
)

func TestDecodeWirePayload(t *testing.T) {
	got, err := decodeMsgpack([]byte{0x81, 0xa1, 'x', 0x92, 0x01, 0xc3})
	want := map[string]any{"x": []any{uint64(1), true}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, %v", got, err)
	}
	for _, raw := range [][]byte{{0xc1}, {0xd9, 4, 'x'}, {0x81}} {
		if _, err := decodeMsgpack(raw); err == nil {
			t.Fatalf("invalid payload accepted: %x", raw)
		}
	}
}
