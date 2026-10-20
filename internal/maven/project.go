// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package maven inspects Maven projects without executing build configuration.
package maven

import (
	"encoding/xml"
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

// Projects reads the declared reactor, including profile modules. Profile-only
// dependencies are candidates, not proof that a profile is active locally.
func Projects(root string) ([]Project, error) {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	var projects []Project
	seen := map[string]bool{}
	var visit func(string) error
	visit = func(directory string) error {
		directory, err := filepath.EvalSymlinks(directory)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(canonical, directory)
		if err != nil || (relative != "." && !filepath.IsLocal(relative)) {
			return fmt.Errorf("maven module escapes repository: %s", directory)
		}
		if seen[directory] {
			return nil
		}
		if len(seen) >= 256 {
			return fmt.Errorf("maven reactor exceeds 256 modules")
		}
		seen[directory] = true
		data, err := os.ReadFile(filepath.Join(directory, "pom.xml"))
		if err != nil {
			return err
		}
		var project Project
		if err := xml.Unmarshal(data, &project); err != nil {
			return fmt.Errorf("parse %s: %w", filepath.Join(relative, "pom.xml"), err)
		}
		project.Directory = relative
		projects = append(projects, project)
		modules := slices.Clone(project.Modules)
		for _, profile := range project.Profiles {
			modules = append(modules, profile.Modules...)
		}
		for _, module := range modules {
			if strings.Contains(module, "${") || !filepath.IsLocal(module) {
				return fmt.Errorf("cannot statically resolve Maven module %q", module)
			}
			if err := visit(filepath.Join(directory, module)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(canonical); err != nil {
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
		byPOM[filepath.Clean(filepath.Join(project.Directory, "pom.xml"))] = i
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
func ValidateTestdriveCommand(command string, args []string) error {
	if !IsTestCommand(command, args) {
		return fmt.Errorf("select a literal mvn/mvnw test or verify command; shell wrappers and other build systems require separate validation")
	}
	for i, arg := range args {
		if arg == "deploy" || strings.HasSuffix(arg, ":deploy") || arg == "site-deploy" {
			return fmt.Errorf("maven publishing goals are not supported by local testdrive; select the test/verify portion of the CI command")
		}
		file := ""
		if arg == "-f" || arg == "--file" {
			if i+1 >= len(args) {
				return fmt.Errorf("maven file option needs a repository-relative path")
			}
			file = args[i+1]
		}
		if value, ok := strings.CutPrefix(arg, "--file="); ok {
			file = value
		}
		if strings.HasPrefix(arg, "-f") && len(arg) > 2 && !strings.HasPrefix(arg, "--") {
			file = arg[2:]
		}
		if file != "" && !filepath.IsLocal(file) {
			return fmt.Errorf("maven POM must stay inside the disposable repository: %s", file)
		}
	}
	return nil
}
