package environment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DataDog/ddtest/internal/constants"
)

// CircleCIParametersPath can be passed directly to continuation/continue.
var CircleCIParametersPath = filepath.Join(constants.PlanDirectory, "circleci", "parameters.json")

type CircleCI struct{}

func (c *CircleCI) Name() string {
	return "circleci"
}

func (c *CircleCI) Configure(plan CIPlan) error {
	if plan.ParallelRunners <= 0 {
		return fmt.Errorf("parallelRunners must be greater than 0, got %d", plan.ParallelRunners)
	}
	parameters := struct {
		Parallelism int  `json:"parallelism"`
		HasTests    bool `json:"has_tests"`
	}{Parallelism: plan.ParallelRunners, HasTests: plan.HasTests}
	data, err := json.Marshal(parameters)
	if err != nil {
		return fmt.Errorf("failed to marshal CircleCI continuation parameters: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(CircleCIParametersPath), 0755); err != nil {
		return fmt.Errorf("failed to create CircleCI output directory: %w", err)
	}
	if err := os.WriteFile(CircleCIParametersPath, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("failed to write CircleCI continuation parameters to %s: %w", CircleCIParametersPath, err)
	}
	return nil
}
