// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package maven inspects Maven projects without executing build configuration.
package maven

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// FrameworkNames includes the JVM frameworks supported with Maven. Weaver
// requires SBT. Use cucumber-java to distinguish it from Cucumber.js.
var FrameworkNames = []string{"junit4", "junit5", "testng", "spock", "cucumber-java", "karate", "scalatest", "munit"}

type Dependency struct {
	Group    string `xml:"groupId"`
	Artifact string `xml:"artifactId"`
}

type Project struct {
	XMLName   xml.Name `xml:"project"`
	Directory string
	POM       string
	Name      string `xml:"name"`
	Group     string `xml:"groupId"`
	Artifact  string `xml:"artifactId"`
	Version   string `xml:"version"`
	Parent    *struct {
		Group        string  `xml:"groupId"`
		Artifact     string  `xml:"artifactId"`
		Version      string  `xml:"version"`
		RelativePath *string `xml:"relativePath"`
	} `xml:"parent"`
	Packaging    string       `xml:"packaging"`
	Dependencies []Dependency `xml:"dependencies>dependency"`
	Modules      []string     `xml:"modules>module"`
	Profiles     []struct {
		Dependencies []Dependency `xml:"dependencies>dependency"`
		Modules      []string     `xml:"modules>module"`
	} `xml:"profiles>profile"`
}

// Projects reads the default reactor and existing profile module candidates.
// Missing profile-only modules are optional; Maven determines active profiles.
func Projects(root string) ([]Project, error) {
	return projectsFromPOM(root, "pom.xml")
}

// TestdriveProjects inspects the same POM that the validated command executes.
func TestdriveProjects(root, command string, args []string) ([]Project, error) {
	pom, err := testdrivePOM(root, command, args)
	if err != nil {
		return nil, err
	}
	return projectsFromPOM(root, pom)
}

