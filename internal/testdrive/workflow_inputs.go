// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/kballard/go-shellquote"
)

// SetupFacts can add a missing local prerequisite, never remove a command,
// unresolved CI fragment, comparison, feature assertion or coverage requirement.
type SetupFacts = onboard.SetupFacts
type PrerequisiteFact = onboard.PrerequisiteFact

const maxSetupEntries = 32
const maxSetupServices = 4
const maxExpectedOutputs = 32
const maxRepositoryNesting = 16

// InputFingerprint binds facts and reports to source/configuration, runtime and
// relevant non-secret environment, and to the selected CI provider: facts and
// reports for one provider never certify another. Git-ignored caches/outputs
// are not audited.
func InputFingerprint(ctx context.Context, root string) (string, error) {
	// Porcelain paths are checkout-relative even when invoked from a package.
	// Use the whole checkout consistently for the index, status and file reads.
	if data, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--show-toplevel").Output(); err == nil {
		root = strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	}
	fingerprint, err := inputFingerprint(ctx, root, 0)
	if provider := onboard.ActiveCIProvider(); err == nil && provider != "" {
		fingerprint = fmt.Sprintf("%x", sha256.Sum256([]byte("ci-provider="+provider+"\x00"+fingerprint)))
	}
	// Reports certify what the user stated too: other statements need a new run.
	if statements := onboard.CIStatementsFingerprint(); err == nil && statements != "" {
		fingerprint = fmt.Sprintf("%x", sha256.Sum256([]byte("ci-statements="+statements+"\x00"+fingerprint)))
	}
	return fingerprint, err
}

