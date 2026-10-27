package testkit
import("testing";"github.com/stretchr/testify/suite")
type ExampleSuite struct{suite.Suite}
func(s *ExampleSuite)TestPass(){s.True(true)}
func Run(t *testing.T){suite.Run(t,new(ExampleSuite))}
