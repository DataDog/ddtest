// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// javaKarateFeature is the probe feature. Its scenario records each execution
// and fails as DDTEST_PROBE_MODE asks, as javaProbeSource does, in Karate's
// JavaScript, so no step class is needed. The tag selects it past the tags a
// project's runner filters by.
const javaKarateFeature = `Feature: ` + javaProbeClass + `

  @ddtest
  Scenario: ddtestProbe
    * eval
      """
      var System = Java.type('java.lang.System');
      var Files = Java.type('java.nio.file.Files');
      var Paths = Java.type('java.nio.file.Paths');
      var FileWriter = Java.type('java.io.FileWriter');
      var path = System.getenv('DDTEST_PROBE_STATE');
      var writer = new FileWriter(path, true);
      writer.write('run\n');
      writer.close();
      var attempts = Files.readAllLines(Paths.get(path)).size();
      var mode = System.getenv('DDTEST_PROBE_MODE');
      if (mode == 'fail' || (mode == 'fail-once' && attempts == 1)) karate.fail('ddtest intentional failure');
      """
`

// javaKarateProbeFeature is where the probe feature goes, relative to a module.
var javaKarateProbeFeature = filepath.Join("src", "test", "resources", "ddtest", "DdtestValidation.feature")

// javaKarateRunner is a test class that runs features: a JUnit 5 @Karate.Test
// or a JUnit 4 runner. A class that calls Karate's parallel Runner is one when
// a test calls it. Importing Karate's types does not make a class one.
var javaKarateRunner = regexp.MustCompile(`@(?:[\w.]+\.)?Karate\.Test\b|@(?:[\w.]+\.)?RunWith\(\s*(?:[\w.]+\.)?Karate\.class\s*\)`)

// javaKarateParallel is a call of Karate's parallel Runner.
var javaKarateParallel = regexp.MustCompile(`\bRunner\.(?:path|builder)\(`)

// javaKarateMethod is a JUnit 5 @Karate.Test or JUnit @Test method, whose
// name is the first group.
var javaKarateMethod = regexp.MustCompile(`@(?:[\w.]+\.)?(?:Karate\.)?Test\b(?:\([^)]*\))?[^{;=]*?\b([\pL_$][\pL\pN_$]*)\s*\(\s*\)`)

// javaKarateProbe places the probe feature in module and selects it through
// the module's Karate runner: karate.options replaces the runner's features
// and tags, and keeps its environment and configuration. A runner that runs
// features in parallel from a JUnit test reports that test too; it is the
// probe's harness. Each method of a runner runs the probe, so only one is
// selected.
func javaKarateProbe(checkout, module string) (javaProbe, string, error) {
	runner, method, err := javaKarateRunnerClass(filepath.Join(checkout, module))
	if err != nil {
		return javaProbe{}, "", err
	}
	if runner == "" {
		return javaProbe{}, "No Karate runner class (@Karate.Test, @RunWith(Karate.class) or Runner.path) was found in the test sources of " + javaModuleText(module) + ", so the probe feature cannot be selected alone.", nil
	}
	selection := runner
	if method != "" {
		selection += "#" + method
	}
	feature := filepath.Join(module, javaKarateProbeFeature)
	// The feature is selected by its file: test resources a POM redefines
	// may leave it off the classpath.
	return javaProbe{
		files:   []javaProbeFile{{feature, javaKarateFeature}},
		harness: runner,
		args:    []string{"-Dtest=" + selection, "-Dkarate.options=--tags @ddtest file:" + filepath.ToSlash(filepath.Join(checkout, feature)), "-Dsurefire.failIfNoSpecifiedTests=false", "-DfailIfNoTests=false", "-DskipITs"},
	}, "", nil
}

// javaKarateRunnerClass finds the first Karate runner class in a module's test
// sources, by path, and the test method that runs features: its first
// @Karate.Test, else the test that calls the parallel Runner. A JUnit 4
// runner has none.
func javaKarateRunnerClass(module string) (runner, method string, err error) {
	err = filepath.WalkDir(filepath.Join(module, "src", "test", "java"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if runner != "" || entry.IsDir() || !strings.HasSuffix(path, ".java") || !entry.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		method = javaKarateRunnerMethod(text)
		// A helper that calls the Runner outside any test cannot be selected.
		if !javaKarateRunner.MatchString(text) && method == "" {
			return nil
		}
		pkg := ""
		if match := javaPackage.FindStringSubmatch(text); match != nil {
			pkg = match[1]
		}
		runner = strings.TrimPrefix(pkg+"."+strings.TrimSuffix(entry.Name(), ".java"), ".")
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return "", "", nil
	}
	return runner, method, err
}

// javaKarateRunnerMethod is the test method of a runner's source that runs
// features, as javaKarateRunnerClass describes.
func javaKarateRunnerMethod(text string) string {
	matches := javaKarateMethod.FindAllStringSubmatchIndex(text, -1)
	for _, match := range matches {
		if strings.Contains(text[match[0]:match[1]], "Karate.Test") {
			return text[match[2]:match[3]]
		}
	}
	// The test that calls the Runner is the last one declared before the call.
	call := javaKarateParallel.FindStringIndex(text)
	method := ""
	for _, match := range matches {
		if call != nil && match[0] < call[0] {
			method = text[match[2]:match[3]]
		}
	}
	return method
}
