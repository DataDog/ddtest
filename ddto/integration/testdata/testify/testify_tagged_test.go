//go:build testify_extra

package fixture_test

import (
	"github.com/stretchr/testify/suite"
	"testing"
)

func TestParityTagged(t *testing.T) { suite.Run(t, new(ParitySuite)) }
