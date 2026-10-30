// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
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
	if site == "" {
		site = defaultDatadogSite
	}
	var text strings.Builder
	_, _ = fmt.Fprintf(&text, "\n# Enable Datadog Test Optimization for %s on %s\n\n", framework, provider.displayName())
	automatic := providerAutomaticSetup(provider, registered, framework)
	if automatic {
		text.WriteString(strings.ReplaceAll(javascriptAutomaticSetup, "ddtest testdrive", "ddtest testdrive --framework "+framework) + "\n\n")
	}
	text.WriteString(provider.setupInstructions(registered.ciSetupRequest(root, framework, site, provider.setupTerms())))
	if site == defaultDatadogSite && !agent {
		text.WriteString("\n\nIf the organization uses a Datadog site other than US1, replace " + defaultDatadogSite + " with that site.")
	}
	if agent {
		text.WriteString("\n\nDo not run testdrive, a configuration check or the tests yourself. Run resume_command once, after the edits above: it validates every discovered configuration and feature in disposable copies and returns the next action. When it returns setup_commands, run them as given, then its resume_command. Finish with its exact next_command and render final_response unchanged; only that coordinator result can certify `COMPLETE LOCALLY`.")
		return text.String()
	}
	_, _ = fmt.Fprintf(&text, "\n\n## Try it locally\n\nRun the local, credential-free setup check:\n\n```shell\nddtest testdrive --framework %s --agent --all --yes%s\n```\n\nAfter it finishes, post every `Open report:` link to the user so they can open the local Test Optimization report.\n\n## Ask a human to connect Datadog\n\n%s Do not ask for the API key in chat, and do not try to create or read the variable yourself. After the human confirms it is ready, commit and push the CI change; the next pipeline verifies the real Datadog backend connection.", framework, ciProviderFollowUpFlag("."), provider.apiKeyAction(false))
	return text.String()
}
