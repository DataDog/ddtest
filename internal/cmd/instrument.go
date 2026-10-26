package cmd

import (
	"errors"

	"github.com/spf13/cobra"
)

// instrumentCmd documents ddtest instrument in ddtest's help. main hands the
// command to ddto before cobra parses anything, so this never runs there.
var instrumentCmd = &cobra.Command{
	Use:   "instrument command [arguments]",
	Short: "Run a Go test command with Datadog Test Optimization",
	Long: `Run a Go test command with Datadog Test Optimization.

Put ddtest instrument in front of the command that runs your Go tests. It
instruments the tests while Go builds them, with temporary overlays, and
keeps go test's arguments, output and exit status:

  ddtest instrument go test ./...
  ddtest instrument go tool gotestsum -- ./...
  ddtest instrument --go-shim make test

instrument must be ddtest's first argument. ddtest instrument --help shows
every option.`,
	DisableFlagParsing: true,
	RunE: func(*cobra.Command, []string) error {
		return errors.New("instrument must be ddtest's first argument")
	},
}
