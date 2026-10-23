// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package environment

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestNodeOptionsUseNodeEscaping(t *testing.T) {
	for _, tc := range []struct {
		input string
		words []string
	}{
		{`--require C:\repo\setup.cjs`, []string{"--require", `C:\repo\setup.cjs`}},
		{`--require "C:\\repo space\\setup.cjs"`, []string{"--require", `C:\repo space\setup.cjs`}},
		{`--title="unit tests" --experimental-vm-modules`, []string{"--title=unit tests", "--experimental-vm-modules"}},
		{`--title='literal'`, []string{"--title='literal'"}},
		{"--title=unit\ttests", []string{"--title=unit\ttests"}},
		{`--title="a\"b"`, []string{`--title=a"b`}},
	} {
		words, err := SplitNodeOptions(tc.input)
		require.NoError(t, err)
		require.Equal(t, tc.words, words)
		roundtrip, err := SplitNodeOptions(JoinNodeOptions(words))
		require.NoError(t, err)
		require.Equal(t, words, roundtrip)
	}
	for _, value := range []string{`--title="unterminated`, `--title="escape\`} {
		_, err := SplitNodeOptions(value)
		require.Error(t, err)
	}
}
