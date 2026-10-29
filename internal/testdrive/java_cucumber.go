// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bufio"
	"cmp"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/maven"
)

// javaCucumberFeature is the probe feature, classpath:ddtest/DdtestValidation.feature.
// Surefire reports a feature's name as its class, so the feature is named
// javaProbeClass and its scenario after the probe method.
const javaCucumberFeature = `Feature: ` + javaProbeClass + `

  Scenario: ddtestProbe
    When the ddtest validation probe runs
`

// javaCucumberProbeBody records each execution and fails as DDTEST_PROBE_MODE
// asks, as javaProbeSource does.
const javaCucumberProbeBody = `        java.nio.file.Path state = java.nio.file.Paths.get(System.getenv("DDTEST_PROBE_STATE"));
        java.nio.file.Files.write(state, "run\n".getBytes(java.nio.charset.StandardCharsets.UTF_8), java.nio.file.StandardOpenOption.CREATE, java.nio.file.StandardOpenOption.APPEND);
        int attempts = java.nio.file.Files.readAllLines(state).size();
        String mode = System.getenv("DDTEST_PROBE_MODE");
        if ("fail".equals(mode) || ("fail-once".equals(mode) && attempts == 1)) {
            throw new AssertionError("ddtest intentional failure");
        }
`

// javaCucumberSteps is the probe's step definition in the project's glue
// package (the default package when empty): an annotated method, or for the
// lambda backend of cucumber-java8, a step registered in the constructor.
func javaCucumberSteps(glue string, lambda bool) string {
	source := ""
	if glue != "" {
		source = "package " + glue + ";\n\n"
	}
	if lambda {
		return source + `public class DdtestValidationSteps implements io.cucumber.java8.En {
    public DdtestValidationSteps() {
        When("the ddtest validation probe runs", () -> {
` + javaCucumberProbeBody + `        });
    }
}
`
	}
	return source + `public class DdtestValidationSteps {
    @io.cucumber.java.en.When("the ddtest validation probe runs")
    public void ddtestProbe() throws Exception {
` + javaCucumberProbeBody + `    }
}
`
}

// javaCucumberLambda reports a module whose glue uses only the lambda backend of
// cucumber-java8, which has no step annotations: as its test sources show, or
// without glue sources, as its POM declares.
func javaCucumberLambda(checkout, module string, sources backendUse) bool {
	if sources.lambda || sources.annotations {
		return sources.lambda && !sources.annotations
	}
	projects, err := maven.ProjectsFrom(checkout, filepath.Join(module, "pom.xml"))
	if err != nil || len(projects) == 0 {
		return false
	}
	dependencies := slices.Clone(projects[0].Dependencies)
	for _, profile := range projects[0].Profiles {
		dependencies = append(dependencies, profile.Dependencies...)
	}
	declares := func(artifact string) bool {
		return slices.ContainsFunc(dependencies, func(dependency maven.Dependency) bool {
			return dependency.Group == "io.cucumber" && dependency.Artifact == artifact
		})
	}
	return declares("cucumber-java8") && !declares("cucumber-java")
}

var (
	javaPackage = regexp.MustCompile(`(?m)^\s*package\s+([\w.]+)\s*;`)
	// A JUnit 4 runner, a JUnit Platform suite of the Cucumber engine, or the
	// engine's own @Cucumber class.
	// Annotations may be fully qualified, as in @org.junit.runner.RunWith.
	javaCucumberRunner = regexp.MustCompile(`@(?:[\w.]+\.)?RunWith\(\s*(?:[\w.]+\.)?Cucumber\.class\s*\)|@(?:[\w.]+\.)?IncludeEngines\(\s*(?:\{\s*(?:"[^"]*"\s*,\s*)*)?"cucumber"|@(?:io\.cucumber\.junit\.platform\.engine\.)?Cucumber\b`)
	javaCucumberSuite  = regexp.MustCompile(`@(?:[\w.]+\.)?IncludeEngines\(\s*(?:\{\s*(?:"[^"]*"\s*,\s*)*)?"cucumber"`)
	// glue = "x" or glue = {"x", ...} in @CucumberOptions, or the suite's
	// glue configuration parameter.
	javaCucumberGlue = regexp.MustCompile(`\bglue\s*=\s*\{?\s*"([^"]+)"|key\s*=\s*(?:(?:[\w.]+\.)?GLUE_PROPERTY_NAME|"cucumber\.glue")\s*,\s*value\s*=\s*"([^"]+)"|value\s*=\s*"([^"]+)"\s*,\s*key\s*=\s*(?:(?:[\w.]+\.)?GLUE_PROPERTY_NAME|"cucumber\.glue")`)
	// Step definitions of the lambda backend, and of the annotation one.
	javaCucumberLambdaGlue     = regexp.MustCompile(`\bio\.cucumber\.java8\.`)
	javaCucumberAnnotationGlue = regexp.MustCompile(`\bio\.cucumber\.java\.(en|[a-z_]+)\b|\bio\.cucumber\.java\.[A-Z]`)
)

