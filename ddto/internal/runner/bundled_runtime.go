package runner

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// bundledMini supplies the Mini sources that a release build carries. It
// extracts them at most once per run, and only when provisioning needs the
// CLI's own sources; the plan releases their lock after go test.
type bundledMini struct {
	env     *goEnvironment
	dir     string
	archive []byte
	// temp is the run's temporary directory, which holds the sources when the
	// user cache is unusable.
	temp string
	once sync.Once
	root string
	lock *os.File
}

type bundledMiniKey struct{}

func withBundledMini(ctx context.Context, bundled *bundledMini) context.Context {
	if bundled == nil {
		return ctx
	}
	return context.WithValue(ctx, bundledMiniKey{}, bundled)
}

func bundledMiniFrom(ctx context.Context) *bundledMini {
	bundled, _ := ctx.Value(bundledMiniKey{}).(*bundledMini)
	return bundled
}

// sources returns the extracted root, or "" when the sources are unavailable;
// provisioning then looks for the CLI's version in GOMODCACHE, as before.
func (b *bundledMini) sources(ctx context.Context) string {
	b.once.Do(func() {
		debug := debugFromContext(ctx)
		root, lock, err := ensureBundledRuntime(ctx, b.env, b.dir, b.archive, debug)
		if err == nil {
			b.root, b.lock = root, lock
			return
		}
		debug.printf("bundled runtime state=unavailable error=%q", err.Error())
		if b.temp == "" {
			return
		}
		// Without a usable user cache, such as one inside the project, the
		// run's temporary directory holds the sources until go test ends.
		root = filepath.Join(b.temp, "runtime")
		archive, err := zip.NewReader(bytes.NewReader(b.archive), int64(len(b.archive)))
		if err == nil {
			err = extractBundledFiles(archive, root)
		}
		if err != nil {
			debug.printf("bundled runtime state=unavailable error=%q", err.Error())
			return
		}
		debug.printf("bundled runtime state=temporary path=%q", root)
		b.root = root
	})
	return b.root
}

func (b *bundledMini) release() {
	if b != nil && b.lock != nil {
		_ = b.lock.Close()
	}
}

// ensureBundledRuntime extracts the runtime zip once per content into the user
// cache, like the tool wrapper, and returns its root with the lock held shared,
// so that a pruner cannot remove the sources while go builds them.
func ensureBundledRuntime(ctx context.Context, env *goEnvironment, dir string, data []byte, debug *cliDebug) (root string, lock *os.File, err error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", nil, fmt.Errorf("read the bundled runtime: %w", err)
	}
	parent, err := ddtoCacheDir(env, dir, "runtimes")
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(parent, 0700); err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(data)
	key := hex.EncodeToString(sum[:8])
	root = filepath.Join(parent, key)
	if lock, err = holdGOROOTMirrorLock(ctx, root+".lock", false); err != nil {
		return "", nil, err
	}
	if validBundledRuntime(root, archive) {
		touchGOROOTMirror(root)
		debug.printf("bundled runtime state=reused path=%q", root)
		return root, lock, nil
	}
	lock.Close()
	debug.printf("bundled runtime state=stale path=%q", root)
	if lock, err = holdGOROOTMirrorLock(ctx, root+".lock", true); err != nil {
		return "", nil, err
	}
	created := false
	if !validBundledRuntime(root, archive) {
		if err := extractBundledRuntime(archive, parent, key, root, debug); err != nil {
			lock.Close()
			return "", nil, err
		}
		created = true
	}
	touchGOROOTMirror(root)
	lock.Close()
	if lock, err = holdGOROOTMirrorLock(ctx, root+".lock", false); err != nil {
		return "", nil, err
	}
	if !validBundledRuntime(root, archive) {
		lock.Close()
		return "", nil, errors.New("bundled runtime changed while it was published")
	}
	if created {
		pruneGOROOTMirrors(parent, key)
	} else {
		debug.printf("bundled runtime state=reused path=%q", root)
	}
	return root, lock, nil
}

// validBundledRuntime reports whether root holds every file of the archive at
// its size, so that an interrupted removal is extracted again.
func validBundledRuntime(root string, archive *zip.Reader) bool {
	for _, file := range archive.File {
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(file.Name)))
		if err != nil || !info.Mode().IsRegular() || uint64(info.Size()) != file.UncompressedSize64 {
			return false
		}
	}
	return validMiniSource(root)
}

func extractBundledRuntime(archive *zip.Reader, parent, key, root string, debug *cliDebug) error {
	started := time.Now()
	if err := removeGOROOTMirror(root); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, key+".tmp-")
	if err != nil {
		return err
	}
	defer removeGOROOTMirror(staging)
	if err := extractBundledFiles(archive, staging); err != nil {
		return err
	}
	if err := os.Rename(staging, root); err != nil {
		return err
	}
	debug.printf("bundled runtime state=created path=%q duration_ms=%d", root, time.Since(started).Milliseconds())
	return nil
}

func extractBundledFiles(archive *zip.Reader, dir string) error {
	for _, file := range archive.File {
		name := filepath.FromSlash(file.Name)
		if !filepath.IsLocal(name) || file.FileInfo().IsDir() {
			return fmt.Errorf("bundled runtime has an invalid entry %q", file.Name)
		}
		if err := extractBundledFile(file, filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}

func extractBundledFile(file *zip.File, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	in, err := file.Open()
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
