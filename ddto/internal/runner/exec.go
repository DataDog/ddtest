package runner

import (
	"errors"
	"io/fs"
	"os/exec"
	"strings"
)

// commandStatus is the exit status of a command that ddto could not run, as
// env reports it: 127 if it is not found, 126 if it cannot run.
func commandStatus(err error) int {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return 127
	}
	return 126
}

// commandLineTail returns a Windows command line after its first argument,
// as written. It ends that argument where Go ends os.Args[0]
// (readNextArg in os/exec_windows.go), so the tail splits into os.Args[1:].
func commandLineTail(cmd string) string {
	cmd = strings.TrimLeft(cmd, " \t")
	inquote, nslash := false, 0
	for ; len(cmd) > 0; cmd = cmd[1:] {
		switch cmd[0] {
		case ' ', '\t':
			if !inquote {
				return strings.TrimLeft(cmd, " \t")
			}
		case '"':
			if nslash%2 == 0 {
				// A doubled quote inside quotes is a literal quote, and ends them.
				if inquote && len(cmd) > 1 && cmd[1] == '"' {
					cmd = cmd[1:]
				}
				inquote = !inquote
			}
			nslash = 0
			continue
		case '\\':
			nslash++
			continue
		}
		nslash = 0
	}
	return ""
}
