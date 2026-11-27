package pyformat

import "testing"

// The expected values are what Python 3.12 prints.

func TestStringIsPythonRepr(t *testing.T) {
	for text, want := range map[string]string{
		"abc":            "'abc'",
		"it's":           `"it's"`,
		`say "hi"`:       `'say "hi"'`,
		`both ' and "`:   `'both \' and "'`,
		"tab\tnew\nline": `'tab\tnew\nline'`,
		`back\slash`:     `'back\\slash'`,
		"\x01\x7f":       `'\x01\x7f'`,
		"ñ€😀":            "'ñ€😀'",
		"":               "''",
		"\u2028":         `'\u2028'`,
	} {
		if got := String(text); got != want {
			t.Errorf("String(%q) = %s, want %s", text, got, want)
		}
	}
}

func TestReprOfValues(t *testing.T) {
	for _, test := range []struct {
		value any
		want  string
	}{
		{1, "1"}, {2.5, "2.5"}, {1e16, "1e+16"}, {true, "True"}, {false, "False"}, {nil, "None"},
		{[]any{"a", 1}, "['a', 1]"},
		{&Ordered{Keys: []string{"b", "a"}, Values: map[string]any{"b": 1, "a": "x"}}, "{'b': 1, 'a': 'x'}"},
		{Tuple{"t"}, "('t',)"},
		{[]string{"x", "y"}, "['x', 'y']"},
	} {
		if got := Repr(test.value); got != test.want {
			t.Errorf("Repr(%#v) = %s, want %s", test.value, got, test.want)
		}
	}
}

func TestDumpsIndentedIsPythonJSONDumps(t *testing.T) {
	doc := map[string]any{"b": []any{1, map[string]any{"z": nil, "a": true}}, "a": "ñ\"\n", "c": []any{},
		"d": map[string]any{}, "e": 1.5}
	want := "{\n  \"a\": \"\\u00f1\\\"\\n\",\n  \"b\": [\n    1,\n    {\n      \"a\": true,\n      \"z\": null\n    }\n  ],\n" +
		"  \"c\": [],\n  \"d\": {},\n  \"e\": 1.5\n}"
	if got := DumpsIndented(doc); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if got := DumpsIndented("😀\x7f"); got != `"\ud83d\ude00\u007f"` {
		t.Errorf("astral and DEL: %s", got)
	}
}
