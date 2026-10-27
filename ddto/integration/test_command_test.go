package integration

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// sdkTestCommand is the pinned SDK's test.command: each greedy filter removes
// from its flag to the last whitespace of the joined command.
func sdkTestCommand(args []string) string {
	cmd := args[0]
	if len(args) > 1 {
		cmd = cmd + " " + strings.Join(args[1:], " ") + " "
	}
	for _, pattern := range []string{`(?si)-test.gocoverdir=(.*)\s`, `(?si)-test.v=(.*)\s`, `(?si)-test.testlogfile=(.*)\s`} {
		cmd = regexp.MustCompile(pattern).ReplaceAllString(cmd, "")
	}
	return strings.TrimSpace(cmd)
}

// isVolatileTestArgument reports a -test.v argument with a value, or one of
// the paths go test injects, which Mini removes.
func isVolatileTestArgument(arg string) bool {
	name := strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-")
	for _, prefix := range []string{"test.gocoverdir=", "test.v=", "test.testlogfile=", "test.coverprofile=", "test.outputdir=", "test.fuzzcachedir="} {
		if strings.HasPrefix(arg, "-") && len(name) >= len(prefix) && strings.EqualFold(name[:len(prefix)], prefix) {
			return true
		}
	}
	return false
}

// miniTestCommand is Mini's test.command: only the volatile flags are removed.
func miniTestCommand(args []string) string {
	kept := []string{args[0]}
	for _, arg := range args[1:] {
		if !isVolatileTestArgument(arg) {
			kept = append(kept, arg)
		}
	}
	return strings.TrimSpace(strings.Join(kept, " "))
}

// alignMiniTestCommands accounts for one declared difference before the CI
// comparison. The pinned SDK's greedy filters cut test.command at its first
// volatile flag, while Mini removes only those flags. A Mini command absent
// from the SDK capture is replaced, in test.command and the session resource,
// by the longest SDK command that, without Mini's volatile flags, is equal to
// it or its whole-argument prefix; any other command stays exact and fails the
// comparison. Child processes started by
// the fixture are matched the same way. For the fixture's own invocation,
// whose arguments are known, Mini's exact form must be present, so a Mini
// that kept the SDK's form cannot pass unnoticed. A nil invocation, for a
// binary whose arguments go test chooses, skips that check.
func alignMiniTestCommands(sdkEvents, miniEvents []map[string]any, invocation []string) []map[string]any {
	sdkCommands := map[string]bool{}
	for _, event := range sdkEvents {
		if content, ok := event["content"].(map[string]any); ok {
			if meta, ok := content["meta"].(map[string]any); ok {
				if command, ok := meta["test.command"].(string); ok {
					sdkCommands[command] = true
				}
			}
		}
	}
	aligned := map[string]string{}
	invocationSeen := false
	for _, event := range miniEvents {
		content, _ := event["content"].(map[string]any)
		meta, _ := content["meta"].(map[string]any)
		command, ok := meta["test.command"].(string)
		if !ok {
			continue
		}
		if invocation != nil && command == miniTestCommand(invocation) {
			invocationSeen = true
		}
		if sdkCommands[command] {
			continue
		}
		replacement, found := aligned[command]
		if !found {
			for _, arg := range strings.Fields(command) {
				if isVolatileTestArgument(arg) {
					panic(fmt.Sprintf("Mini test.command %q retains a volatile flag", command))
				}
			}
			for candidate := range sdkCommands {
				// The SDK keeps the volatile flags that precede none of its
				// filters, such as a final -test.fuzzcachedir; Mini removes them.
				kept := miniTestCommand(strings.Fields(candidate))
				if (kept == command || strings.HasPrefix(command, kept+" ")) && len(candidate) > len(replacement) {
					replacement = candidate
				}
			}
			aligned[command] = replacement
		}
		if replacement == "" {
			continue
		}
		meta["test.command"] = replacement
		if resource, ok := content["resource"].(string); ok && event["type"] == "test_session_end" && strings.HasSuffix(resource, "test_session."+command) {
			content["resource"] = strings.TrimSuffix(resource, command) + replacement
		}
	}
	if invocation != nil {
		if mini := miniTestCommand(invocation); mini != sdkTestCommand(invocation) && !invocationSeen {
			panic(fmt.Sprintf("no Mini event has test.command %q", mini))
		}
	}
	return miniEvents
}

func TestAlignMiniTestCommandsRequiresMiniForm(t *testing.T) {
	invocation := []string{"fixture.test", "-test.count=1", "-test.v=true", "-test.run=^FuzzNative$"}
	session := func(command string) map[string]any {
		return map[string]any{"type": "test_session_end", "content": map[string]any{"resource": "golang.org/pkg/testing.test_session." + command, "meta": map[string]any{"test.command": command}}}
	}
	if got, want := sdkTestCommand(invocation), "fixture.test -test.count=1"; got != want {
		t.Fatalf("SDK form %q, want %q", got, want)
	}
	if got, want := miniTestCommand(invocation), "fixture.test -test.count=1 -test.run=^FuzzNative$"; got != want {
		t.Fatalf("Mini form %q, want %q", got, want)
	}
	sdk := []map[string]any{session("fixture.test -test.count=1"), session("fixture.test -test.run=^FuzzChild$"), session("fixture.test -test.run=^FuzzOther$ -test.timeout=15s")}
	sdk = append(sdk, session("fixture.test -test.run=^FuzzCache$ -test.fuzzcachedir=cache"))
	mini := []map[string]any{session("fixture.test -test.count=1 -test.run=^FuzzNative$"), session("fixture.test -test.run=^FuzzChild$ -test.parallel=1"), session("fixture.test -test.run=^FuzzOther$ -test.timeout=15s"), session("fixture.test -unrelated"), session("fixture.test -test.run=^FuzzCache$")}
	alignMiniTestCommands(sdk, mini, invocation)
	for i, want := range []string{"fixture.test -test.count=1", "fixture.test -test.run=^FuzzChild$", "fixture.test -test.run=^FuzzOther$ -test.timeout=15s", "fixture.test -unrelated", "fixture.test -test.run=^FuzzCache$ -test.fuzzcachedir=cache"} {
		content := mini[i]["content"].(map[string]any)
		if got := content["meta"].(map[string]any)["test.command"]; got != want {
			t.Fatalf("event %d test.command = %q, want %q", i, got, want)
		}
		if got := content["resource"]; got != "golang.org/pkg/testing.test_session."+want {
			t.Fatalf("event %d resource = %q", i, got)
		}
	}
	for _, events := range [][]map[string]any{
		{session("fixture.test -test.count=1")},                                     // Mini kept the SDK's form.
		{session("fixture.test -test.count=1 -test.v=true -test.run=^FuzzNative$")}, // Mini kept a volatile flag.
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("accepted Mini events %v", events)
				}
			}()
			alignMiniTestCommands([]map[string]any{session("fixture.test -test.count=1")}, events, invocation)
		}()
	}
}
