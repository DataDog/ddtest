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
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// FrameworkNames lists the JVM test frameworks supported with Maven. The
// cucumber-java name keeps Cucumber JVM distinct from Cucumber.js.
var FrameworkNames = []string{"junit4", "junit5", "testng", "spock", "cucumber-java", "karate", "scalatest", "munit"}

// DisplayNames labels each framework in output.
var DisplayNames = map[string]string{
	"junit4": "JUnit 4", "junit5": "JUnit 5", "testng": "TestNG", "spock": "Spock",
	"cucumber-java": "Cucumber JVM", "karate": "Karate", "scalatest": "ScalaTest", "munit": "Scala MUnit",
}

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
		Resources    []Resource   `xml:"build>resources>resource"`
	} `xml:"profiles>profile"`
	// Resources are the main resources Maven packages into the artifact.
	Resources []Resource `xml:"build>resources>resource"`
}

// Resource is a directory of files Maven copies into the artifact, filtered by
// Ant-style include and exclude patterns.
type Resource struct {
	// POM declares the resource, relative to the inspected root.
	POM       string   `xml:"-"`
	Directory string   `xml:"directory"`
	Includes  []string `xml:"includes>include"`
	Excludes  []string `xml:"excludes>exclude"`
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

// afterTestPhases run after Surefire's test phase: packaging, integration
// tests and the checks bound to verify.
var afterTestPhases = []string{"prepare-package", "package", "pre-integration-test", "integration-test", "post-integration-test", "verify", "failsafe:integration-test", "failsafe:verify"}

// UpToTest replaces the phases and goals of Maven arguments that run after the
// test phase with test, once, keeping every option and earlier goal.
func UpToTest(args []string) []string {
	var result []string
	test := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case slices.Contains(optionsWithValue, arg):
			result = append(result, args[i:min(i+2, len(args))]...)
			i++
			continue
		case strings.HasPrefix(arg, "-"):
		case slices.Contains(afterTestPhases, shortGoal(arg)) || arg == "test":
			if test {
				continue
			}
			arg, test = "test", true
		}
		result = append(result, arg)
	}
	return result
}

// publishingPhases run every test phase, then write artifacts to the local
// repository or publish them.
var publishingPhases = []string{"install", "deploy"}

// pluginPrefixes are the prefixes of the test plugins' fully qualified goals.
var pluginPrefixes = map[string]string{"maven-surefire-plugin": "surefire", "maven-failsafe-plugin": "failsafe"}

// shortGoal writes a fully qualified test plugin goal, such as
// org.apache.maven.plugins:maven-surefire-plugin:3.5.4:test, by its prefix.
func shortGoal(goal string) string {
	parts := strings.Split(goal, ":")
	if len(parts) == 3 || len(parts) == 4 {
		if prefix, ok := pluginPrefixes[parts[1]]; ok {
			return prefix + ":" + parts[len(parts)-1]
		}
	}
	return goal
}

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
		if slices.Contains(testPhases, shortGoal(args[i])) {
			return true
		}
	}
	return false
}

// CommandPOM returns the POM that -f/--file selects in a Maven command's
// arguments, relative to its working directory, or pom.xml.
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
		// The caller resolves the path from the command's working directory
		// and confines it to the repository.
		if filepath.IsAbs(file) {
			return "", fmt.Errorf("maven POM must be a repository path: %s", file)
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
		case strings.HasPrefix(arg, "-") || slices.Contains(validationPhases, shortGoal(arg)):
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
		if (name == "skipTests" || name == "maven.test.skip") && !strings.EqualFold(value, "false") {
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

// ModuleDirectories maps the names the Java agent reports as test.module, a
// project's <name> or else its artifactId, to the project's directory. A name
// that several projects share maps to "".
func ModuleDirectories(projects []Project) map[string]string {
	modules := map[string]string{}
	for _, project := range projects {
		for _, name := range []string{project.Name, project.Artifact} {
			if name == "" || strings.Contains(name, "${") {
				continue
			}
			if previous, ok := modules[name]; ok && previous != project.Directory {
				modules[name] = ""
			} else {
				modules[name] = project.Directory
			}
		}
	}
	return modules
}

// PackagedResources returns the main resources that every pom.xml under root,
// and every other POM listed in poms (relative to root), declares in its
// build or any profile, with directories relative to root. A directory under
// the build directory is build output and left out; one built from other
// properties is returned unresolved, with its ${...}.
func PackagedResources(root string, poms []string) ([]Resource, error) {
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && slices.Contains([]string{".git", "target", "node_modules", ".testoptimization"}, entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != "pom.xml" || !entry.Type().IsRegular() {
			return nil
		}
		pom, err := filepath.Rel(root, path)
		poms = append(poms, pom)
		return err
	})
	if err != nil {
		return nil, err
	}
	var resources []Resource
	for _, pom := range slices.Compact(slices.Sorted(slices.Values(poms))) {
		declared, err := pomResources(root, pom)
		if err != nil {
			return nil, err
		}
		resources = append(resources, declared...)
	}
	return resources, nil
}

// pomResources returns the main resources pom declares, as PackagedResources.
func pomResources(root, pom string) ([]Resource, error) {
	data, err := os.ReadFile(filepath.Join(root, pom))
	if err != nil {
		return nil, err
	}
	var project Project
	if err := xml.Unmarshal(data, &project); err != nil {
		return nil, fmt.Errorf("parse %s: %w", pom, err)
	}
	declared := slices.Clone(project.Resources)
	for _, profile := range project.Profiles {
		declared = append(declared, profile.Resources...)
	}
	var resources []Resource
	for _, resource := range declared {
		directory := strings.TrimSpace(resource.Directory)
		if directory == "" || strings.HasPrefix(directory, "${project.build.directory}") {
			continue
		}
		for _, base := range []string{"${project.basedir}", "${basedir}"} {
			if rest, ok := strings.CutPrefix(directory, base); ok {
				directory = "." + rest
			}
		}
		switch {
		case strings.Contains(directory, "${"):
		case filepath.IsAbs(directory):
			// An absolute directory may still name the checkout.
			absolute, err := filepath.Abs(root)
			if err != nil {
				return nil, err
			}
			if directory, err = filepath.Rel(absolute, directory); err != nil {
				return nil, err
			}
		default:
			directory = filepath.Join(filepath.Dir(pom), directory)
		}
		resource.POM, resource.Directory = pom, directory
		resources = append(resources, resource)
	}
	return resources, nil
}
