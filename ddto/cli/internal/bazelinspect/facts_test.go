package bazelinspect

import (
	"os"
	"path/filepath"
	"testing"
)

// compareFacts requires the indexed reading of payload to equal the decoded
// one.
func compareFacts(t testing.TB, payload string) Payload {
	t.Helper()
	path := filepath.Join(t.TempDir(), "payload.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	indexed, decoded := decodePayload(path), decodedPayload(path)
	if indexed != decoded {
		t.Fatalf("payload %q\nindexed %+v\ndecoded %+v", payload, indexed, decoded)
	}
	return indexed
}

func TestIndexedFactsMatchDecoding(t *testing.T) {
	session := `{"type":"test_session_end","content":{"test_session_id":%s,"error":%s,"meta":{"test.session.empty_reason":"zero_tests","test.status":"skip"},"metrics":{"test.exit_code":%s}}}`
	format := func(id, failed, exit string) string {
		return `{"events":[` + replaceAll(session, id, failed, exit) + `]}`
	}
	cases := map[string]bool{
		`{"events":[{"type":"test","content":{}}]}`: true,
		`{"events":[{"type":"test","content":[]}]}`: true,
		`{"events":[{"type":"span","content":{"type":"test"}},{"content":{"type":"test"}},{"type":null,"content":{"type":"test"}}]}`: true,
		`{"events":[{"type":"span","content":{"type":"other"}}, 1, "x", null, []]}`:                                                  true,
		`{"events":[{"type":"te\u0073t","content":{}}]}`:                                                                             true,
		`{"events":[{"type":"x","type":"test","content":{}}],"events":[]}`:                                                           true,
		`{"events":{"a":1}}`: true,
		`{"events":[]}`:      true,
		`[1,2]`:              true,
		`"text"`:             true,
		` null `:             true,
		"\ufeff{\"events\":[{\"type\":\"test\",\"content\":{}}]}": true,
		format("7", "0", "0"):       true,
		format("7", "false", "0.0"): true,
		format("7", "0", "-0"):      true,
		format("7", "0", "1e-400"):  true,
		format("0", "0", "0"):       true,
		format("7.0", "0", "0"):     true,
		format(`"7"`, "0", "0"):     true,
		format("7", "1", "0"):       true,
		format("7", "0", `"0"`):     true,
		`{"events":[{"type":"test_session_end","content":{"test_session_id":7,"error":0,"meta":[],"metrics":{"test.exit_code":0}}}]}`:                               true,
		`{"events":[{"type":"test_session_end","content":{"test_session_id":7,"error":0,"meta":{"test.session.empty_reason":"zero_tests","test.status":"skip"}}}]}`: true,
		`{"events":[`:                    false,
		`{"events":[{"type":"te\qst"}]}`: false,
		"{\"a\":\"\x01\"}":               false,
		`{"n":01}`:                       false,
		`{"n":-}`:                        false,
		`{"a":1} x`:                      false,
		`{"a":NaN}`:                      false,
		"{\"a\":\"\xff\"}":               false,
		`{"a":1}{"b":2}`:                 false,
	}
	for payload, decoded := range cases {
		if got := compareFacts(t, payload); got.Decoded != decoded {
			t.Errorf("payload %q decoded %v, want %v", payload, got.Decoded, decoded)
		}
	}
}

func replaceAll(format string, values ...string) string {
	out := ""
	for i := 0; i < len(format); i++ {
		if i+1 < len(format) && format[i] == '%' && format[i+1] == 's' {
			out += values[0]
			values = values[1:]
			i++
			continue
		}
		out += string(format[i])
	}
	return out
}

func TestIndexedFactsCountTests(t *testing.T) {
	payload := compareFacts(t, `{"events":[{"type":"test","content":{}},{"type":"span","content":{"type":"test"}},{"type":"test"}]}`)
	if payload.TestEvents != 2 || payload.EmptySession {
		t.Fatalf("facts %+v", payload)
	}
	if payload := compareFacts(t, `{"events":[{"type":"test_session_end","content":{"test_session_id":7,"error":0,`+
		`"meta":{"test.session.empty_reason":"zero_tests","test.status":"skip"},"metrics":{"test.exit_code":0}}}]}`); !payload.EmptySession {
		t.Fatalf("facts %+v", payload)
	}
}

func FuzzIndexedFactsMatchDecoding(f *testing.F) {
	for _, seed := range []string{`{"events":[{"type":"test","content":{}}]}`, `{"events":[{"content":{"type":"test"}}]}`,
		`{"events":[{"type":"test_session_end","content":{"test_session_id":7,"error":0,"meta":{"test.session.empty_reason":"zero_tests","test.status":"skip"},"metrics":{"test.exit_code":0}}}]}`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, payload string) { compareFacts(t, payload) })
}