func inputFingerprint(ctx context.Context, root string, depth int) (string, error) {
	if depth > maxRepositoryNesting {
		return "", fmt.Errorf("repository nesting exceeds 16 levels")
	}
	hash := sha256.New()
	var files, indexedNames []string
	gitlinks := map[string]string{}
	index, err := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "--stage", "-z").Output()
	if err == nil {
		tree, treeErr := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--verify", "HEAD^{tree}").Output()
		if treeErr != nil && ctx.Err() != nil {
			return "", ctx.Err()
		}
		_, _ = fmt.Fprintf(hash, "tree\x00%s\x00", tree)
		for _, entry := range bytes.Split(index, []byte{0}) {
			header, name, ok := bytes.Cut(entry, []byte{'\t'})
			if !ok {
				continue
			}
			path := string(name)
			if excludedFingerprintPath(path) {
				continue
			}
			indexedNames = append(indexedNames, path)
			_, _ = hash.Write(entry)
			_, _ = hash.Write([]byte{0})
			fields := strings.Fields(string(header))
			if len(fields) == 3 && fields[0] == "160000" {
				if fields[2] != "0" {
					return "", fmt.Errorf("resolve unmerged submodule %s before validation", path)
				}
				gitlinks[path] = fields[1]
				files = append(files, path)
			}
		}
		status, err := exec.CommandContext(ctx, "git", "-C", root, "status", "--porcelain=v2", "-z", "--untracked-files=all", "--ignore-submodules=all").Output()
		if err != nil {
			// A detached reference may not have its commit objects locally yet. The
			// index still binds staged content; diff-files binds working-tree changes.
			if treeErr == nil {
				return "", fmt.Errorf("read repository status: %w", err)
			}
			modified, diffErr := exec.CommandContext(ctx, "git", "-C", root, "diff-files", "--name-only", "-z").Output()
			untracked, untrackedErr := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "--others", "--exclude-standard", "-z").Output()
			if diffErr != nil || untrackedErr != nil {
				return "", fmt.Errorf("read repository status: %w", err)
			}
			status = nil
			for _, name := range strings.Split(string(append(modified, untracked...)), "\x00") {
				if name != "" {
					status = append(status, []byte("? "+name+"\x00")...)
				}
			}
		}
		records := strings.Split(string(status), "\x00")
		for i := 0; i < len(records); i++ {
			record := records[i]
			if record == "" {
				continue
			}
			var path string
			var modified bool
			switch record[0] {
			case '1', '2':
				count := 9
				if record[0] == '2' {
					count = 10
				}
				fields := strings.SplitN(record, " ", count)
				if len(fields) != count {
					return "", fmt.Errorf("invalid Git status record")
				}
				path = fields[count-1]
				modified = len(fields[1]) == 2 && fields[1][1] != '.'
				if record[0] == '2' {
					i++
				}
			case '?':
				path = strings.TrimPrefix(record, "? ")
				modified = true
			case 'u':
				fields := strings.SplitN(record, " ", 11)
				if len(fields) != 11 {
					return "", fmt.Errorf("invalid unmerged Git status")
				}
				path = fields[10]
				modified = true
			default:
				continue
			}
			if excludedFingerprintPath(path) {
				continue
			}
			_, _ = fmt.Fprintf(hash, "status\x00%s\x00", record)
			if modified {
				files = append(files, path)
			}
		}
	} else {
		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.IsDir() && slices.Contains([]string{".git", "node_modules", ".testoptimization", "coverage", "dist"}, entry.Name()) {
				return filepath.SkipDir
			}
			if !entry.IsDir() {
				relative, _ := filepath.Rel(root, path)
				files = append(files, relative)
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	// Include ignored installed manifests at the root and discovered workspace
	// packages. Lockfiles alone do not identify a manually replaced installation.
	packages := []string{"."}
	for _, name := range append(slices.Clone(files), indexedNames...) {
		if filepath.Base(name) == "package.json" && !strings.Contains(filepath.ToSlash(name), "node_modules/") {
			packages = append(packages, filepath.Dir(name))
		}
	}
	for _, directory := range slices.Compact(slices.Sorted(slices.Values(packages))) {
		for _, module := range []string{"dd-trace", "vitest", "jest", "mocha", "@playwright/test", "cypress", "@cucumber/cucumber", "@wdio/cli"} {
			files = append(files, filepath.Join(directory, "node_modules", module, "package.json"))
		}
	}
	slices.Sort(files)
	files = slices.Compact(files)
	for _, name := range files {
		if name == "" || excludedFingerprintPath(name) {
			continue
		}
		if !filepath.IsLocal(name) {
			return "", fmt.Errorf("non-local input path %q", name)
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		path := filepath.Join(root, name)
		if commit, ok := gitlinks[name]; ok {
			state, err := submoduleFingerprint(ctx, path, depth+1)
			if err != nil {
				return "", fmt.Errorf("submodule %s: %w", name, err)
			}
			_, _ = fmt.Fprintf(hash, "%s\x00gitlink\x00%s\x00%s\x00", name, commit, state)
			continue
		}
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			_, _ = fmt.Fprintf(hash, "%s\x00missing\x00", name)
			continue
		}
		if err != nil {
			return "", err
		}
		if info.IsDir() {
			// Git represents an untracked nested checkout as one directory entry.
			// Recurse into its own index/worktree rather than dropping its inputs.
			state, err := inputFingerprint(ctx, path, depth+1)
			if err != nil {
				return "", fmt.Errorf("nested repository %s: %w", name, err)
			}
			head, err := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--verify", "HEAD").Output()
			if err != nil {
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				head = nil // An unborn nested repository still has source inputs.
			}
			_, _ = fmt.Fprintf(hash, "%s\x00repository\x00%s\x00%s\x00", filepath.ToSlash(name), strings.TrimSpace(string(head)), state)
			continue
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00", filepath.ToSlash(name), info.Mode())
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return "", err
			}
			_, _ = io.WriteString(hash, link)
		} else {
			if !info.Mode().IsRegular() {
				return "", fmt.Errorf("input is not a regular file: %s", name)
			}
			file, err := os.Open(path)
			if err != nil {
				return "", err
			}
			_, err = io.Copy(hash, file)
			closeErr := file.Close()
			if err != nil {
				return "", err
			}
			if closeErr != nil {
				return "", closeErr
			}
		}
		_, _ = hash.Write([]byte{0})
	}
	for _, key := range []string{"NODE_OPTIONS", "NODE_ENV", "CI", "TZ"} {
		_, _ = fmt.Fprintf(hash, "%s=%s\x00", key, os.Getenv(key))
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func submoduleFingerprint(ctx context.Context, path string, depth int) (string, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "uninitialized", nil
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("expected a real submodule directory")
	}
	if _, err := os.Lstat(filepath.Join(path, ".git")); os.IsNotExist(err) {
		entries, err := os.ReadDir(path)
		if err == nil && len(entries) != 0 {
			err = fmt.Errorf("nonempty submodule is not initialized; initialize it before validation")
		}
		return "uninitialized", err
	} else if err != nil {
		return "", err
	}
	head, err := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("read checked-out commit: %w", err)
	}
	contents, err := inputFingerprint(ctx, path, depth)
	return strings.TrimSpace(string(head)) + ":" + contents, err
}

