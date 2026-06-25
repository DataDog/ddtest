package testoptimization

import "fmt"

type Test struct {
	Name            string `json:"name"`
	Suite           string `json:"suite"`
	Module          string `json:"module"`
	Parameters      string `json:"parameters"`
	SuiteSourceFile string `json:"suiteSourceFile"`
}

// FQN returns the parameter-free test identity used for Test Management matching.
func (t *Test) FQN() string {
	return fmt.Sprintf("%s.%s.%s", t.Module, t.Suite, t.Name)
}

// DatadogTestId returns the parameterized test identity used for TIA matching.
func (t *Test) DatadogTestId() string {
	return fmt.Sprintf("%s.%s.%s.%s", t.Module, t.Suite, t.Name, t.Parameters)
}

// DatadogSuiteId returns the parameter-free suite identity used for suite-level TIA matching.
func (t *Test) DatadogSuiteId() string {
	return fmt.Sprintf("%s.%s", t.Module, t.Suite)
}
