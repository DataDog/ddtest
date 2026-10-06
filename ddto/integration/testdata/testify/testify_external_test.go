package fixture_test

import (
	testkit "example.com/external-testkit"
	"testing"
)

// The actual suite.Run reference is in another module, outside client overlays.
func TestParityExternalHelper(t *testing.T) { testkit.Run(t, new(ParitySuite)) }
