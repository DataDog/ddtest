// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Walk visits source except ddtest's own .testoptimization output. When
// skipIgnored is non-nil, Git-ignored paths for which it returns true are
// skipped as well; a nil filter keeps every ignored path.
func Walk(ctx context.Context, source string, skipIgnored func(relative string) bool, visit func(path, relative string, entry fs.DirEntry) error) error {
	ignored := map[string]bool{}
	if skipIgnored != nil {
		ignored = ignoredPaths(ctx, source, skipIgnored)
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == ".testoptimization" || ignored[relative] {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		return visit(path, relative, entry)
	})
}

func ignoredPaths(ctx context.Context, source string, skip func(string) bool) map[string]bool {
	ignored := map[string]bool{}
	// Git may be absent in a disposable fixture; then preserve all source inputs.
	command := exec.CommandContext(ctx, "git", "-C", source, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z")
	data, err := command.Output()
	if err != nil {
		return ignored
	}
	for _, path := range strings.Split(string(data), "\x00") {
		path = filepath.FromSlash(strings.TrimSuffix(path, "/"))
		if path == "" || !filepath.IsLocal(path) || !skip(path) {
			continue
		}
		ignored[path] = true
	}
	return ignored
}

// FileDescriptorsExhausted reports a failure caused by the process or system
// open-file limit, typically while copying a large dependency tree.
func FileDescriptorsExhausted(err error) bool {
	return errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE)
}

// Copy replicates source into target outside the repository, following Walk's
// skipIgnored rules. Links inside source are rewritten to stay inside target.
func Copy(ctx context.Context, source, target string, skipIgnored func(relative string) bool) error {
	canonical, err := filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	source = canonical
	destination := CanonicalRoot(target)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		destination = filepath.Join(CanonicalRoot(filepath.Dir(target)), filepath.Base(target))
	}
	if relative, err := filepath.Rel(source, destination); err == nil && (relative == "." || filepath.IsLocal(relative)) {
		return fmt.Errorf("configuration copy destination is inside the repository; set TMPDIR outside the repository before retrying")
	}
	return Walk(ctx, source, skipIgnored, func(path, relative string, entry fs.DirEntry) error {
		destination := filepath.Join(target, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(destination, info.Mode().Perm()|0700)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			absolute := link
			if !filepath.IsAbs(absolute) {
				absolute = filepath.Join(filepath.Dir(path), link)
			}
			local, relErr := filepath.Rel(source, absolute)
			if relErr == nil && filepath.IsLocal(local) {
				if filepath.IsAbs(link) {
					link = filepath.Join(target, local)
				}
			} else {
				link = absolute
			}
			return os.Symlink(link, destination)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("cannot isolate special file %s", relative)
		}
		return copyWithDescriptorRetry(func() error { return copyFile(path, destination, info) })
	})
}

// descriptorRetryDelays spaces the retries of one file copy after the host
// runs out of open files. Every trial hit this once at a few hundred open
// descriptors: the pressure was host-wide and transient.
var descriptorRetryDelays = []time.Duration{250 * time.Millisecond, time.Second}

func copyWithDescriptorRetry(copyFile func() error) error {
	err := copyFile()
	for _, delay := range descriptorRetryDelays {
		if !FileDescriptorsExhausted(err) {
			return err
		}
		time.Sleep(delay)
		err = copyFile()
	}
	return err
}

func copyFile(source, target string, info fs.FileInfo) error {
	cloned, err := cloneFile(source, target)
	if err != nil {
		return err
	}
	if !cloned {
		in, err := os.Open(source)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()|0600)
		if err != nil {
			return errors.Join(err, in.Close())
		}
		_, copyErr := io.Copy(out, in)
		if err := errors.Join(copyErr, in.Close(), out.Close()); err != nil {
			return err
		}
	}
	if err := os.Chmod(target, info.Mode().Perm()|0600); err != nil {
		return err
	}
	return os.Chtimes(target, info.ModTime(), info.ModTime())
}
