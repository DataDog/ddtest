package other

import "testing"

func TestOther(t *testing.T) {
	if Double(3) != 6 {
		t.Fatal("double")
	}
}
