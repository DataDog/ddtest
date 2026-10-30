// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// The Datadog Jenkins plugin sets up Test Optimization for a whole pipeline
// enabled with datadog(testOptimization: [enabled: true, serviceName: …,
// languages: […]]) in the declarative top-level options, or around a scripted
// pipeline's nodes. On each step it installs the tracer on the build agent
// with the agent's own toolchain (the latest release; a language whose
// tracer joins the project's dependencies uses a release they already
// pin), and sets the tracer's variables, the API key and the site from the
// plugin's global configuration. ddtest certifies the Jenkinsfile statically
// and asks the user to confirm what only Jenkins knows: the plugin's version
// and mode (see jenkinsFacts).

const (
	// jenkinsPluginReference names the setup in findings.
	jenkinsPluginReference = "Datadog Jenkins plugin"
	// jenkinsPluginMinimum is the first plugin release with the
	// testOptimization option ddtest certifies.
	jenkinsPluginMinimum = "5.6.2"
	// The facts the user confirms (--ci-confirm).
	jenkinsPluginFact     = "jenkins-plugin"
	jenkinsModeFact       = "jenkins-plugin-mode"
	jenkinsRuntimeMatches = "jenkins-agent-runtime-matches"
)

// jenkinsSiteValue is a Datadog site hostname.
var jenkinsSiteValue = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*\.[a-z]+$`)

// jenkinsFacts are the facts a Jenkins setup depends on that no Jenkinsfile
// shows.
var jenkinsFacts = []ciFactDefinition{
	{
		Key:      jenkinsPluginFact,
		Question: "Is the Datadog plugin " + jenkinsPluginMinimum + " or newer installed on the Jenkins controller? (Manage Jenkins > Plugins > Installed plugins lists it as Datadog.)",
		Remedy:   "If not, a Jenkins administrator installs or updates the Datadog plugin to " + jenkinsPluginMinimum + " or newer first.",
		Statement: func(string) string {
			return "The Datadog Jenkins plugin " + jenkinsPluginMinimum + " or newer is installed on the Jenkins controller"
		},
	},
	{
		Key:      jenkinsModeFact,
		Values:   "agentless:<site>|agent",
		Question: "How is the Datadog plugin configured in Manage Jenkins > System > Datadog Plugin: Agentless, with a Datadog API key stored as a Jenkins credential and which Datadog site (agentless:<site>, such as agentless:datadoghq.com), or Agent mode, with a Datadog Agent the build agents reach (agent)?",
		Remedy:   "If it is neither, a Jenkins administrator configures one first; never write the API key into the Jenkinsfile.",
		valid: func(value string) bool {
			site, agentless := strings.CutPrefix(value, "agentless:")
			return value == "agent" || agentless && jenkinsSiteValue.MatchString(site)
		},
		Statement: func(value string) string {
			if site, agentless := strings.CutPrefix(value, "agentless:"); agentless {
				return "The Datadog Jenkins plugin is configured in Agentless mode for " + site + ", with the API key as a Jenkins credential"
			}
			return "The Datadog Jenkins plugin is configured in Agent mode, with a Datadog Agent the build agents reach that reports to the selected Datadog site"
		},
	},
	{
		Key:      jenkinsRuntimeMatches,
		Question: "The plugin installs the tracer with the build agent's default toolchain (the one on the agent's PATH without the Jenkinsfile's tools, nodejs(…), PATH changes or version managers such as nvm use or rbenv). Is that the same runtime and package manager the tests run with in the listed jobs?",
		Remedy:   "If not, the tracer would be installed for another runtime: put the tests' runtime first on the agent's default PATH, or set up Test Optimization for those jobs manually (ddtest cannot validate that).",
		Statement: func(string) string {
			return "The Jenkins agents' default toolchain, which the Datadog Jenkins plugin installs the tracer with, is the one the tests run with"
		},
	},
}

// jenkinsSetup is a datadog(testOptimization: …) the Jenkinsfile enables.
type jenkinsSetup struct {
	source string
	// languages are the plugin's language tokens.
	languages []string
	service   string
	// site is an additionalVariables DD_SITE.
	site string
	// variables are the additionalVariables the plugin sets for every step.
	variables map[string]string
	problems  []string
}

func (s *jenkinsSetup) problem() string {
	if len(s.problems) == 0 {
		return ""
	}
	return strings.Join(s.problems, " ")
}

// jenkinsTestOptimization finds the testOptimization (or its legacy alias
// testVisibility) argument of a datadog call.
func jenkinsTestOptimization(call *groovyCall) (string, groovyNode, bool) {
	for _, arg := range call.Args {
		if arg.Name == "testOptimization" || arg.Name == "testVisibility" {
			return arg.Name, arg.Value, true
		}
	}
	return "", nil, false
}

// datadog reads a datadog(…) call; nil when it does not configure Test
// Optimization (such as datadog(collectLogs: true)).
func (r *jenkinsReader) datadog(call *groovyCall, scope jenkinsScope) *jenkinsSetup {
	key, value, ok := jenkinsTestOptimization(call)
	if !ok {
		return nil
	}
	setup := &jenkinsSetup{source: r.where(call), variables: map[string]string{}}
	fail := func(problem string) { setup.problems = append(setup.problems, problem) }
	options, literal := value.(*groovyMap)
	if !literal {
		fail("The datadog(" + key + ": …) option at " + setup.source + " is not a literal map, so ddtest cannot tell what it enables; write [enabled: true, serviceName: '<service>', languages: [...]] literally.")
		return setup
	}
	fields := map[string]groovyNode{}
	for _, entry := range options.Entries {
		fields[entry.Name] = entry.Value
	}
	if enabled, ok := fields["enabled"].(*groovyLiteral); !ok || enabled.Text != "true" {
		fail("The datadog(" + key + ": …) option at " + setup.source + " does not set enabled: true literally, so the plugin does not enable Test Optimization; set enabled: true.")
	}
	languages, isList := fields["languages"].(*groovyList)
	if !isList || len(languages.Items) == 0 {
		fail("The datadog(" + key + ": …) option at " + setup.source + " has no literal languages list; list the plugin's language tokens, such as languages: ['" + jenkinsLanguageExample() + "'].")
	} else {
		for _, item := range languages.Items {
			token, problem := r.value(item, scope)
			if problem != "" {
				fail("The datadog(" + key + ": …) option at " + setup.source + " lists a language ddtest cannot resolve: " + r.text(item) + ".")
				continue
			}
			setup.languages = append(setup.languages, strings.ToUpper(token))
		}
	}
	if service, problem := r.value(fields["serviceName"], scope); problem != "" || strings.TrimSpace(service) == "" {
		fail("The datadog(" + key + ": …) option at " + setup.source + " has no literal serviceName; without one the plugin reports the tests under service null. Set serviceName: '<service>'.")
	} else {
		setup.service = service
	}
	if variables, ok := fields["additionalVariables"]; ok {
		entries, isMap := variables.(*groovyMap)
		if !isMap {
			fail("The datadog(" + key + ": …) option at " + setup.source + " has additionalVariables ddtest cannot read; write them as a literal map.")
		} else {
			for _, entry := range entries.Entries {
				text, problem := r.value(entry.Value, scope)
				switch {
				case entry.Name == "DD_API_KEY":
					fail("The datadog(" + key + ": …) option at " + setup.source + " sets DD_API_KEY in additionalVariables, which writes the key into the Jenkinsfile; remove it and store the key as a Jenkins credential in the Datadog plugin's configuration.")
				case problem != "":
					fail("The datadog(" + key + ": …) option at " + setup.source + " sets " + entry.Name + " in additionalVariables to " + r.text(entry.Value) + ", which ddtest cannot resolve.")
				case entry.Name == "DD_CIVISIBILITY_ENABLED" && (strings.EqualFold(text, "false") || text == "0"):
					fail("The datadog(" + key + ": …) option at " + setup.source + " disables Test Optimization with DD_CIVISIBILITY_ENABLED=" + text + " in additionalVariables; remove it.")
				case entry.Name == "DD_SITE":
					setup.site = text
					setup.variables[entry.Name] = text
				default:
					setup.variables[entry.Name] = text
					if slices.ContainsFunc(discoveryLanguages, func(language discoveryLanguage) bool {
						exported, _ := language.ciPluginExports(nil)
						_, overwritten := exported[entry.Name]
						return overwritten
					}) {
						r.review = append(r.review, r.file+": "+entry.Name+" in the additionalVariables of datadog(…) at "+setup.source+" is overwritten by the variables the Datadog Jenkins plugin sets for the tracer; set those options inside the sh script that runs the tests instead.")
					}
				}
			}
		}
	}
	return setup
}

// jenkinsLanguageExample is a plugin language token for messages.
func jenkinsLanguageExample() string {
	return discoveryLanguages[0].ciPluginLanguage()
}

// ciSetup is the setup the plugin runs for each job in its scope.
func (s *jenkinsSetup) ciSetup() ciSetup {
	var tokens []string
	for _, language := range discoveryLanguages {
		if slices.Contains(s.languages, language.ciPluginLanguage()) {
			tokens = append(tokens, language.actionLanguage())
		}
	}
	inputs := map[string]string{"languages": strings.Join(tokens, " "), "api_key": "the Datadog plugin's Jenkins credential"}
	if s.site != "" {
		inputs["site"] = s.site
	}
	var exports []string
	for _, language := range discoveryLanguages {
		if slices.Contains(s.languages, language.ciPluginLanguage()) {
			_, references := language.ciPluginExports(nil)
			exports = append(exports, slices.Sorted(maps.Keys(references))...)
		}
	}
	return ciSetup{
		Reference: jenkinsPluginReference, Pinned: true, Supported: true, DefaultLatest: true, shaped: true,
		Floating: true, WorkspaceRoot: true, Problem: s.problem(), Inputs: inputs, Exports: exports,
		Confirmations: []string{jenkinsPluginFact, jenkinsModeFact},
		Caveats:       []string{"The Datadog Jenkins plugin installs the tracer on each build agent when a build runs, with that agent's toolchain; ddtest read the Jenkinsfile, not the plugin's configuration or the agents."},
	}
}

func (jenkins) setupName() string { return jenkinsPluginReference }

// setupInput names a setup input in the plugin's terms.
func (jenkins) setupInput(name string) string {
	switch name {
	case "languages":
		return "languages"
	case "site":
		return "DD_SITE in additionalVariables"
	}
	if strings.HasSuffix(name, "-tracer-version") {
		return "tracer release (the Datadog Jenkins plugin has no setting for it: it installs the latest release, or one the project's dependencies pin)"
	}
	return name
}

// setupProblem accepts the plugin's floating tracer when no release is
// given (the plugin installs the latest), but a release a language reads
// from the project must be exact. The site is the one additionalVariables
// sets, or the one the user confirmed for Agentless mode.
func (jenkins) setupProblem(setup ciSetup, tracerInput string, exact func(string) bool, example, site string) string {
	if setup.Problem != "" {
		return setup.Problem
	}
	if tracer, pinned := setup.Inputs[tracerInput]; pinned && !exact(tracer) {
		return "The Datadog Jenkins plugin would install " + fmt.Sprintf("%q", tracer) + ", which is not an exact tracer release; pin an exact release, such as " + example + ", in the project's dependencies as the setup instructions show."
	}
	configured, source := setup.Inputs["site"], "DD_SITE in its additionalVariables"
	if configured == "" {
		if mode, stated := statedFact(jenkinsModeFact); stated {
			configured, _ = strings.CutPrefix(mode, "agentless:")
			source = "--ci-confirm " + jenkinsModeFact + "=" + mode
			if mode == "agent" {
				configured = ""
			}
		}
	}
	if site != "" && configured != "" && configured != site {
		return "The Datadog Jenkins plugin reports to " + configured + " (" + source + "), but the selected Datadog site is " + site + "; configure the plugin for " + site + ", or select " + configured + "."
	}
	return ""
}

func (jenkins) apiKeyAction(bool) string {
	return "Ask a Jenkins administrator to configure the Datadog plugin (" + jenkinsPluginMinimum + " or newer) in Manage Jenkins > System > Datadog Plugin: Agentless mode with the Datadog API key stored as a Jenkins credential and the selected Datadog site, or Agent mode with a Datadog Agent the build agents reach. Never write the key into the Jenkinsfile. Until the plugin reports, the tests run untraced."
}

// The plugin sets its variables for every step; a step's own changes go in
// the sh script that runs the tests.
func (jenkins) setupTerms() ciSetupTerms {
	return ciSetupTerms{
		Installer:  "Datadog Jenkins plugin",
		Entry:      "the datadog(testOptimization: …) option",
		SetupEntry: "the datadog(testOptimization: …) option",
		Later:      "every step of the pipeline",
		Variables:  "the stage's environment directive",
		Persist: func(command string) string {
			return command + " inside the sh script that runs the tests, before the test command"
		},
		PersistEntry:    func(command string) string { return command },
		MatrixExclusion: "with the matrix's excludes",
		SharedMatrix:    "a matrix shares the stage's steps",
		ImageGuard:      func(string, string, string) string { return "" },
		Plugin:          true,
	}
}

// jenkinsPluginToken is the plugin's token for an installer language token.
func jenkinsPluginToken(language string) string {
	for _, registered := range discoveryLanguages {
		if registered.actionLanguage() == language {
			return registered.ciPluginLanguage()
		}
	}
	return strings.ToUpper(language)
}

func (j jenkins) setupInstructions(request ciSetupRequest) string {
	token := jenkinsPluginToken(request.Language)
	var text strings.Builder
	text.WriteString("Set up Datadog Test Optimization in Jenkins with the Datadog Jenkins plugin, enabled for the whole pipeline in the Jenkinsfile. The plugin installs the tracer on each build agent when a build runs and sets its variables for every step, so the Jenkinsfile only enables it.\n\n")
	text.WriteString("1. Ask the user to have a Jenkins administrator install the Datadog plugin " + jenkinsPluginMinimum + " or newer on the Jenkins controller and configure it in Manage Jenkins > System > Datadog Plugin: Agentless mode with the Datadog API key stored as a Jenkins credential and the site " + request.Site + ", or Agent mode with a Datadog Agent the build agents reach. Never write the key into the repository, the Jenkinsfile or additionalVariables. ddtest cannot read Jenkins' configuration, so after the edit it asks the user to confirm the plugin and its mode (--ci-confirm) before it certifies anything.\n")
	text.WriteString("2. In every Jenkinsfile that runs the tests listed above, enable Test Optimization for the pipeline. In a declarative pipeline, add the datadog option to the pipeline's top-level options directive (create the directive after agent if there is none; when it already has a datadog(…) call, add testOptimization to it and keep its other settings); stage-level options are not certified:\n\n```groovy\noptions {\n  datadog(testOptimization: [enabled: true, serviceName: '<service>', languages: ['" + token + "']])\n}\n```\n\nIn a scripted pipeline, wrap the node blocks that run the tests in it instead, re-indenting them and keeping every line:\n\n```groovy\ndatadog(testOptimization: [enabled: true, serviceName: '<service>', languages: ['" + token + "']]) {\n  node {\n    // existing stages\n  }\n}\n```\n\nReplace <service> with the service name the test results should report under, such as the repository name. When the pipeline already enables other languages, add '" + token + "' to the existing languages list. Keep every other line, stage, agent and step unchanged.\n")
	text.WriteString("\n3. The plugin only instruments steps that run on a host agent (agent any, agent { label … }, node(…)). It does not instrument tests inside containers (agent { docker … }, dockerfile, kubernetes, a podTemplate's node(POD_LABEL), docker.image(…).inside, container(…), or docker run in an sh script): ddtest reports those stages as unsupported. Do not move tests between agents to obtain a pass; tell the user and let them decide.\n")
	if len(request.After) > 0 {
		text.WriteString("\n4. In the sh script of each stage that runs the tests, add this line before the test command (inside the same sh string, so it runs in the test's shell):\n\n```sh\n" + strings.Join(request.After, "\n") + "\n```\n")
	}
	for _, note := range request.Notes {
		text.WriteString("\n- " + strings.ReplaceAll(note, "\n", "\n  "))
	}
	return strings.TrimSpace(text.String())
}
