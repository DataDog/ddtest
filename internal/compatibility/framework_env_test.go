package compatibility

import (
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
)

func frameworkRunEnv(t *testing.T, f framework.Framework) map[string]string {
	t.Helper()
	env, err := f.Platform().RunEnv(framework.RuntimeOptions{ESM: f.Name() == "vitest"})
	if err != nil {
		t.Fatal(err)
	}
	return env
}