func readSetupFacts(root, path string) ([]byte, error) {
	root = workspace.CanonicalRoot(root)
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil || filepath.IsLocal(relative) {
		return nil, fmt.Errorf("store --facts outside the checkout so creating it does not change its input fingerprint")
	}
	file, err := os.Open(absolute)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("facts exceed 1 MiB")
	}
	return data, nil
}

func applySetupFacts(root, path, fingerprint string, scope *onboard.ValidationScope) (string, error) {
	data, err := readSetupFacts(root, path)
	if err != nil {
		return "", err
	}
	return applySetupFactsData(root, data, fingerprint, scope)
}

func applySetupFactsData(root string, data []byte, fingerprint string, scope *onboard.ValidationScope) (string, error) {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var facts SetupFacts
	if err := decoder.Decode(&facts); err != nil {
		return "", err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return "", fmt.Errorf("facts must contain one JSON document")
	}
	if facts.SchemaVersion != 2 || facts.Fingerprint != fingerprint {
		return "", fmt.Errorf("facts schema/input fingerprint does not match this repository; inspect current inputs and supply fresh facts")
	}
	if len(facts.Prerequisites)+len(facts.Configurations) == 0 || len(facts.Prerequisites)+len(facts.Configurations) > maxSetupEntries {
		return "", fmt.Errorf("facts require 1–32 explicit setup entries")
	}
	updated := append([]onboard.TestCommand(nil), scope.Commands...)
	for _, fact := range facts.Prerequisites {
		index, matches := -1, 0
		for i, command := range updated {
			if command.Command == fact.ForCommand && command.Directory == "." {
				index, matches = i, matches+1
			}
		}
		if matches != 1 {
			return "", fmt.Errorf("fact does not match exactly one existing root command; use schema_version=2 and configuration_id")
		}
		if err := addPrerequisiteFact(root, &updated[index], fact); err != nil {
			return "", err
		}
	}
	if len(facts.Configurations) > 0 && facts.SchemaVersion != 2 {
		return "", fmt.Errorf("configuration facts require schema_version=2")
	}
	for _, fact := range facts.Configurations {
		index := slices.IndexFunc(updated, func(command onboard.TestCommand) bool {
			return command.ID == fact.ConfigurationID && fact.ConfigurationID != ""
		})
		if index < 0 {
			return "", fmt.Errorf("unknown configuration_id %q; facts cannot replace discovered commands", fact.ConfigurationID)
		}
		entry := &updated[index]
		if fact.Directory != "" && filepath.Clean(fact.Directory) != filepath.Clean(entry.Directory) {
			return "", fmt.Errorf("working directory must agree with discovered CI configuration %s", entry.ID)
		}
		if _, err := localSetupDirectory(root, entry.Directory); err != nil {
			return "", err
		}
		for _, build := range fact.Prerequisites {
			if err := addPrerequisiteFact(root, entry, build); err != nil {
				return "", err
			}
		}
		if len(fact.Services) > maxSetupServices {
			return "", fmt.Errorf("at most four application services per configuration")
		}
		if fact.PortOverride != nil {
			if len(fact.Services) > 0 {
				return "", fmt.Errorf("do not duplicate action-owned startup as a service fact")
			}
			if err := applyPortFact(root, entry, *fact.PortOverride); err != nil {
				return "", err
			}
		}
		for _, service := range fact.Services {
			if service.Directory == "" {
				service.Directory = entry.Directory
			}
			if err := validateServiceFact(root, service); err != nil {
				return "", err
			}
			entry.Services = append(append([]onboard.ServiceCommand(nil), entry.Services...), service)
		}
	}
	scope.Commands = updated
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func addPrerequisiteFact(root string, entry *onboard.TestCommand, fact PrerequisiteFact) error {
	if fact.ForCommand != "" && fact.ForCommand != entry.Command {
		return fmt.Errorf("prerequisite must target its discovered command")
	}
	if fact.Directory == "" {
		fact.Directory = entry.Directory
	}
	directory, err := localSetupDirectory(root, fact.Directory)
	if err != nil {
		return err
	}
	if err := validatePrerequisiteFact(root, directory, fact); err != nil {
		return err
	}
	for i, build := range entry.Prerequisites {
		if build.Kind == onboard.PreparationNeedsFacts && build.Command == shellquote.Join(fact.Argv...) && build.Directory == fact.Directory {
			entry.Prerequisites = append([]onboard.BuildCommand(nil), entry.Prerequisites...)
			entry.Prerequisites[i].ExpectedFiles = fact.ExpectedFiles
			entry.Prerequisites[i].FactSource = "agent-supplied; declared CI prerequisite with required expected outputs"
			return nil
		}
	}
	entry.Prerequisites = append(append([]onboard.BuildCommand(nil), entry.Prerequisites...), onboard.BuildCommand{Directory: fact.Directory, Command: shellquote.Join(fact.Argv...), Kind: "agent-prerequisite", ExpectedFiles: fact.ExpectedFiles, FactSource: "agent-supplied; successful execution and expected output files required"})
	return nil
}

func validatePrerequisiteFact(root, directory string, fact PrerequisiteFact) error {
	if len(fact.Argv) == 0 {
		return fmt.Errorf("prerequisite requires an argv")
	}
	if len(fact.ExpectedFiles) == 0 || len(fact.ExpectedFiles) > maxExpectedOutputs {
		return fmt.Errorf("prerequisite requires 1-%d expected repository files", maxExpectedOutputs)
	}
	for _, arg := range fact.Argv {
		if strings.ContainsAny(arg, "\x00\n\r") || slices.Contains([]string{";", "&&", "||", "|", ">", "<"}, arg) {
			return fmt.Errorf("prerequisite must be a single argv, not a shell program")
		}
	}
	switch fact.Argv[0] {
	case "npm", "pnpm", "yarn":
		if len(fact.Argv) != 3 || fact.Argv[1] != "run" {
			return fmt.Errorf("prerequisite must name an existing package script with <manager> run <script>")
		}
		var manifest struct {
			Scripts map[string]string `json:"scripts"`
		}
		data, err := os.ReadFile(filepath.Join(directory, "package.json"))
		if err != nil {
			return err
		}
		if err = json.Unmarshal(data, &manifest); err != nil {
			return err
		}
		if manifest.Scripts[fact.Argv[2]] == "" {
			return fmt.Errorf("unknown prerequisite script %q", fact.Argv[2])
		}
	case "node", "sh", "bash":
		if len(fact.Argv) < 2 || !filepath.IsLocal(fact.Argv[1]) {
			return fmt.Errorf("prerequisite script must be repository-local")
		}
		if _, err := repositoryScript(root, directory, fact.Argv[1]); err != nil {
			return err
		}
	default:
		// Discovery also offers a repository shell script run directly.
		if !strings.HasPrefix(fact.Argv[0], "./") {
			return fmt.Errorf("prerequisite must use an installed package script, or node, sh, bash or ./ with a local script")
		}
		info, err := repositoryScript(root, directory, fact.Argv[0])
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("prerequisite script %s is not executable", fact.Argv[0])
		}
	}
	for _, name := range fact.ExpectedFiles {
		if !filepath.IsLocal(name) || strings.HasPrefix(filepath.ToSlash(name), "node_modules/") || strings.HasPrefix(filepath.ToSlash(name), ".git/") || strings.HasPrefix(filepath.ToSlash(name), ".testoptimization/") {
			return fmt.Errorf("expected output must be a repository application file: %q", name)
		}
	}
	return nil
}

// repositoryScript resolves a prerequisite script from directory, following
// symlinks, and requires a regular file inside the repository.
func repositoryScript(root, directory, name string) (os.FileInfo, error) {
	resolved, err := filepath.EvalSymlinks(filepath.Join(directory, name))
	if err != nil {
		return nil, err
	}
	local, err := filepath.Rel(workspace.CanonicalRoot(root), resolved)
	if err != nil || !filepath.IsLocal(local) {
		return nil, fmt.Errorf("prerequisite script escapes the repository")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("prerequisite script %s is not a regular file", name)
	}
	return info, nil
}

func excludedFingerprintPath(name string) bool {
	name = filepath.ToSlash(name)
	return name == ".testoptimization" || strings.HasPrefix(name, ".testoptimization/")
}
