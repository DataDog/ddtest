package onboard

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShellDiscoveryUsesLiteralASTValues(t *testing.T) {
	commands, err := staticCommands(`DEBUG='cucumber-js:*' node "a path.js" 'x;y' a\ b && echo '$(literal)'`)
	require.NoError(t, err)
	require.Equal(t, [][]string{{"DEBUG=cucumber-js:*", "node", "a path.js", "x;y", "a b"}, {"echo", "$(literal)"}}, commands)
	for _, command := range []string{`echo "$(node run.js)"`, `echo "$HOME"`, `node a.js > out`, `node a.js &`} {
		_, err := staticCommands(command)
		require.Error(t, err, command)
	}
}
