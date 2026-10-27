package fixture_test

import (
	. "github.com/stretchr/testify/suite"
	"testing"
)

func TestParityDot(t *testing.T) { runner := Run; runner(t, new(ParitySuite)) }
