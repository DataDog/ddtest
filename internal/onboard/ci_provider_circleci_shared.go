// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"maps"
	"slices"
	"strings"
)

// A CircleCI job is a template: a workflow may run one job several times,
// with other parameter values, as a parameterized gradle_execute_task job
// whose gradle_task parameter is test -Pe2e in one invocation and pitest in
// another. A setup step added to such a job runs in every invocation, so it
// would instrument the commands of the invocations that run no tests too:
// JAVA_TOOL_OPTIONS, for one, loads dd-java-agent in every JVM, PIT's child
// JVMs included. ddtest prescribes a boolean job parameter
// (circleSetupParameter, false by default) that enables the setup only on
// the invocations that run the tests, and reports a setup that reaches the
// others.

// ciSharedJob is a job definition whose workflow invocations run the tests
// in some and only other commands in others.
type ciSharedJob struct {
	// Job names the job definition in the configuration's jobs.
	Job string
	// Tests and Others are the invocations that run the tests and those
	// that do not, as <workflow>/<name>.
	Tests, Others []string
}

// jobRunsTests reports a job with a step that runs or may run framework's
// tests, as the CI checks read it.
func jobRunsTests(root string, workflow ciWorkflow, job runtimeJob, language, framework string) bool {
	return slices.ContainsFunc(job.Steps, func(step runtimeStep) bool {
		resolution := resolveTestStep(root, workflow, job, step, language, framework)
		return !resolution.Review && (resolution.Matched || resolution.Reason != "" && stepMayStartTests(root, workflow, job, step, framework))
	})
}

// circleItem is the configuration entry a CircleCI step was read from: one
// entry for every invocation of the job it is written in.
func circleItem(step runtimeStep) (circleStep, bool) {
	origin, ok := step.origin.(circleStep)
	return origin, ok && origin.item != nil
}

// circleSharedJobs lists the CircleCI jobs a workflow runs both for
// framework's tests and for invocations that run no tests, where the test
// step is written in the job's own steps.
func circleSharedJobs(root string, workflows []ciWorkflow, language, framework string) []ciSharedJob {
	var shared []ciSharedJob
	for _, workflow := range workflows {
		names := slices.Sorted(maps.Keys(workflow.Jobs))
		runs := map[string]bool{}
		for _, name := range names {
			runs[name] = !disabledCondition(workflow.Jobs[name].If) && jobRunsTests(root, workflow, workflow.Jobs[name], language, framework)
		}
		index := map[string]int{}
		for _, name := range names {
			job := workflow.Jobs[name]
			if !runs[name] {
				continue
			}
			for _, step := range job.Steps {
				origin, ok := circleItem(step)
				if !ok || origin.owner.kind != "job" || !origin.owner.editable || !resolveTestStep(root, workflow, job, step, language, framework).Matched {
					continue
				}
				for _, other := range names {
					if runs[other] || disabledCondition(workflow.Jobs[other].If) || !slices.ContainsFunc(workflow.Jobs[other].Steps, func(candidate runtimeStep) bool {
						current, ok := circleItem(candidate)
						return ok && current.item == origin.item
					}) {
						continue
					}
					i, seen := index[origin.owner.name]
					if !seen {
						i = len(shared)
						index[origin.owner.name] = i
						shared = append(shared, ciSharedJob{Job: origin.owner.name})
					}
					if !slices.Contains(shared[i].Tests, name) {
						shared[i].Tests = append(shared[i].Tests, name)
					}
					if !slices.Contains(shared[i].Others, other) {
						shared[i].Others = append(shared[i].Others, other)
					}
				}
			}
		}
	}
	for i := range shared {
		slices.Sort(shared[i].Tests)
		slices.Sort(shared[i].Others)
	}
	return shared
}

// sharedJobsFor are the shared jobs (see circleSharedJobs) that run the tests
// of jobs, the "<path> / <job>" keys of one setup mechanism's jobs; nil on
// other providers.
func sharedJobsFor(root string, provider ciProvider, language, framework string, jobs []string) []ciSharedJob {
	if provider.name() != circleCIProvider {
		return nil
	}
	workflows, err := readProviderWorkflows(provider, root)
	if err != nil {
		return nil
	}
	return slices.DeleteFunc(circleSharedJobs(root, workflows, language, framework), func(shared ciSharedJob) bool {
		return !slices.ContainsFunc(shared.Tests, func(name string) bool { return slices.Contains(jobs, circleCIFile+" / "+name) })
	})
}

// circleWorkflowEntry splits an invocation <workflow>/<name> into the
// workflow and the name its entry runs under.
func circleWorkflowEntry(invocation string) (workflow, name string) {
	workflow, name, _ = strings.Cut(invocation, "/")
	return workflow, name
}

// circleSharedSetupGuide is how the setup of a shared job runs only in the
// invocations that run the tests: fallback for the setup without Bash or
// curl, otherwise the Datadog orb step.
func circleSharedSetupGuide(shared ciSharedJob, fallback bool) string {
	setup := "wrap the Datadog orb step (and the step after it that appends to $BASH_ENV, if any) in a when step on << parameters." + circleSetupParameter + " >>"
	if fallback {
		setup = "wrap the " + fallbackSetupName + " inside its run step in " + circleSetupGuard + " … fi, keeping the test command after fi"
	}
	return "Enable the setup only for the invocations that run the tests: declare a boolean parameter " + circleSetupParameter + " (default: false) in the job " + shared.Job + ", " + setup + ", and set " + circleSetupParameter + ": true only on the workflow entries of " + strings.Join(shared.Tests, ", ") + "."
}

