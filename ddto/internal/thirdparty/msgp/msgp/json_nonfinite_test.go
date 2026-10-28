package msgp

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"
)

// JSON has no NaN or infinity: CopyToJSON writes them as null, so the rest of
// the document, such as a Bazel payload file, stays valid.
func TestCopyJSONNonFiniteFloats(t *testing.T) {
	var data []byte
	data = AppendMapHeader(data, 5)
	data = AppendString(data, "nan")
	data = AppendFloat64(data, math.NaN())
	data = AppendString(data, "inf")
	data = AppendFloat64(data, math.Inf(1))
	data = AppendString(data, "ninf32")
	data = AppendFloat32(data, float32(math.Inf(-1)))
	data = AppendString(data, "nan32")
	data = AppendFloat32(data, float32(math.NaN()))
	data = AppendString(data, "value")
	data = AppendFloat64(data, 1.5)
	var buf bytes.Buffer
	if _, err := CopyToJSON(&buf, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if !json.Valid(buf.Bytes()) || json.Unmarshal(buf.Bytes(), &got) != nil {
		t.Fatalf("invalid JSON: %s", buf.Bytes())
	}
	if got["nan"] != nil || got["inf"] != nil || got["ninf32"] != nil || got["nan32"] != nil || got["value"] != 1.5 {
		t.Fatalf("decoded %v", got)
	}
}
