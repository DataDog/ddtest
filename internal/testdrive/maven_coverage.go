package testdrive

import (
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/environment"
	"github.com/DataDog/ddtest/internal/maven"
)

type mavenModuleCoverage struct {
	Directory     string `json:"directory"`
	Artifact      string `json:"artifact"`
	Status        string `json:"status"`
	ReactorStatus string `json:"reactor_status,omitempty"`
	NativeResults int    `json:"native_results"`
}
type mavenWorkflowCoverage struct {
	Path            string `json:"path"`
	Instrumentation string `json:"instrumentation"`
	Execution       string `json:"execution"`
}
type mavenCoverage struct {
	WorkflowDiscovery string                  `json:"workflow_discovery"`
	Scope             string                  `json:"scope"`
	Modules           []mavenModuleCoverage   `json:"modules,omitempty"`
	Workflows         []mavenWorkflowCoverage `json:"ci_workflows"`
	Reason            string                  `json:"reason"`
}

func discoverMavenCoverage(root string) *mavenCoverage {
	workflows, configured, err := environment.NewGitHub().FindTestWorkflows(root, "java", "")
	coverage := &mavenCoverage{WorkflowDiscovery: "observed (static only)", Scope: "selected Maven command", Workflows: []mavenWorkflowCoverage{}, Reason: "Declared modules include profile candidates. Native results establish execution only for the selected command. Static action presence does not certify CI Java/tracer versions, other commands or remote execution."}
	if err != nil {
		coverage.WorkflowDiscovery = "inconclusive"
		coverage.Reason = "CI workflow discovery is unresolved: " + reportText(err.Error()) + ". Local Maven validation is scoped to the selected command; CI coverage is not certified."
		return coverage
	}
	for _, path := range workflows {
		status := "missing"
		if slices.Contains(configured, path) {
			status = "present (static only)"
		}
		coverage.Workflows = append(coverage.Workflows, mavenWorkflowCoverage{Path: path, Instrumentation: status, Execution: "not exercised"})
	}
	return coverage
}

var mavenReactorLine = regexp.MustCompile(`(?m)^\[INFO\] (.*?) \.{2,} (SUCCESS|FAILURE|SKIPPED)(?:\s|$)`)

func observeMavenModules(projects []maven.Project, tests []maven.Test, output string) []mavenModuleCoverage {
	counts, reactor := map[string]int{}, map[string]string{}
	for _, test := range tests {
		counts[test.Module]++
	}
	names := mavenModuleDirectories(projects)
	for _, match := range mavenReactorLine.FindAllStringSubmatch(output, -1) {
		if directory, ok := names[strings.TrimSpace(match[1])]; ok && directory != "ambiguous" {
			reactor[directory] = strings.ToLower(match[2])
		}
	}
	var modules []mavenModuleCoverage
	for _, project := range projects {
		module := mavenModuleCoverage{Directory: project.Directory, Artifact: project.Artifact, NativeResults: counts[project.Directory], ReactorStatus: reactor[project.Directory], Status: "not observed"}
		switch {
		case module.NativeResults > 0:
			module.Status = "observed"
		case module.ReactorStatus == "skipped":
			module.Status = "not reached"
		case project.Packaging == "pom":
			module.Status = "aggregator (no native results)"
		}
		modules = append(modules, module)
	}
	return modules
}

func writeMavenCoverage(output io.Writer, coverage *mavenCoverage) {
	if coverage == nil {
		return
	}
	for _, module := range coverage.Modules[:min(len(coverage.Modules), maxReportDifferences)] {
		_, _ = fmt.Fprintf(output, "Maven module %s: %s; %d native results; reactor=%s.\n", module.Directory, module.Status, module.NativeResults, module.ReactorStatus)
	}
	for _, workflow := range coverage.Workflows[:min(len(coverage.Workflows), maxReportDifferences)] {
		_, _ = fmt.Fprintf(output, "CI workflow %s: instrumentation %s; execution %s.\n", workflow.Path, workflow.Instrumentation, workflow.Execution)
	}
	if len(coverage.Modules) > maxReportDifferences || len(coverage.Workflows) > maxReportDifferences {
		_, _ = fmt.Fprintln(output, "Additional module/workflow coverage is in maven_coverage in Results JSON.")
	}
	_, _ = fmt.Fprintln(output, coverage.Reason)
}

func mavenFeatureBlockReason(result validationResult) string {
	if result.NativeComparison != nil && result.NativeComparison.Status != "compatible" {
		return "Feature probes were not run: native baseline/reporting outcomes have not established compatibility (" + result.NativeComparison.Code + ")."
	}
	if result.TelemetryComparison != nil && result.TelemetryComparison.Status != "compatible" {
		return "Feature probes were not run: telemetry identity reconciliation is unresolved (" + result.TelemetryComparison.Code + "). Matching existing failures alone do not block compatibility."
	}
	return ""
}

func writeMavenComparisons(output io.Writer, result validationResult) {
	for _, entry := range []struct {
		label string
		check *verdict
	}{{"Native outcome parity", result.NativeComparison}, {"Telemetry identity reconciliation", result.TelemetryComparison}} {
		if entry.check == nil {
			continue
		}
		_, _ = fmt.Fprintf(output, "%s: %s — %s\n", entry.label, entry.check.Status, entry.check.Reason)
	}
}