// circleSharedSetupInstructions is the instructions' step for the shared
// jobs of request (see ciSetupRequest.Shared): number is its number, and
// setup the setup's lines as the job's steps list them (the orb step), or
// as the run step's command starts (fallback).
func circleSharedSetupInstructions(request ciSetupRequest, number, setup string, fallback bool) string {
	if len(request.Shared) == 0 {
		return ""
	}
	var text strings.Builder
	for _, shared := range request.Shared {
		text.WriteString("\n" + number + ". The workflows also run the job " + shared.Job + ", whose steps include the test step of " + strings.Join(shared.Tests, ", ") + ", for invocations that run no " + request.Framework + " tests (" + strings.Join(shared.Others, ", ") + "), with other values of its parameters. A setup added to the job's steps runs in every invocation and would instrument those commands too. " + circleSharedSetupGuide(shared, fallback) + " Keep the job's other parameters, steps and workflow entries unchanged, and leave the parameter unset on the other invocations: ddtest reports a setup that reaches them.\n\n```yaml\njobs:\n  " + shared.Job + ":\n    parameters:\n      " + circleSetupParameter + ":\n        type: boolean\n        default: false\n    steps:\n")
		if fallback {
			text.WriteString("      - run:\n          name: <the step's name>\n          command: |\n            " + circleSetupGuard + "\n")
			for _, line := range strings.Split(setup, "\n") {
				text.WriteString("              " + line + "\n")
			}
			text.WriteString("            fi\n            <the test command>\n")
		} else {
			text.WriteString("      - when:\n          condition: << parameters." + circleSetupParameter + " >>\n          steps:\n" + setup)
		}
		text.WriteString("workflows:\n")
		entries := map[string][]string{}
		var order []string
		for _, invocation := range shared.Tests {
			workflow, name := circleWorkflowEntry(invocation)
			if _, seen := entries[workflow]; !seen {
				order = append(order, workflow)
			}
			entries[workflow] = append(entries[workflow], name)
		}
		for _, workflow := range order {
			text.WriteString("  " + workflow + ":\n    jobs:\n")
			for _, name := range entries[workflow] {
				text.WriteString("      - " + shared.Job + ":\n")
				if name != shared.Job {
					text.WriteString("          name: " + strconvQuoteYAML(name) + "\n")
				}
				text.WriteString("          " + circleSetupParameter + ": true\n")
			}
		}
		text.WriteString("```\n")
	}
	return text.String()
}

// sharedSetupLeaks reports, as incomplete setup, every CircleCI job that
// runs no framework tests but runs a Datadog setup for language written in
// a step a job running the tests shares with it, as a shared job's setup
// without the parameter that keeps it out (see circleSharedJobs). Other
// providers have none.
func sharedSetupLeaks(root string, workflows []ciWorkflow, language, framework string) []RuntimeFinding {
	token := setupActionLanguage(language)
	var findings []RuntimeFinding
	for _, workflow := range workflows {
		names := slices.Sorted(maps.Keys(workflow.Jobs))
		// Only a CircleCI setup step can be shared: other workflows cost
		// nothing more.
		if !slices.ContainsFunc(names, func(name string) bool {
			return slices.ContainsFunc(workflow.Jobs[name].Steps, func(step runtimeStep) bool {
				_, isSetup := datadogSetup(step)
				_, shared := circleItem(step)
				return isSetup && shared
			})
		}) {
			continue
		}
		runs := map[string]bool{}
		for _, name := range names {
			runs[name] = !disabledCondition(workflow.Jobs[name].If) && jobRunsTests(root, workflow, workflow.Jobs[name], language, framework)
		}
		for _, name := range names {
			job := workflow.Jobs[name]
			if runs[name] || disabledCondition(job.If) {
				continue
			}
			for i, step := range job.Steps {
				setup, isSetup := datadogSetup(step)
				origin, ok := circleItem(step)
				if !isSetup || !ok {
					continue
				}
				if selected, err := setupSelectsLanguage(setup, nil, token); err == nil && !selected {
					continue
				}
				var tests []string
				for _, other := range names {
					if runs[other] && slices.ContainsFunc(workflow.Jobs[other].Steps, func(candidate runtimeStep) bool {
						current, ok := circleItem(candidate)
						return ok && current.item == origin.item
					}) {
						tests = append(tests, other)
					}
				}
				if len(tests) == 0 {
					continue
				}
				shared := ciSharedJob{Job: origin.owner.name, Tests: tests, Others: []string{name}}
				where := circleOwnerDescription(origin.owner)
				guide := "Run the setup only in the jobs that run the tests."
				if origin.owner.kind == "job" && origin.owner.editable {
					guide = circleSharedSetupGuide(shared, setup.fallback)
				}
				setupName := "Datadog orb step"
				if setup.fallback {
					setupName = fallbackSetupName
				}
				findings = append(findings, RuntimeFinding{Location: (commandLocation{Workflow: workflow.Path, Job: name}).String(), Workflow: workflow.Path, Job: name, Step: stepNumber(step, i), Action: setup.Reference, Status: StatusInconclusive, Code: CodeIncompleteCISetup,
					Reason: "This CircleCI job runs no " + framework + " tests, but it runs the " + setupName + " written in " + where + ", which " + strings.Join(tests, ", ") + " also run before their tests: the setup instruments this job's commands too, which ddtest neither validates nor intends to instrument. " + guide})
				break
			}
		}
	}
	return findings
}

// sharedSetupLeak is the finding of leaks for job name of workflow.
func sharedSetupLeak(leaks []RuntimeFinding, workflow ciWorkflow, name string) (RuntimeFinding, bool) {
	i := slices.IndexFunc(leaks, func(leak RuntimeFinding) bool { return leak.Workflow == workflow.Path && leak.Job == name })
	if i < 0 {
		return RuntimeFinding{}, false
	}
	return leaks[i], true
}
