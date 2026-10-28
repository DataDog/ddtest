package fixture
import("testing";"github.com/stretchr/testify/suite")
type ExampleSuite struct{suite.Suite}
func(s *ExampleSuite)TestPass(){s.Equal(5,Add(2,3))}
func TestSuite(t *testing.T){suite.Run(t,new(ExampleSuite))}
