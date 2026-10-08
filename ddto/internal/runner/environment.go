package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"go/version"
	"strings"
)

const minimumGoVersion = "go1.25.0"

type goEnvironment struct {
	GOMOD, GOWORK, GOMODCACHE, GOVERSION string
}

func readGoEnvironment(ctx context.Context, dir string) (*goEnvironment, error) {
	out, err := goTool(ctx, dir, nil, "env", "-json", "GOMOD", "GOWORK", "GOMODCACHE", "GOVERSION")
	if err != nil {
		return nil, err
	}
	var environment goEnvironment
	if err := json.Unmarshal([]byte(out), &environment); err != nil {
		return nil, fmt.Errorf("read Go environment: %w", err)
	}
	return &environment, nil
}

func supportsGoToolchain(value string) bool {
	// Development distributions report "devel go1.N-<revision> ...".
	fields := strings.Fields(strings.TrimPrefix(value, "devel "))
	return len(fields) != 0 && version.IsValid(fields[0]) && version.Compare(fields[0], minimumGoVersion) >= 0
}
