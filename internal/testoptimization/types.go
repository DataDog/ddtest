package testoptimization

import "fmt"

type Test struct {
	Name            string `json:"name"`
	Suite           string `json:"suite"`
	Module          string `json:"module"`
	Parameters      string `json:"parameters"`
	SuiteSourceFile string `json:"suiteSourceFile"`
}

// FQN returns the fully qualified name of the test
func (t *Test) FQN() string {
	return fmt.Sprintf("%s.%s.%s", t.Suite, t.Name, t.Parameters)
}
