package helpers

import (
	"github.com/stretchr/testify/suite"
	"testing"
)

// Run deliberately lives in a regular helper package, reached from root tests.
func Run(t *testing.T, s suite.TestingSuite) { runner := suite.Run; runner(t, s) }