func projectsFromPOM(root, pom string) ([]Project, error) {
	canonical, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	canonical, err = filepath.EvalSymlinks(canonical)
	if err != nil {
		return nil, err
	}
	var projects []Project
	seen := map[string]bool{}
	var visit func(string) error
	visit = func(pom string) error {
		resolved, err := resolvePOM(canonical, pom)
		if err != nil {
			return err
		}
		if seen[resolved] {
			return nil
		}
		if len(seen) >= 256 {
			return fmt.Errorf("maven reactor exceeds 256 modules")
		}
		seen[resolved] = true
		data, err := os.ReadFile(filepath.Join(canonical, resolved))
		if err != nil {
			return err
		}
		var project Project
		if err := xml.Unmarshal(data, &project); err != nil {
			return fmt.Errorf("parse %s: %w", resolved, err)
		}
		project.POM, project.Directory = resolved, filepath.Dir(resolved)
		projects = append(projects, project)
		visitModule := func(module string, optional bool) error {
			if strings.Contains(module, "${") || !filepath.IsLocal(module) {
				return fmt.Errorf("cannot statically resolve Maven module %q", module)
			}
			err := visit(filepath.Join(project.Directory, module))
			if optional && errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		for _, module := range project.Modules {
			if err := visitModule(module, false); err != nil {
				return err
			}
		}
		for _, profile := range project.Profiles {
			for _, module := range profile.Modules {
				if err := visitModule(module, true); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(pom); err != nil {
		return nil, err
	}
	return inheritDependencies(projects)
}

// Resolve only parents already inside the declared reactor, with matching Maven
// coordinates. An aggregator alone does not establish dependency inheritance.
// Remote parents, property interpolation and active profiles remain candidates
// requiring a successful execution/control, not an inferred effective POM.
func inheritDependencies(projects []Project) ([]Project, error) {
	byPOM := map[string]int{}
	for i, project := range projects {
		byPOM[project.POM] = i
		if parent := project.Parent; parent != nil {
			if project.Group == "" {
				projects[i].Group = parent.Group
			}
			if project.Version == "" {
				projects[i].Version = parent.Version
			}
		}
	}
	state := make([]uint8, len(projects))
	var resolve func(int) error
	resolve = func(i int) error {
		if state[i] == 1 {
			return fmt.Errorf("cyclic local Maven parents at %s", projects[i].Directory)
		}
		if state[i] == 2 {
			return nil
		}
		state[i] = 1
		project := &projects[i]
		if parent := project.Parent; parent != nil {
			relative := "../pom.xml"
			if parent.RelativePath != nil {
				relative = strings.TrimSpace(*parent.RelativePath)
			}
			if relative != "" {
				if j, ok := byPOM[filepath.Clean(filepath.Join(project.Directory, relative))]; ok {
					candidate := projects[j]
					if parent.Group == candidate.Group && parent.Artifact == candidate.Artifact && parent.Version == candidate.Version {
						if err := resolve(j); err != nil {
							return err
						}
						project.Dependencies = append(slices.Clone(projects[j].Dependencies), project.Dependencies...)
					}
				}
			}
		}
		state[i] = 2
		return nil
	}
	for i := range projects {
		if err := resolve(i); err != nil {
			return nil, err
		}
	}
	return projects, nil
}

func (p Project) Frameworks() []string {
	deps := slices.Clone(p.Dependencies)
	for _, profile := range p.Profiles {
		deps = append(deps, profile.Dependencies...)
	}
	var result []string
	for _, dep := range deps {
		name := ""
		switch {
		case dep.Group == "junit" && dep.Artifact == "junit":
			name = "junit4"
		case dep.Group == "org.junit.jupiter" && strings.HasPrefix(dep.Artifact, "junit-jupiter"):
			name = "junit5"
		case dep.Group == "org.testng" && dep.Artifact == "testng":
			name = "testng"
		case dep.Group == "org.spockframework" && dep.Artifact == "spock-core":
			name = "spock"
		case dep.Group == "io.cucumber" && (dep.Artifact == "cucumber-java" || dep.Artifact == "cucumber-java8"):
			name = "cucumber-java"
		case (dep.Group == "com.intuit.karate" || dep.Group == "io.karatelabs") && strings.HasPrefix(dep.Artifact, "karate-"):
			name = "karate"
		case dep.Group == "org.scalatest" && strings.HasPrefix(dep.Artifact, "scalatest"):
			name = "scalatest"
		case dep.Group == "org.scalameta" && strings.HasPrefix(dep.Artifact, "munit_"):
			name = "munit"
		}
		if name != "" && !slices.Contains(result, name) {
			result = append(result, name)
		}
	}
	slices.Sort(result)
	return result
}

func DefaultCommand() (string, []string) {
	if info, err := os.Stat("mvnw"); err == nil && !info.IsDir() {
		return "./mvnw", []string{"test"}
	}
	return "mvn", []string{"test"}
}

// IsTestCommand recognizes literal Maven lifecycle phases, never substrings of
// properties (such as -Dmessage=test). Other shells/wrappers require review.
func IsTestCommand(command string, args []string) bool {
	base := filepath.Base(command)
	if !slices.Contains([]string{"mvn", "mvnw", "mvn.cmd", "mvnw.cmd"}, base) {
		return false
	}
	for i := 0; i < len(args); i++ {
		if slices.Contains([]string{"-f", "--file", "-pl", "--projects", "-P", "--activate-profiles", "-s", "--settings", "-gs", "--global-settings", "-t", "--toolchains", "-T", "--threads", "-rf", "--resume-from"}, args[i]) {
			i++
			continue
		}
		if slices.Contains([]string{"test", "prepare-package", "package", "pre-integration-test", "integration-test", "post-integration-test", "verify", "install", "deploy", "surefire:test", "failsafe:integration-test"}, args[i]) {
			return true
		}
	}
	return false
}

// ValidateTestdriveCommand keeps project selection inside the disposable copy
// and prevents publishing artifacts as a side effect of local validation.
func ValidateTestdriveCommand(root, command string, args []string) error {
	_, err := testdrivePOM(root, command, args)
	return err
}

func testdrivePOM(root, command string, args []string) (string, error) {
	if !IsTestCommand(command, args) {
		return "", fmt.Errorf("select a literal mvn/mvnw test or verify command; shell wrappers and other build systems require separate validation")
	}
	pom := "pom.xml"
	selected := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		goal, _, _ := strings.Cut(arg, "@")
		if !strings.HasPrefix(arg, "-") && (goal == "deploy" || strings.HasSuffix(goal, ":deploy") || strings.HasSuffix(goal, ":deploy-file") || goal == "site-deploy") {
			return "", fmt.Errorf("maven publishing goals are not supported by local testdrive; select the test/verify portion of the CI command")
		}
		file := ""
		if arg == "-f" || arg == "--file" {
			if i+1 >= len(args) {
				return "", fmt.Errorf("maven file option needs a repository-relative path")
			}
			file = args[i+1]
			i++
		}
		if value, ok := strings.CutPrefix(arg, "--file="); ok {
			file = value
		}
		if strings.HasPrefix(arg, "-f") && len(arg) > 2 && !strings.HasPrefix(arg, "--") && !slices.Contains([]string{"-ff", "-fae", "-fn"}, arg) {
			file = strings.TrimPrefix(arg[2:], "=")
		}
		if file != "" {
			if selected {
				return "", fmt.Errorf("select only one Maven POM with -f/--file")
			}
			selected = true
			if !filepath.IsLocal(file) {
				return "", fmt.Errorf("maven POM must stay inside the disposable repository: %s", file)
			}
			pom = file
		}
	}
	resolved, err := resolvePOM(root, pom)
	if err != nil {
		return "", err
	}
	if err := validateImplicitArguments(root, pom, resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

// Implicit arguments have version-dependent parsing and precedence. Require
// explicit command inputs rather than certifying a different reactor or goals.
func validateImplicitArguments(root, pom, resolved string) error {
	for _, key := range []string{"MAVEN_ARGS", "MAVEN_BASEDIR", "MAVEN_PROJECTBASEDIR"} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return fmt.Errorf("%s supplies implicit Maven configuration; unset it and select the POM and arguments explicitly in --command for local testdrive", key)
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	selected := filepath.Join(root, pom)
	if info, err := os.Stat(selected); err != nil {
		return err
	} else if !info.IsDir() {
		selected = filepath.Dir(selected)
	}
	// Check both lexical and canonical locations: an internal POM alias can
	// have a different ancestor .mvn directory from the file it points to.
	for _, start := range []string{root, selected, filepath.Dir(filepath.Join(root, resolved))} {
		for directory := start; ; directory = filepath.Dir(directory) {
			config := filepath.Join(directory, ".mvn", "maven.config")
			data, err := os.ReadFile(config)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line != "" && !strings.HasPrefix(line, "#") {
					return fmt.Errorf("%s supplies implicit Maven arguments; move them into --command and disable this configuration in the disposable input before local testdrive", config)
				}
			}
			if info, err := os.Stat(filepath.Join(directory, ".mvn")); err == nil && info.IsDir() {
				break
			}
			if filepath.Dir(directory) == directory {
				break
			}
		}
	}
	return nil
}

// Maven accepts either a POM file or its directory. Check the resolved file,
// including the default and reactor POMs, before any build can follow a symlink.
func resolvePOM(root, pom string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, pom)
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		path = filepath.Join(path, "pom.xml")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || !filepath.IsLocal(relative) {
		return "", fmt.Errorf("maven POM must stay inside the disposable repository: %s", pom)
	}
	return relative, nil
}
