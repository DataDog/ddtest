package runner

import (
	"slices"
	"testing"
)

// Under another program's command, such as ddtest instrument, ddto runs its
// own modes again as that command; ddto's own executable runs them directly.
func TestSelfCommandRunsTheProgramsCommand(t *testing.T) {
	defer func(previous []string) { SelfCommand = previous }(SelfCommand)
	SelfCommand = nil
	if got := selfCommand("/bin/ddto", "test-exec", "program"); !slices.Equal(got, []string{"/bin/ddto", "test-exec", "program"}) {
		t.Fatalf("ddto: %q", got)
	}
	if tool, err := toolCommand("/bin/ddto", "", "/tmp/plan.json", "testify"); err != nil || tool != "'/bin/ddto' 'tool-overlay' 'testify' '/tmp/plan.json'" {
		t.Fatalf("ddto's tool-overlay: %q, %v", tool, err)
	}
	SelfCommand = []string{"instrument"}
	if got := selfCommand("/bin/ddtest", gitWorkerCommand, "state"); !slices.Equal(got, []string{"/bin/ddtest", "instrument", "ci-git-worker", "state"}) {
		t.Fatalf("ddtest: %q", got)
	}
	if tool, err := toolCommand("/bin/ddtest", "", "/tmp/plan.json", "testify"); err != nil || tool != "'/bin/ddtest' 'instrument' 'tool-overlay' 'testify' '/tmp/plan.json'" {
		t.Fatalf("ddtest's tool-overlay: %q, %v", tool, err)
	}
}
