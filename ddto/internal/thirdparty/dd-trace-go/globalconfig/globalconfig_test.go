package globalconfig

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"testing"
)

func TestRuntimeIDFormat(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := make(map[string]bool)
	for range 32 {
		id := newRuntimeID()
		if !pattern.MatchString(id) {
			t.Fatalf("runtime ID is not a lowercase UUIDv4: %q", id)
		}
		if seen[id] {
			t.Fatalf("runtime IDs reused: %q", id)
		}
		seen[id] = true
	}
	if !pattern.MatchString(RuntimeID()) || RuntimeID() != RuntimeID() {
		t.Fatal("process runtime ID must be a stable UUIDv4")
	}
}

func TestRootSessionIDInheritance(t *testing.T) {
	for _, inherited := range []string{"", "parent-session-id"} {
		t.Run(inherited, func(t *testing.T) {
			t.Setenv(rootSessionIDEnvVar, inherited)
			t.Setenv("MINI_RUNTIME_ID_CHILD", "1")
			cmd := exec.Command(os.Args[0], "-test.run=^TestRuntimeIDChild$")
			data, err := cmd.Output()
			if err != nil {
				t.Fatalf("runtime ID child: %v", err)
			}
			var result struct{ Runtime, Root, Propagated string }
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatalf("decode runtime IDs: %v: %s", err, data)
			}
			want := inherited
			if want == "" {
				want = result.Runtime
			}
			if result.Runtime == "" || result.Root != want || result.Propagated != want {
				t.Fatalf("runtime/session IDs %+v; want root and propagated %q", result, want)
			}
		})
	}
}

func TestRuntimeIDChild(t *testing.T) {
	if os.Getenv("MINI_RUNTIME_ID_CHILD") != "1" {
		return
	}
	if err := json.NewEncoder(os.Stdout).Encode(struct{ Runtime, Root, Propagated string }{
		RuntimeID(), RootSessionID(), os.Getenv(rootSessionIDEnvVar),
	}); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
