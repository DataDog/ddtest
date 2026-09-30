package instrument

import (
	"strings"
	"testing"
)

func TestRejectUnsupportedTesting(t *testing.T) {
	_, err := Transform(map[string][]byte{"testing.go": []byte("package testing\n")})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("unsupported testing must fail before compilation; got %v", err)
	}
}

func TestRejectAlreadyInstrumentedTesting(t *testing.T) {
	_, err := Transform(map[string][]byte{"testing.go": []byte("package testing\nvar __dd_civisibility_instrumentTestingM int\n")})
	if err == nil || !strings.Contains(err.Error(), "already instrumented") {
		t.Fatalf("double instrumentation must be rejected; got %v", err)
	}
}

func TestRejectInvalidSource(t *testing.T) {
	_, err := Transform(map[string][]byte{"testing.go": []byte("package testing; func broken(")})
	if err == nil {
		t.Fatal("invalid source accepted")
	}
}
