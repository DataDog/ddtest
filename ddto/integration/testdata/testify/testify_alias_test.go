package fixture_test

import (
	ts "github.com/stretchr/testify/suite"
	"testing"
)

var aliasedRunner = ts.Run

func TestParityAlias(t *testing.T) { aliasedRunner(t, new(ParitySuite)) }
