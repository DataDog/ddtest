package bazelupload

import (
	"os"
	"path/filepath"
	"sync"
)

// directories opens the directories of the plan's sources inside the upload
// root, each once and relative to its parent, and closes each one after the
// last source or subdirectory that uses it. os.Root resolves every component
// of a path for each operation; a source inside its open directory resolves
// only its name.
type directories struct {
	root *os.Root
	mu   sync.Mutex
	open map[string]*directory
}

type directory struct {
	name   string
	ready  chan struct{}
	root   *os.Root
	err    error
	parent *directory
	// users counts the sources and subdirectories that hold the directory.
	// directories.mu guards it.
	users int
}

func newDirectories(root *os.Root) *directories {
	return &directories{root: root, open: make(map[string]*directory)}
}

// acquire returns the open directory name, a cleaned path relative to the
// root, and holds it until release.
func (dirs *directories) acquire(name string) *directory {
	if name == "." {
		return &directory{name: ".", root: dirs.root}
	}
	dirs.mu.Lock()
	if existing := dirs.open[name]; existing != nil {
		existing.users++
		dirs.mu.Unlock()
		<-existing.ready
		return existing
	}
	opening := &directory{name: name, ready: make(chan struct{}), users: 1}
	dirs.open[name] = opening
	dirs.mu.Unlock()

	opening.parent = dirs.acquire(filepath.Dir(name))
	if opening.parent.err == nil {
		opening.root, opening.err = opening.parent.root.OpenRoot(filepath.Base(name))
	}
	if opening.root == nil {
		// A symbolic link can lead elsewhere in the root, outside its parent.
		opening.root, opening.err = dirs.root.OpenRoot(name)
	}
	close(opening.ready)
	return opening
}

// release drops a hold that acquire returned, and closes each directory that
// nothing holds any more.
func (dirs *directories) release(held *directory) {
	for held != nil && held.name != "." {
		dirs.mu.Lock()
		held.users--
		if held.users > 0 {
			dirs.mu.Unlock()
			return
		}
		delete(dirs.open, held.name)
		dirs.mu.Unlock()
		if held.root != nil {
			_ = held.root.Close()
		}
		held = held.parent
	}
}
