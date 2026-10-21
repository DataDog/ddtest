// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/kballard/go-shellquote"
	"github.com/stretchr/testify/require"
)

func TestJestSelectionEvaluatesConditions(t *testing.T) {
	for _, condition := range []string{"false", "${{ false }}", "env.ENABLED == 'yes'"} {
		t.Run(condition, func(t *testing.T) {
			root := newJestRepository(t, fmt.Sprintf(`jobs:
  disabled:
    if: ${{ false }}
    steps:
      - run: jest --config disabled-job.js
  active:
    env: {ENABLED: 'no'}
    steps:
      - if: "%s"
        run: jest --config disabled-step.js
      - run: jest --config active.js
`, condition))
			selected, err := JestValidationCommand(root)
			require.NoError(t, err)
			require.Equal(t, shellquote.Join(filepath.Join(root, "node_modules/.bin/jest"), "--config", "active.js"), selected)
		})
	}
}
