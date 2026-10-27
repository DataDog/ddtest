// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"regexp"
	"strings"

	"github.com/DataDog/ddtest/internal/maven"
)

var mavenFailedGoal = regexp.MustCompile(`^\[ERROR\] Failed to execute goal ([^ ]+) \([^)]*\) on project ([^:]+): (.*)$`)

// Fresh reports can cover only an early part of the reactor. A failed command
// is usable only when every Maven goal failure is a reported native test failure.
func incompleteMavenBuild(exitCode int, projects []maven.Project, tests []maven.Test, output string) string {
	const incomplete = "Maven build is incomplete: failure is not explained solely by native test failures in declared modules. Inspect Maven output for compilation, setup or reactor failures."
	if exitCode != 0 && exitCode != 1 {
		return incomplete
	}
	failedModules := map[string]bool{}
	for _, test := range tests {
		if test.Status == "failed" {
			failedModules[test.Module] = true
		}
	}
	goals := 0
	for _, line := range strings.Split(ansiEscape.ReplaceAllString(output, ""), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "[ERROR] Failed to execute goal") {
			continue
		}
		match := mavenFailedGoal.FindStringSubmatch(line)
		if match == nil {
			return incomplete
		}
		parts := strings.Split(match[1], ":")
		if len(parts) != 4 || !strings.HasPrefix(match[3], "There are test failures") {
			return incomplete
		}
		switch parts[0] + ":" + parts[1] + ":" + parts[3] {
		case "org.apache.maven.plugins:maven-surefire-plugin:test", "org.apache.maven.plugins:maven-failsafe-plugin:verify", "org.scalatest:scalatest-maven-plugin:test", "org.eclipse.tycho:tycho-surefire-plugin:test":
		default:
			return incomplete
		}
		// Maven goal errors identify artifactId, not the tracer's display name.
		matches := 0
		for _, project := range projects {
			if project.Artifact == match[2] {
				matches++
				if !failedModules[project.Directory] {
					return incomplete
				}
			}
		}
		if matches != 1 {
			return incomplete
		}
		goals++
	}
	if exitCode != 0 && goals == 0 {
		return incomplete
	}
	return ""
}
