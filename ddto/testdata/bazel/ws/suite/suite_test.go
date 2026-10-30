package suite

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type ExampleSuite struct {
	suite.Suite
	value int
}

func (s *ExampleSuite) SetupTest() { s.value = 5 }

func (s *ExampleSuite) TestPass() { s.Equal(5, s.value) }

func (s *ExampleSuite) TestFail() { s.Equal(6, s.value, "intentional failure") }

func (s *ExampleSuite) TestSkip() { s.T().Skip("intentional skip") }

func TestExampleSuite(t *testing.T) { suite.Run(t, new(ExampleSuite)) }

func TestRegular(t *testing.T) {}
