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

// FrameworkNames lists the JVM test frameworks supported with Maven. The
// cucumber-java name keeps Cucumber JVM distinct from Cucumber.js.
var FrameworkNames = []string{"junit4", "junit5", "testng", "spock", "cucumber-java", "karate", "scalatest", "munit"}

// maxModules bounds reactor traversal for pathological or generated POMs.
const maxModules = 256

type Dependency struct {
	Group    string `xml:"groupId"`
	Artifact string `xml:"artifactId"`
}

// Project is one POM of the reactor, read as written: properties, parents and
// BOMs are not resolved into an effective POM.
type Project struct {
	// Directory and POM are relative to the inspected root.
	Directory    string       `xml:"-"`
	POM          string       `xml:"-"`
	Artifact     string       `xml:"artifactId"`
	Dependencies []Dependency `xml:"dependencies>dependency"`
	Modules      []string     `xml:"modules>module"`
	Profiles     []struct {
		Dependencies []Dependency `xml:"dependencies>dependency"`
		Modules      []string     `xml:"modules>module"`
	} `xml:"profiles>profile"`
}

// Projects reads root's pom.xml and every module it declares, including
// modules of profiles that may be inactive. Profile modules that do not exist
// are skipped; Maven decides which profiles are active. POMs must resolve
// inside root, so a symlink cannot make inspection read another checkout.
func Projects(root string) ([]Project, error) {
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
	var visit func(pom string) error
	visit = func(pom string) error {
		resolved, err := resolvePOM(canonical, pom)
		if err != nil {
			return err
		}
		if seen[resolved] {
			return nil
		}
		if len(seen) == maxModules {
			return fmt.Errorf("maven reactor exceeds %d modules", maxModules)
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
				return fmt.Errorf("cannot statically resolve Maven module %q in %s", module, resolved)
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
	if err := visit("pom.xml"); err != nil {
		return nil, err
	}
	return projects, nil
}

// resolvePOM accepts a POM file or its directory, as Maven does, and returns
// its symlink-free path relative to root.
func resolvePOM(root, pom string) (string, error) {
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
		return "", fmt.Errorf("maven POM must stay inside the repository: %s", pom)
	}
	return relative, nil
}

// Frameworks returns the supported test frameworks the project declares,
// including profile dependencies. A declaration does not prove execution.
func (p Project) Frameworks() []string {
	dependencies := slices.Clone(p.Dependencies)
	for _, profile := range p.Profiles {
		dependencies = append(dependencies, profile.Dependencies...)
	}
	var names []string
	for _, dependency := range dependencies {
		if name := dependency.framework(); name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func (d Dependency) framework() string {
	switch {
	case d.Group == "junit" && d.Artifact == "junit":
		return "junit4"
	case d.Group == "org.junit.jupiter" && strings.HasPrefix(d.Artifact, "junit-jupiter"):
		return "junit5"
	case d.Group == "org.testng" && d.Artifact == "testng":
		return "testng"
	case d.Group == "org.spockframework" && d.Artifact == "spock-core":
		return "spock"
	case d.Group == "io.cucumber" && (d.Artifact == "cucumber-java" || d.Artifact == "cucumber-java8"):
		return "cucumber-java"
	case (d.Group == "com.intuit.karate" || d.Group == "io.karatelabs") && strings.HasPrefix(d.Artifact, "karate-"):
		return "karate"
	case d.Group == "org.scalatest" && strings.HasPrefix(d.Artifact, "scalatest"):
		return "scalatest"
	case d.Group == "org.scalameta" && strings.HasPrefix(d.Artifact, "munit_"):
		return "munit"
	}
	return ""
}

// Frameworks returns the supported test frameworks declared anywhere in the reactor.
func Frameworks(projects []Project) []string {
	var names []string
	for _, project := range projects {
		for _, name := range project.Frameworks() {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	slices.Sort(names)
	return names
}

// optionsWithValue are Maven CLI options whose next word is a value, never a goal.
var optionsWithValue = []string{"-f", "--file", "-pl", "--projects", "-P", "--activate-profiles", "-s", "--settings", "-gs", "--global-settings", "-t", "--toolchains", "-T", "--threads", "-rf", "--resume-from", "-l", "--log-file", "-b", "--builder"}

// testPhases are lifecycle phases and goals that run Surefire or Failsafe tests.
var testPhases = []string{"test", "prepare-package", "package", "pre-integration-test", "integration-test", "post-integration-test", "verify", "install", "deploy", "surefire:test", "failsafe:integration-test"}

// IsTestCommand reports whether a literal mvn or mvnw invocation reaches a
// test phase. Words that only contain a phase, such as -Dmessage=test, do not
// count; shell wrappers and other build tools are not recognized.
func IsTestCommand(command string, args []string) bool {
	if !slices.Contains([]string{"mvn", "mvnw", "mvn.cmd", "mvnw.cmd"}, filepath.Base(command)) {
		return false
	}
	for i := 0; i < len(args); i++ {
		if slices.Contains(optionsWithValue, args[i]) {
			i++
			continue
		}
		if slices.Contains(testPhases, args[i]) {
			return true
		}
	}
	return false
}
