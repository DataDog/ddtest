package onboard

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// SetupPlan keeps original bytes until every planned edit has been checked.
// It is deliberately ephemeral: it cannot be resumed after customer edits.
type SetupPlan struct {
	root   string
	edits  []setupEdit
	impact []string
}

// Configured reports that the repository already has every planned edit.
func (p *SetupPlan) Configured() bool { return len(p.edits) == 0 }

// LocalImpact describes how the edits change commands developers run, so it
// can be shown before they agree to apply them.
func (p *SetupPlan) LocalImpact() []string { return p.impact }

func (p *SetupPlan) Diff() (string, error) {
	var out strings.Builder
	for _, edit := range p.edits {
		directory, err := os.MkdirTemp("", "ddtest-setup-diff-")
		if err != nil {
			return "", err
		}
		before, after := filepath.Join(directory, "before"), filepath.Join(directory, "after")
		err = os.WriteFile(before, edit.before, 0600)
		if err == nil {
			err = os.WriteFile(after, edit.after, 0600)
		}
		if err != nil {
			_ = os.RemoveAll(directory)
			return "", err
		}
		command := exec.Command("git", "diff", "--no-color", "--no-index", "--no-ext-diff", "--no-textconv", "--text", "--", before, after)
		data, diffErr := command.CombinedOutput()
		_ = os.RemoveAll(directory)
		var status *exec.ExitError
		if diffErr != nil && (!errors.As(diffErr, &status) || status.ExitCode() != 1) {
			return "", fmt.Errorf("preview %s: %w: %s", edit.path, diffErr, data)
		}
		for _, line := range strings.SplitAfter(string(data), "\n") {
			switch {
			case strings.HasPrefix(line, "diff --git "):
				out.WriteString("diff --git " + strconv.Quote("a/"+edit.path) + " " + strconv.Quote("b/"+edit.path) + "\n")
				if edit.before == nil {
					out.WriteString("new file mode 100644\n")
				}
			case strings.HasPrefix(line, "--- "):
				if edit.before == nil {
					out.WriteString("--- /dev/null\n")
				} else {
					out.WriteString("--- " + strconv.Quote("a/"+edit.path) + "\n")
				}
			case strings.HasPrefix(line, "+++ "):
				out.WriteString("+++ " + strconv.Quote("b/"+edit.path) + "\n")
			default:
				out.WriteString(line)
			}
		}
	}
	return out.String(), nil
}

func (p *SetupPlan) Apply(allowDirty bool) ([]SetupChange, error) {
	return p.apply(allowDirty, os.Rename)
}

func (p *SetupPlan) apply(allowDirty bool, rename func(string, string) error) ([]SetupChange, error) {
	for _, edit := range p.edits {
		current, err := readSetupEdit(p.root, edit.path)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(current.before, edit.before) || (current.before == nil) != (edit.before == nil) {
			return nil, fmt.Errorf("%s changed during planning; rerun onboarding", edit.path)
		}
	}
	if !allowDirty && len(p.edits) > 0 {
		check := exec.Command("git", "rev-parse", "--show-toplevel")
		check.Dir = p.root
		if _, err := check.Output(); err == nil {
			args := []string{"status", "--porcelain=v1", "--untracked-files=all", "--"}
			for _, edit := range p.edits {
				args = append(args, edit.path)
			}
			command := exec.Command("git", args...)
			command.Dir = p.root
			status, err := command.Output()
			if err != nil {
				return nil, err
			}
			if len(status) > 0 {
				var dirty []string
				for _, line := range strings.Split(strings.TrimRight(string(status), "\n"), "\n") {
					if len(line) > 3 {
						dirty = append(dirty, line[3:])
					}
				}
				return nil, &SetupGap{"source edits", "Target files have uncommitted changes (" + strings.Join(dirty, ", ") + "). Review the preview, then commit/stash them or explicitly pass --allow-dirty with --apply --yes to preserve and extend those edits."}
			}
		}
	}
	// Stage every replacement first, so staging failures never leave a partial apply.
	staged := make([]string, len(p.edits))
	defer func() {
		for _, name := range staged {
			if name != "" {
				_ = os.Remove(name)
			}
		}
	}()
	for i, edit := range p.edits {
		name, err := stageSetupFile(filepath.Join(p.root, edit.path), edit.after, edit.mode)
		if err != nil {
			return nil, err
		}
		staged[i] = name
	}
	for i, edit := range p.edits {
		if err := rename(staged[i], filepath.Join(p.root, edit.path)); err != nil {
			failure := fmt.Errorf("write %s: %w", edit.path, err)
			for j := i - 1; j >= 0; j-- {
				previous := p.edits[j]
				path := filepath.Join(p.root, previous.path)
				var restoreErr error
				if previous.before == nil {
					restoreErr = os.Remove(path)
				} else {
					var backup string
					backup, restoreErr = stageSetupFile(path, previous.before, previous.mode)
					if restoreErr == nil {
						restoreErr = os.Rename(backup, path)
						_ = os.Remove(backup)
					}
				}
				if restoreErr != nil {
					failure = errors.Join(failure, fmt.Errorf("restore %s: %w", previous.path, restoreErr))
				}
			}
			return nil, failure
		}
	}
	changes := make([]SetupChange, 0, len(p.edits))
	for _, edit := range p.edits {
		changes = append(changes, SetupChange{Path: edit.path, Before: fmt.Sprintf("%x", sha256.Sum256(edit.before)), After: fmt.Sprintf("%x", sha256.Sum256(edit.after))})
	}
	return changes, nil
}

func stageSetupFile(path string, data []byte, mode os.FileMode) (string, error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".ddtest-setup-*")
	if err != nil {
		return "", err
	}
	name := file.Name()
	err = file.Chmod(mode)
	if err == nil {
		_, err = file.Write(data)
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}
