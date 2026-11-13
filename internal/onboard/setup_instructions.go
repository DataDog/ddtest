// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"slices"
	"strings"
)

// providerSetupInstructions are the setup instructions on a CI provider
// without the shared workflow syntax: the provider's setup step with the
// language's values and rules, then local validation. agent selects the
// coding agent's handoff to coordinated validation; site empty is the
// default site.
func providerSetupInstructions(root string, provider ciProvider, language, framework, site string, agent bool) string {
	registered := discoveryLanguageNamed(language)
	if registered == nil {
		return ""
	}
	selected := site
	if site == "" {
		site = defaultDatadogSite
	}
	var text strings.Builder
	var keyAction string
	// request is the first setup mechanism's, whose tracer release local
	// validation uses when it is not coordinated.
	var request ciSetupRequest
	lines, handWritten := handWrittenSetups(root, language, framework, site)
	if handWritten {
		// Every job already sets up the tracer by hand: those setups stay,
		// and only what they lack is prescribed.
		_, _ = fmt.Fprintf(&text, "\n# Keep the hand-written Datadog Test Optimization setup for %s on %s\n\n", framework, provider.displayName())
		text.WriteString("Every CI job that runs these tests already sets up Test Optimization by hand. Keep those setups: do not add the " + provider.setupName() + " and do not change how they report. Make only the changes listed for each job, in its own setup, then validate:\n\n- " + strings.Join(lines, "\n- "))
		keyAction = CIProviderHandWrittenKeyAction()
	} else {
		_, _ = fmt.Fprintf(&text, "\n# Enable Datadog Test Optimization for %s on %s\n\n", framework, provider.displayName())
		automatic := providerAutomaticSetup(root, provider, registered, framework, selected)
		if automatic {
			text.WriteString(strings.ReplaceAll(javascriptAutomaticSetup, "ddtest testdrive", "ddtest testdrive --framework "+framework) + "\n\n")
		} else if pluginSetupWritable(root, language, framework, selected) {
			text.WriteString("`ddtest onboard --apply` previews the edit that enables the " + provider.setupName() + " in the CI configuration, writing nothing, and `ddtest onboard --apply --yes` writes it; neither runs tests. Make the rest of the setup below yourself.\n\n")
		}
		groups := testJobSetups(root, provider, language, framework)
		for i, group := range groups {
			terms := group.provider.setupTerms()
			for _, other := range groups {
				if other.provider.setupName() != group.provider.setupName() && terms.Beside == "" {
					terms.Beside = "the " + other.provider.setupName() + " of " + strings.Join(other.jobs, ", ")
				}
			}
			current := registered.ciSetupRequest(root, framework, site, terms)
			current.Steps = group.steps
			if len(groups) > 1 || group.provider != provider {
				// The provider sets its jobs up with several mechanisms.
				current.Framework, current.Jobs = framework, group.jobs
			}
			if current.Shared = sharedJobsFor(root, provider, language, framework, group.jobs); len(current.Shared) > 0 {
				current.Framework = framework
			}
			if i > 0 {
				text.WriteString("\n\n")
			} else {
				request = current
			}
			text.WriteString(group.provider.setupInstructions(current))
		}
		keyAction = groupsAPIKeyAction(provider, groups)
	}
	if site == defaultDatadogSite && !agent && !handWritten {
		text.WriteString("\n\nIf the organization uses a Datadog site other than US1, replace " + defaultDatadogSite + " with that site.")
	}
	if agent && !registered.coordinated() {
		text.WriteString("\n\n" + standaloneAgentValidation(framework))
		return text.String()
	}
	if agent {
		text.WriteString("\n\nDo not run testdrive, a configuration check or the tests yourself. Run resume_command once, after the edits above: it validates every discovered configuration and feature in disposable copies and returns the next action. When it returns setup_commands, run them as given, then its resume_command. Finish with its exact next_command and render final_response unchanged; only that coordinator result can certify `COMPLETE LOCALLY`.")
		return text.String()
	}
	check := fmt.Sprintf("Run the local, credential-free setup check:\n\n```shell\nddtest testdrive --framework %s --agent --all --yes%s\n```", framework, ciProviderFollowUpFlag("."))
	if !registered.coordinated() {
		// Without coordinated validation, the configuration check comes
		// first, then validation with the release the setup installs.
		tracer := ""
		if request.TracerInput != "" {
			tracer = " --tracer-version " + request.TracerVersion
		}
		check = fmt.Sprintf("Run the local, credential-free setup check: first the CI configuration alone, then, after the edits, the local validation with the release the setup installs:\n\n```shell\nddtest testdrive --framework %s --all --check-only --yes%s\nddtest testdrive --framework %s --all --yes%s%s\n```", framework, ciProviderFollowUpFlag("."), framework, tracer, ciProviderFollowUpFlag("."))
	}
	_, _ = fmt.Fprintf(&text, "\n\n## Try it locally\n\n%s\n\nAfter it finishes, post every `Open report:` link to the user so they can open the local Test Optimization report.\n\n## Ask a human to connect Datadog\n\n%s Do not ask for the API key in chat, and do not try to create or read the variable yourself. After the human confirms it is ready, commit and push the CI change; the next pipeline verifies the real Datadog backend connection.", check, keyAction)
	return text.String()
}

// groupsAPIKeyAction is the person's step that stores the API key for each
// setup mechanism groups use.
func groupsAPIKeyAction(provider ciProvider, groups []jobSetups) string {
	several, ok := provider.(ciAPIKeyActions)
	if !ok {
		// Each mechanism says what happens without the key, such as the
		// setup without Bash or curl that skips beside an orb that fails.
		var actions []string
		for _, group := range groups {
			if action := group.provider.apiKeyAction(false); !slices.Contains(actions, action) {
				actions = append(actions, action)
			}
		}
		if len(actions) == 0 {
			return provider.apiKeyAction(false)
		}
		return strings.Join(actions, " ")
	}
	var references []string
	for _, group := range groups {
		references = append(references, group.provider.setupName())
	}
	return several.apiKeyActions(references, false)
}

// standaloneAgentValidation ends a coding agent's setup instructions for a
// language whose validation is standalone (Python): resume_command validates
// every discovered configuration once the edits are in place.
func standaloneAgentValidation(framework string) string {
	return "Do not run testdrive, a configuration check or the tests yourself. Run resume_command once, after the edits above: it validates every discovered configuration and feature in disposable copies, with the tracer release the CI setup installs, and returns one structured result. When it asks the user (next_action ask_user), stop and ask them; never answer for them. Finish by rendering its final_response unchanged. " + StandaloneVerdictInstruction(framework)
}
