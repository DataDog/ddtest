// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
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
	"github.com/kballard/go-shellquote"
)

// SetupFacts can add a missing local prerequisite, never remove a command,
// unresolved CI fragment, comparison, feature assertion or coverage requirement.
type SetupFacts = onboard.SetupFacts
type PrerequisiteFact = onboard.PrerequisiteFact

// InputFingerprint binds facts and reports to source/configuration, runtime and
// relevant non-secret environment. Git-ignored caches/outputs are not audited.
func InputFingerprint(ctx context.Context, root string) (string, error) {
	var files []string
	data, err := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "--cached", "--others", "--exclude-standard", "-z").Output()
	if err == nil {
		files = strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
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
	files = append(files, "node_modules/vitest/package.json", "node_modules/jest/package.json", "node_modules/mocha/package.json", "node_modules/@playwright/test/package.json", "node_modules/cypress/package.json", "node_modules/@cucumber/cucumber/package.json", "node_modules/@wdio/cli/package.json")
	slices.Sort(files)
	files = slices.Compact(files)
	hash := sha256.New()
	for _, name := range files {
		if name == "" || strings.HasPrefix(filepath.ToSlash(name), ".testoptimization/") {
			continue
		}
		if !filepath.IsLocal(name) {
			return "", fmt.Errorf("non-local input path %q", name)
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			_, _ = fmt.Fprintf(hash, "%s\x00missing\x00", name)
			continue
		}
		if err != nil {
			return "", err
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

func applySetupFacts(root, path, fingerprint string, scope *onboard.ValidationScope) (string, error) {
	root = canonicalWorkflowRoot(root)
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil || filepath.IsLocal(relative) {
		return "", fmt.Errorf("store --facts outside the checkout so creating it does not change its input fingerprint")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return "", err
	}
	if len(data) > 1<<20 {
		return "", fmt.Errorf("facts exceed 1 MiB")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var facts SetupFacts
	if err := decoder.Decode(&facts); err != nil {
		return "", err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return "", fmt.Errorf("facts must contain one JSON document")
	}
	if (facts.SchemaVersion != 1 && facts.SchemaVersion != 2) || facts.Fingerprint != fingerprint {
		return "", fmt.Errorf("facts schema/input fingerprint does not match this repository; inspect current inputs and supply fresh facts")
	}
	if len(facts.Prerequisites)+len(facts.Configurations) == 0 || len(facts.Prerequisites)+len(facts.Configurations) > 32 {
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
		if len(fact.Services) > 4 {
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
	entry.Prerequisites = append(append([]onboard.BuildCommand(nil), entry.Prerequisites...), onboard.BuildCommand{Directory: fact.Directory, Command: shellquote.Join(fact.Argv...), Kind: "agent-prerequisite", ExpectedFiles: fact.ExpectedFiles, FactSource: "agent-supplied; successful execution and expected output files required"})
	return nil
}

func localSetupDirectory(root, relative string) (string, error) {
	if !filepath.IsLocal(relative) {
		return "", fmt.Errorf("setup working directory must stay in the repository: %q", relative)
	}
	directory, err := filepath.EvalSymlinks(filepath.Join(root, relative))
	if err != nil {
		return "", err
	}
	local, err := filepath.Rel(canonicalWorkflowRoot(root), directory)
	if err != nil || !filepath.IsLocal(local) {
		return "", fmt.Errorf("setup working directory escapes repository")
	}
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("setup working directory is not a directory")
	}
	return directory, nil
}

func validatePrerequisiteFact(root, directory string, fact PrerequisiteFact) error {
	if len(fact.Argv) < 2 || len(fact.ExpectedFiles) == 0 || len(fact.ExpectedFiles) > 32 {
		return fmt.Errorf("prerequisite requires an argv and expected repository files")
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
	case "node":
		if !filepath.IsLocal(fact.Argv[1]) {
			return fmt.Errorf("prerequisite script must be repository-local")
		}
		resolved, err := filepath.EvalSymlinks(filepath.Join(directory, fact.Argv[1]))
		if err != nil {
			return err
		}
		local, err := filepath.Rel(canonicalWorkflowRoot(root), resolved)
		if err != nil || !filepath.IsLocal(local) {
			return fmt.Errorf("prerequisite script escapes the repository")
		}
	default:
		return fmt.Errorf("prerequisite must use an installed package script or node with a local script")
	}
	for _, name := range fact.ExpectedFiles {
		if !filepath.IsLocal(name) || strings.HasPrefix(filepath.ToSlash(name), "node_modules/") || strings.HasPrefix(filepath.ToSlash(name), ".git/") || strings.HasPrefix(filepath.ToSlash(name), ".testoptimization/") {
			return fmt.Errorf("expected output must be a repository application file: %q", name)
		}
	}
	return nil
}
