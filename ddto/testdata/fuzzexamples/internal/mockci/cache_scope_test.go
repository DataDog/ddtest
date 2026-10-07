package mockci

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
)

// A new intake can reuse the previous one's URL while serving a new policy.
// Its children must inherit the intake's cache directory, not the caller's.
func TestIntakesHaveDistinctInheritedCacheRoots(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DD_FUZZ_EXAMPLE_CACHE_ROOT", root)
	t.Cleanup(net.ResetReadCacheHooksForTesting)
	seen := map[string]bool{}
	for range 3 {
		server := Start(net.SettingsResponseData{}, nil, nil)
		inherited := os.Getenv("DD_FUZZ_EXAMPLE_CACHE_ROOT")
		server.Close()
		if inherited == root || inherited == "" || seen[inherited] {
			t.Fatalf("intake reused cache root %q", inherited)
		}
		relative, err := filepath.Rel(root, inherited)
		if err != nil || !filepath.IsLocal(relative) {
			t.Fatalf("intake cache escaped fixture root: %q", inherited)
		}
		if _, err := os.Stat(inherited); err != nil {
			t.Fatal(err)
		}
		seen[inherited] = true
		if os.Getenv("DD_FUZZ_EXAMPLE_CACHE_ROOT") != root {
			t.Fatal("Close did not restore caller cache root")
		}
	}
}
