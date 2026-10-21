package runner

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// nestedGoVariable counts the ddto commands that run go around a process. A
// go on PATH that starts ddto, such as a script that runs ddto go, would make
// every ddto command start another, without end.
const nestedGoVariable = "DDTO_NESTED_GO"

// maxNestedGo is more ddto commands inside each other's tests than a project
// runs.
const maxNestedGo = 8

// NestGoCommand counts this ddto command, which runs go, for the commands it
// starts, and fails once too many ddto commands enclose it.
func NestGoCommand() error {
	depth, _ := strconv.Atoi(os.Getenv(nestedGoVariable))
	if depth >= maxNestedGo {
		goPath, err := exec.LookPath("go")
		if err != nil {
			goPath = "go"
		}
		return fmt.Errorf("%d ddto commands run inside each other: the go command on PATH, %s, starts ddto again; put the Go toolchain's go first on PATH", depth+1, goPath)
	}
	return os.Setenv(nestedGoVariable, strconv.Itoa(depth+1))
}

// IsGoCommand reports whether args run the go command, as in ddto go build.
func IsGoCommand(args []string) bool {
	return len(args) > 0 && strings.TrimSuffix(filepath.Base(args[0]), ".exe") == "go"
}

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
