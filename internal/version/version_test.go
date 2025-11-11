package version

import "testing"

func TestParseValid(t *testing.T) {
	input := "1.23.4"
	v, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse(%q) returned error: %v", input, err)
	}

	if v.String() != input {
		t.Fatalf("expected String() to return %q, got %q", input, v.String())
	}

	expectedComponents := []int{1, 23, 4}
	actual := v.Components()
	if len(actual) != len(expectedComponents) {
		t.Fatalf("expected %d components, got %d", len(expectedComponents), len(actual))
	}

	for i, expected := range expectedComponents {
		if actual[i] != expected {
			t.Fatalf("expected component %d to be %d, got %d", i, expected, actual[i])
		}
	}
}

func TestParseInvalid(t *testing.T) {
	cases := []string{
		"",
		"  ",
		"1..2",
		"1.2.a",
		"1.-2",
	}

	for _, input := range cases {
		if _, err := Parse(input); err == nil {
			t.Fatalf("expected Parse(%q) to fail", input)
		}
	}
}

func TestIsValid(t *testing.T) {
	if !IsValid("1.0.0") {
		t.Fatal("expected 1.0.0 to be valid")
	}

	if IsValid("1.0.beta") {
		t.Fatal("expected 1.0.beta to be invalid")
	}
}

func TestCompare(t *testing.T) {
	v1 := MustParse("1.2.3")
	v2 := MustParse("1.2.3")
	v3 := MustParse("1.3.0")
	v4 := MustParse("1.2")

	if v1.Compare(v2) != 0 {
		t.Fatal("expected equal versions to compare as 0")
	}

	if v1.Compare(v3) >= 0 {
		t.Fatal("expected v1 < v3")
	}

	if v3.Compare(v1) <= 0 {
		t.Fatal("expected v3 > v1")
	}

	if v1.Compare(v4) <= 0 {
		t.Fatal("expected v1 > v4 when compared with shorter version")
	}
}

func TestCompareStrings(t *testing.T) {
	cmp, err := CompareStrings("1.2.3", "1.2.4")
	if err != nil {
		t.Fatalf("CompareStrings returned error: %v", err)
	}
	if cmp >= 0 {
		t.Fatal("expected 1.2.3 < 1.2.4")
	}

	if _, err := CompareStrings("1.2", "1.a"); err == nil {
		t.Fatal("expected invalid version comparison to fail")
	}
}
