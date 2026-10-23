package onboard

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestParseValidationCommand(t *testing.T) {
	for _, input := range []string{`OUTPUT='two words' COUNT=2 npx runner --reporter=junit`, `cross-env OUTPUT='two words' COUNT=2 npx runner --reporter=junit`} {
		command, args, env, err := ParseValidationCommand(input)
		require.NoError(t, err)
		require.Equal(t, "npx", command)
		require.Equal(t, []string{"runner", "--reporter=junit"}, args)
		require.Equal(t, map[string]string{"OUTPUT": "two words", "COUNT": "2"}, env)
	}
	for _, input := range []string{`OUTPUT=$HOME runner`, `OUTPUT=$(id) runner`, `OUTPUT=x`, `runner && other`, `NODE_OPTIONS=-r runner`, `PATH=/unknown runner`} {
		_, _, _, err := ParseValidationCommand(input)
		require.Error(t, err, input)
	}
}
