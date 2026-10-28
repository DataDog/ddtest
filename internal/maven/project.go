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
	Name         string       `xml:"name"`
	Artifact     string       `xml:"artifactId"`
	Dependencies []Dependency `xml:"dependencies>dependency"`
	// Parent is set when the POM inherits from a parent POM.
	Parent *struct{} `xml:"parent"`
	// DefaultGoal runs when a command names no goal.
	DefaultGoal string   `xml:"build>defaultGoal"`
	Modules     []string `xml:"modules>module"`
	Profiles    []struct {
		Dependencies []Dependency `xml:"dependencies>dependency"`
		Modules      []string     `xml:"modules>module"`
	} `xml:"profiles>profile"`
}

// Projects reads root's pom.xml and every module it declares; see ProjectsFrom.
func Projects(root string) ([]Project, error) {
	return ProjectsFrom(root, "pom.xml")
}

// ProjectsFrom reads pom, relative to root, and every module it declares,
// including modules of profiles that may be inactive. Profile modules that do
// not exist are skipped; Maven decides which profiles are active. POMs must
// resolve inside root, so a symlink cannot make inspection read another checkout.
func ProjectsFrom(root, pom string) ([]Project, error) {
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
			// A module may name a sibling (../child); resolvePOM rejects any
			// path that leaves the repository.
			path := filepath.Join(project.Directory, module)
			if strings.Contains(module, "${") || filepath.IsAbs(module) || !filepath.IsLocal(path) {
				return fmt.Errorf("cannot statically resolve Maven module %q in %s", module, resolved)
			}
			err := visit(path)
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

// optionsWithValue are the Maven CLI options whose next word is their value,
// never a goal, as documented by mvn --help.
var optionsWithValue = []string{
	"-b", "--builder", "--color", "-D", "--define", "-emp", "--encrypt-master-password", "-ep", "--encrypt-password",
	"-f", "--file", "-gs", "--global-settings", "-gt", "--global-toolchains", "-l", "--log-file",
	"-P", "--activate-profiles", "-pl", "--projects", "-rf", "--resume-from", "-s", "--settings",
	"-t", "--toolchains", "-T", "--threads",
}

// validationPhases are what local validation runs: the clean lifecycle, the
// default lifecycle up to verify, and the Surefire and Failsafe goals.
var validationPhases = []string{
	"pre-clean", "clean", "post-clean",
	"validate", "initialize", "generate-sources", "process-sources", "generate-resources", "process-resources", "compile", "process-classes",
	"generate-test-sources", "process-test-sources", "generate-test-resources", "process-test-resources", "test-compile", "process-test-classes",
	"test", "prepare-package", "package", "pre-integration-test", "integration-test", "post-integration-test", "verify",
	"surefire:test", "failsafe:integration-test", "failsafe:verify",
}

// testPhases run Surefire or Failsafe tests, including the phases after
// verify that a CI build may end with.
var testPhases = []string{"test", "prepare-package", "package", "pre-integration-test", "integration-test", "post-integration-test", "verify", "install", "deploy", "surefire:test", "failsafe:integration-test"}

// publishingPhases run every test phase, then write artifacts to the local
// repository or publish them.
var publishingPhases = []string{"install", "deploy"}

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

// CommandPOM returns the POM that -f/--file selects in a Maven command's
// arguments, or pom.xml. The path must stay inside the repository.
func CommandPOM(args []string) (string, error) {
	pom := ""
	for i := 0; i < len(args); i++ {
		arg, file := args[i], ""
		switch {
		case arg == "-f" || arg == "--file":
			if i+1 == len(args) {
				return "", fmt.Errorf("maven %s needs a POM path", arg)
			}
			file = args[i+1]
			i++
		case strings.HasPrefix(arg, "--file="):
			file = strings.TrimPrefix(arg, "--file=")
		case strings.HasPrefix(arg, "-f") && !strings.HasPrefix(arg, "--") && !slices.Contains([]string{"-fae", "-ff", "-fn"}, arg):
			file = strings.TrimPrefix(arg[2:], "=")
		default:
			continue
		}
		if pom != "" {
			return "", fmt.Errorf("maven command selects more than one POM")
		}
		if !filepath.IsLocal(file) {
			return "", fmt.Errorf("maven POM must stay inside the repository: %s", file)
		}
		pom = file
	}
	if pom == "" {
		return "pom.xml", nil
	}
	return pom, nil
}

// TestInvocation splits Maven arguments into what local validation runs and
// the goals it leaves out. Options are kept. install and deploy are replaced
// by verify, which runs the same tests without writing to ~/.m2 or publishing;
// other goals, such as javadoc:jar, are left out.
func TestInvocation(args []string) (validated, omitted []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case slices.Contains(optionsWithValue, arg):
			validated = append(validated, args[i:min(i+2, len(args))]...)
			i++
		case strings.HasPrefix(arg, "-") || slices.Contains(validationPhases, arg):
			validated = append(validated, arg)
		case slices.Contains(publishingPhases, arg):
			omitted = append(omitted, arg)
			if !slices.Contains(Goals(validated), "verify") {
				validated = append(validated, "verify")
			}
		default:
			omitted = append(omitted, arg)
		}
	}
	return validated, omitted
}

// SkipsTests reports whether Maven arguments skip test execution, as
// -DskipTests and -Dmaven.test.skip do, in any of their -D forms.
func SkipsTests(args []string) bool {
	for i := 0; i < len(args); i++ {
		property := ""
		switch arg := args[i]; {
		case (arg == "-D" || arg == "--define") && i+1 < len(args):
			property = args[i+1]
			i++
		case strings.HasPrefix(arg, "--define="):
			property = strings.TrimPrefix(arg, "--define=")
		case strings.HasPrefix(arg, "-D") && arg != "-D":
			property = strings.TrimPrefix(arg, "-D")
		case slices.Contains(optionsWithValue, arg):
			i++
		}
		name, value, _ := strings.Cut(property, "=")
		if (name == "skipTests" || name == "maven.test.skip") && value != "false" {
			return true
		}
	}
	return false
}

// Metadata reports Maven arguments that print help or the version and exit
// without building.
func Metadata(args []string) bool {
	return len(Goals(args)) == 0 && slices.ContainsFunc(args, func(arg string) bool {
		return slices.Contains([]string{"-v", "--version", "-h", "--help"}, arg)
	})
}

// Goals returns the phases and plugin goals in Maven arguments, without
// options or their values.
func Goals(args []string) []string {
	var goals []string
	for i := 0; i < len(args); i++ {
		switch {
		case slices.Contains(optionsWithValue, args[i]):
			i++
		case !strings.HasPrefix(args[i], "-"):
			goals = append(goals, args[i])
		}
	}
	return goals
}
