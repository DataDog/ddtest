package leak

import (
	"testing"

	"go.uber.org/goleak"
)

func TestVerifyNoneClean(t *testing.T) {
	defer goleak.VerifyNone(t)
}
