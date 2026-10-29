package testdrive

import "testing"

// SelectedIntegrationScenarios is integrationScenarios.selected for the
// external test package: the named scenarios this process's shard runs.
func SelectedIntegrationScenarios(t *testing.T, names []string, weights map[string]int) []string {
	t.Helper()
	return integrationScenarios{names: names, weights: weights}.selected(t)
}
