//go:build !windows

package runner

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/goenv"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

// ExecWithCallerEnvironment restores the caller's Go settings, then replaces
// this process with the program at path, given its arguments in args after
// it: a go test -exec program sees what native go test gives it, rather than
// ddto's temporary workspace. args[0] is the program as go test names it; an
// empty path looks it up.
func ExecWithCallerEnvironment(path string, args []string) int {
	goenv.Restore()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, version.BuildLogPrefix+" ERROR: missing -exec program")
		return 2
	}
	var err error
	if path == "" {
		path, err = exec.LookPath(args[0])
	}
	if err == nil {
		err = syscall.Exec(path, args, os.Environ())
		// As os.StartProcess reports it to go test.
		err = &os.PathError{Op: "fork/exec", Path: path, Err: err}
	}
	fmt.Fprintln(os.Stderr, err)
	return 2
}

// ExecCommand replaces this process with args, unchanged: ddto passes every
// command but go test through. It finds and starts the command as execvp,
// which env and shells use, does.
func ExecCommand(args []string) int {
	debug := passThroughDebug()
	debug.printf("pass-through command=%q args=%d", args[0], len(args)-1)
	err := execvp(args, debug)
	fmt.Fprintln(os.Stderr, version.BuildLogPrefix+" ERROR:", &fs.PathError{Op: "exec", Path: args[0], Err: err})
	return commandStatus(err)
}

// passThroughDebug returns the CLI's debug log for a passed-through command,
// or nil. It writes to a duplicate of stderr, closed on exec: Go ends a
// process that writes to a broken pipe through fd 2, which would replace the
// command's result, while another descriptor only fails the write.
func passThroughDebug() *cliDebug {
	if !cliDebugEnabled() {
		return nil
	}
	fd, err := syscall.Dup(2)
	if err != nil {
		return nil
	}
	syscall.CloseOnExec(fd)
	return debugFromContext(withCLIDebug(context.Background(), os.NewFile(uintptr(fd), "stderr")))
}

// execvp starts args as the platform's execvp does, and returns only if they
// do not start. A name without / is tried in each PATH directory, or
// defaultPath's without PATH, where an empty entry is the current directory.
// The libc's own rules (searchNext) say which errors end the search and which
// report that a candidate could not run (EACCES) if nothing else starts.
func execvp(args []string, debug *cliDebug) error {
	file := args[0]
	if file == "" {
		return syscall.ENOENT
	}
	if strings.Contains(file, "/") {
		return execve(file, args, debug)
	}
	if err := searchName(file); err != nil {
		return err
	}
	path, ok := os.LookupEnv("PATH")
	if !ok {
		path = defaultPath
	}
	debug.printf("pass-through search path_entries=%d default_path=%t", strings.Count(path, ":")+1, !ok)
	var err error = syscall.ENOENT
	denied := false
	for i, dir := range strings.Split(path, ":") {
		candidate, ok := entryPath(dir, file)
		if !ok {
			debug.printf("pass-through skip path_entry=%d bytes=%d reason=%q", i, len(dir), "too long for the libc")
			continue
		}
		err = execve(candidate, args, debug)
		next, deniedHere := searchNext(candidate, err)
		if !next {
			return err
		}
		denied = denied || deniedHere
	}
	if denied {
		return syscall.EACCES
	}
	return notFound(err)
}

// execve starts path with args, and an executable file without a #! line
// with /bin/sh, as execvp does.
func execve(path string, args []string, debug *cliDebug) error {
	debug.printf("pass-through exec path=%q", path)
	err := syscall.Exec(path, args, os.Environ())
	if err == syscall.ENOEXEC {
		debug.printf("pass-through exec path=%q shell=/bin/sh reason=%q", path, "exec format error")
		err = syscall.Exec("/bin/sh", append([]string{"/bin/sh", path}, args[1:]...), os.Environ())
	}
	debug.printf("pass-through exec path=%q failed error=%q", path, err.Error())
	return err
}

// ExecNativeTool replaces the wrapper: no second process, wait loop or plan I/O.
// It is only for the CLI entrypoint, never an in-process library caller.
func ExecNativeTool(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, version.BuildLogPrefix+" ERROR: missing tool executable")
		return 2
	}
	args, err := chainUserToolexec(args)
	if err == nil {
		var executable string
		if executable, err = exec.LookPath(args[0]); err == nil {
			err = syscall.Exec(executable, args, os.Environ())
		}
	}
	fmt.Fprintln(os.Stderr, err)
	return 2
}
