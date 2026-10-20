package onboard

import (
	"fmt"
	"io"
)

func WriteValidationScope(output io.Writer, scope ValidationScope) {
	_, _ = fmt.Fprintln(output, "\nRequired local validation configurations (every CI consumer must also be instrumented):")
	for _, entry := range scope.Commands {
		_, _ = fmt.Fprintf(output, "  - [%s] %s\n", entry.Directory, entry.Command)
		if entry.Resolution != "" {
			_, _ = fmt.Fprintf(output, "      Resolved test command: %s\n", entry.Resolution)
		}
		for _, build := range entry.Prerequisites {
			_, _ = fmt.Fprintf(output, "      Preparation: [%s] %s\n", build.Directory, build.Command)
		}
		for _, setup := range entry.UnvalidatedSetup {
			_, _ = fmt.Fprintf(output, "      Unvalidated setup: %s\n", setup)
		}
		for _, location := range entry.Locations {
			_, _ = fmt.Fprintf(output, "      %s\n", location)
		}
	}
	_, _ = fmt.Fprintln(output, "--all replays listed preparation in separate temporary copies before each comparison, without instrumentation. --check-only executes no preparation. Unresolved setup stays unvalidated. Preserve CI commands.")
	for _, reason := range scope.Unresolved {
		_, _ = fmt.Fprintf(output, "  - Unresolved scope: %s\n", reason)
	}
	for _, reason := range scope.Review {
		_, _ = fmt.Fprintf(output, "  - Review separately: %s\n", reason)
	}
}
