// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
)

type tracerRecommendation struct {
	Selection   platform.JSSelection `json:"selection"`
	Compatible  []string             `json:"compatible_ci_nodes,omitempty"`
	Unsupported []string             `json:"unsupported_ci_nodes,omitempty"`
	Unresolved  []string             `json:"unresolved_ci_nodes,omitempty"`
}

// Prefer coverage of known CI runtimes, then the newest verified major. Never
// install a candidate or change the selected/project tracer based on this advice.
func recommendJavaScriptTracer(ctx context.Context, check jestPreflight, ci *onboard.RuntimeCheck, resolve func(context.Context, string) (platform.JSSelection, error), framework string) (*tracerRecommendation, []string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var best *tracerRecommendation
	var notes []string
	for _, major := range []string{"6", "5"} { // Resolve current releases from the supported tracer lines; no release pins.
		selected, err := resolve(ctx, major)
		if err != nil {
			notes = append(notes, fmt.Sprintf("Could not resolve dd-trace %s: %s", major, reportText(err.Error())))
			continue
		}
		support := checkJestSupport
		if framework == "vitest" {
			support = checkVitestSupport
		}
		if verdict := support(check, selected); verdict.Status != "compatible" {
			continue
		}
		candidate := &tracerRecommendation{Selection: selected}
		if ci != nil {
			for _, job := range ci.Jobs {
				node := job.Node
				if job.NodeResolution != nil {
					node = job.NodeResolution.Version
				}
				label := nodeScopeLabel(job.Node)
				status, _ := onboard.CompareNodeRequirement(node, selected.Node)
				switch status {
				case "compatible":
					candidate.Compatible = appendUnique(candidate.Compatible, label)
				case "incompatible":
					candidate.Unsupported = appendUnique(candidate.Unsupported, label)
				default:
					candidate.Unresolved = appendUnique(candidate.Unresolved, label)
				}
			}
			// No candidate is useful for an entirely incompatible known CI matrix.
			if len(candidate.Compatible) == 0 && len(candidate.Unsupported) > 0 {
				continue
			}
		}
		if best == nil || len(candidate.Compatible) > len(best.Compatible) {
			best = candidate
		}
	}
	if best == nil {
		notes = append(notes, fmt.Sprintf("No verified tracer candidate satisfies the detected %s configuration, local Node and any known CI runtime. Review prerequisites; do not drop test coverage or guess older majors.", framework))
	}
	return best, notes
}

func appendUnique(values []string, value string) []string {
	if !slices.Contains(values, value) {
		return append(values, value)
	}
	return values
}

func scopeList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	if len(values) > 8 {
		return strings.Join(values[:8], ", ") + fmt.Sprintf(" and %d more (see ci_runtime.jobs)", len(values)-8)
	}
	return strings.Join(values, ", ")
}

func nodeScopeLabel(node string) string {
	if node == "" {
		return "unknown"
	}
	value := []rune(node)
	if len(value) > 80 {
		return string(value[:80]) + "…"
	}
	return node
}

// Recommend before excluding older CI runtimes, including when the remaining
// instrumented jobs already pass static validation. Never change dependencies.
func (t *Testdrive) recommendTracer(ctx context.Context, output io.Writer, result *validationResult) {
	check, selection := result.Preflight, result.Selection
	if t.resolveJSTracer == nil || strings.HasPrefix(selection.Requested, "git:") {
		return
	}
	needed := check.Verdict.Status != "compatible"
	if ci := result.CIRuntime; ci != nil {
		needed = needed || (ci.Status != "compatible" && ci.Status != "not applicable")
		for _, job := range ci.Jobs {
			needed = needed || job.Code == "excluded_runtime"
		}
	}
	if !needed {
		return
	}
	check.Recommendation, check.RecommendationNotes = recommendJavaScriptTracer(ctx, *check, result.CIRuntime, t.resolveJSTracer, t.framework.Name())
	if rec := check.Recommendation; rec != nil {
		_, _ = fmt.Fprintf(output, "Recommended candidate: dd-trace@%s (Node %s). CI Node compatible: %s; unsupported: %s; unresolved: %s.\nPrefer a candidate covering the whole existing matrix before excluding runtimes. Use --tracer-version %s and js-tracer-version: %s, then rerun --check-only before full validation. This is advisory; the current selection and project dependency are unchanged.\n", rec.Selection.Version, rec.Selection.Node, scopeList(rec.Compatible), scopeList(rec.Unsupported), scopeList(rec.Unresolved), rec.Selection.Version, rec.Selection.Version)
	}
	for _, note := range check.RecommendationNotes {
		_, _ = fmt.Fprintln(output, "Tracer recommendation: "+note)
	}
}
