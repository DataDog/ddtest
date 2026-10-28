package testdrive

import (
	"fmt"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
)

// ciFidelity separates gaps between CI and this local run. Validation runs as
// close to CI as it can, but some CI values can't be replayed locally. A gap
// whose remaining risk the run already covered is a caveat: the report names
// it, and it never blocks completion. Any other gap stays a blocker.
//
// A runtime-computed matrix axis is a caveat only when discovery found that a
// dependency install alone reads it (so the CI edit can't depend on its
// value), and every validated configuration of its job passed with a
// framework version the tracer supports. An accepted substitute runtime is a
// caveat too; an unaccepted one stays a blocker elsewhere. So is a
// configuration that runs only on a remote browser grid with CI secrets, once
// a local configuration of the same suite passed its paired comparison.
func ciFidelity(result *validationResult) (caveats, blockers []string) {
	for _, sub := range result.substitutions() {
		if sub.Accepted {
			caveats = append(caveats, sub.caveat())
		}
	}
	for _, configuration := range result.Configurations {
		if configuration.RemoteGrid == "" {
			continue
		}
		if remoteGridCovered(*result) {
			caveats = append(caveats, fmt.Sprintf("%s runs on a remote browser grid (%s) and can't run locally; CI runs it with the same Datadog setup, and the local configurations of the same suite were validated.", configuration.Command, configuration.RemoteGrid))
			continue
		}
		blockers = append(blockers, fmt.Sprintf("%s runs only on a remote browser grid (%s), and no local configuration of the same suite was validated", configuration.Command, configuration.RemoteGrid))
	}
	if result.Scope == nil {
		return caveats, blockers
	}
	for _, axis := range result.Scope.ComputedAxes {
		configurations := jobConfigurations(result, axis)
		version, passed := "", len(configurations) > 0
		for _, configuration := range configurations {
			preflight := configuration.Preflight
			if preflight == nil {
				preflight = result.Preflight
			}
			passed = passed && configuration.Status == "passed" && preflight != nil && preflight.Verdict.Status == "compatible"
			if preflight != nil && version == "" {
				version = preflight.Version
			}
		}
		if passed {
			caveats = append(caveats, fmt.Sprintf("%s: matrix axis %s is computed at runtime; tests were validated with the installed %s %s, and other %s values were not run locally (CI runs them).", axis.Location(), axis.Axis, displayName(result.Framework), version, axis.Axis))
			continue
		}
		blockers = append(blockers, axis.Location()+": matrix axis "+axis.Axis+" is computed at runtime, and its tests did not pass validation with the installed dependencies, so other "+axis.Axis+" values remain unverified")
	}
	return caveats, blockers
}

// jobConfigurations returns the instrumented configurations discovered in the
// axis's workflow job.
func jobConfigurations(result *validationResult, axis onboard.ComputedAxis) []configurationResult {
	prefix := axis.Location()
	var matched []configurationResult
	for _, configuration := range result.Configurations {
		if configurationExcluded(configuration) {
			continue
		}
		for _, location := range configuration.Locations {
			if location == prefix || strings.HasPrefix(location, prefix+" (") || strings.HasPrefix(location, prefix+" / ") {
				matched = append(matched, configuration)
				break
			}
		}
	}
	return matched
}
