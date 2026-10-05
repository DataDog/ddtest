package coverage

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWorkerNamespaces(t *testing.T) {
	s := &Session{Directory: t.TempDir(), helper: "helper", cypressHook: "hook", nyc: "nyc"}
	var wg sync.WaitGroup
	for node := 0; node < 2; node++ {
		for worker := 0; worker < 3; worker++ {
			wg.Go(func() {
				env := map[string]string{"USER_SETTING": "preserved"}
				if err := s.WorkerEnv(env, node, worker); err != nil {
					t.Error(err)
					return
				}
				if env["USER_SETTING"] != "preserved" || env[HelperEnv] != "helper" || env[NYCEnv] != "nyc" {
					t.Error(env)
				}
				if _, err := os.Stat(env[WorkerDirectoryEnv]); err != nil {
					t.Error(err)
				}
			})
		}
	}
	wg.Wait()
	entries, err := os.ReadDir(s.Directory)
	if err != nil || len(entries) != 6 {
		t.Fatalf("entries=%v error=%v", entries, err)
	}
	sentinel := filepath.Join(s.Directory, "node-0-worker-0", "existing.json")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.WorkerEnv(map[string]string{}, 0, 0); err == nil {
		t.Fatal("reused a worker namespace")
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "preserve" {
		t.Fatal("existing coverage changed")
	}
}
