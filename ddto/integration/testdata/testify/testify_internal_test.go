package fixture

import (
	"github.com/stretchr/testify/suite"
	"testing"
)

type InternalSuite struct{ suite.Suite }

func (s *InternalSuite) TestPass()    { s.True(true) }
func TestParityInternal(t *testing.T) { suite.Run(t, new(InternalSuite)) }