// javaCucumberProbe places the probe in module: the feature in the test
// resources, and its step in the glue package of the module's Cucumber runner,
// so the project's own glue, hooks and object factory still load. The runner
// alone is selected, with the probe feature in place of the project's.
func javaCucumberProbe(checkout, module string, args []string) (javaProbe, string, error) {
	runner, glue, suite, lambdaGlue, err := javaCucumberRunnerClass(filepath.Join(checkout, module))
	if err != nil {
		return javaProbe{}, "", err
	}
	if runner == "" {
		return javaProbe{}, "No Cucumber runner class (@RunWith(Cucumber.class) or a JUnit Platform suite of the cucumber engine) was found in " + module + "'s test sources, so the probe feature cannot be selected alone.", nil
	}
	// The selected command's own glue property wins over the runner's.
	for i := 0; i < len(args); i++ {
		property := ""
		switch arg := args[i]; {
		case (arg == "-D" || arg == "--define") && i+1 < len(args):
			property = args[i+1]
			i++
		case strings.HasPrefix(arg, "--define="):
			property = strings.TrimPrefix(arg, "--define=")
		case strings.HasPrefix(arg, "-D"):
			property = strings.TrimPrefix(arg, "-D")
		}
		if value, ok := strings.CutPrefix(property, "cucumber.glue="); ok {
			glue = javaGluePackage(value)
		}
	}
	selection := []string{"-Dtest=" + runner, "-Dcucumber.features=classpath:ddtest/DdtestValidation.feature", "-Dsurefire.failIfNoSpecifiedTests=false", "-DfailIfNoTests=false", "-DskipITs"}
	if suite {
		// With cucumber.features set, the root Cucumber engine discovers the
		// probe too; the suite's own engine is the one that runs it.
		selection = append(selection, "-Dsurefire.excludeJUnit5Engines=cucumber")
	}
	return javaProbe{
		files: []javaProbeFile{
			{filepath.Join(module, "src", "test", "resources", "ddtest", "DdtestValidation.feature"), javaCucumberFeature},
			{filepath.Join(module, "src", "test", "java", filepath.FromSlash(strings.ReplaceAll(glue, ".", "/")), "DdtestValidationSteps.java"), javaCucumberSteps(glue, javaCucumberLambda(checkout, module, lambdaGlue))},
		},
		args: selection,
	}, "", nil
}

// javaCucumberRunnerClass finds the first Cucumber runner class in a module's
// test sources, by path, and its glue package: the runner's glue option or
// configuration parameter, the module's junit-platform.properties, or the
// runner's own package. suite reports a JUnit Platform suite.
func javaCucumberRunnerClass(module string) (runner, glue string, suite bool, lambda backendUse, err error) {
	sources := filepath.Join(module, "src", "test", "java")
	err = filepath.WalkDir(sources, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".java") || !entry.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		lambda.lambda = lambda.lambda || javaCucumberLambdaGlue.MatchString(text)
		lambda.annotations = lambda.annotations || javaCucumberAnnotationGlue.MatchString(text)
		if runner != "" || !javaCucumberRunner.MatchString(text) {
			return nil
		}
		pkg := ""
		if match := javaPackage.FindStringSubmatch(text); match != nil {
			pkg = match[1]
		}
		runner = strings.TrimPrefix(pkg+"."+strings.TrimSuffix(entry.Name(), ".java"), ".")
		suite = javaCucumberSuite.MatchString(text)
		if match := javaCucumberGlue.FindStringSubmatch(text); match != nil {
			glue = cmp.Or(match[1], match[2], match[3])
		}
		glue = cmp.Or(glue, javaPlatformGlue(module), pkg)
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return "", "", false, backendUse{}, nil
	}
	return runner, javaGluePackage(glue), suite, lambda, err
}

// javaGluePackage is the first package of a glue list; a path such as
// classpath:com/example names the package com.example.
func javaGluePackage(glue string) string {
	return strings.ReplaceAll(strings.Trim(strings.TrimPrefix(strings.TrimSpace(strings.Split(glue, ",")[0]), "classpath:"), "/"), "/", ".")
}

// backendUse records which Cucumber glue backends a module's test sources use.
type backendUse struct{ lambda, annotations bool }

// javaPlatformGlue is the cucumber.glue of a module's junit-platform.properties.
func javaPlatformGlue(module string) string {
	file, err := os.Open(filepath.Join(module, "src", "test", "resources", "junit-platform.properties"))
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if key, value, ok := strings.Cut(scanner.Text(), "="); ok && strings.TrimSpace(key) == "cucumber.glue" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
