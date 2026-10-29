// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import "strings"

// CircleCI's Node.js and browser-tools orbs, modeled from their sources
// (CircleCI-Public/node-orb, CircleCI-Public/browser-tools-orb) for the
// releases configurations use. A model keeps what matters to the tests:
// the commands each step runs, the directory it runs them in and the Node
// version it selects (ddtest-runtime); caches and test-result uploads run
// nothing ddtest replays. Commands a model doesn't describe stay unresolved.
func (javascriptDiscovery) ciComponents(provider string) []ciComponent {
	if provider != circleCIProvider {
		return nil
	}
	return []ciComponent{
		{Name: "circleci/node", Majors: []string{"4"}, Definition: nodeOrbModel("")},
		{Name: "circleci/node", Majors: []string{"5", "6"}, Definition: nodeOrbModel(nodeOrbTestJob("    executor:\n      name: default\n      tag: << parameters.version >>\n", "      version:\n        type: string\n        default: lts\n"))},
		{Name: "circleci/node", Majors: []string{"7"}, Definition: nodeOrbModel(nodeOrbTestJob("    executor: << parameters.executor >>\n", "      executor:\n        type: string\n        default: default\n") + nodeOrbRunJob)},
		{Name: "circleci/browser-tools", Majors: []string{"1", "2"}, Definition: browserToolsOrbModel()},
	}
}

// nodeOrbPackageManagers are the node orb's pkg-manager values, with the
// command each runs a package script with and installs dependencies with
// (scripts/packages/*-install.sh).
var nodeOrbPackageManagers = []struct{ name, run, install string }{
	{"npm", "npm run", "npm ci"},
	{"yarn", "yarn run", "yarn install --frozen-lockfile"},
	{"yarn-berry", "yarn run", "yarn install --immutable"},
	{"pnpm", "pnpm run", "pnpm install --frozen-lockfile"},
	{"bun", "bun run", "bun install --frozen-lockfile"},
}

// nodeOrbModel is the node orb: its default executor, install (selecting a
// Node version), install-packages (one install command in app-dir, or the
// override-ci-command) and the package-manager installers, plus jobs.
func nodeOrbModel(jobs string) string {
	var text strings.Builder
	text.WriteString(`executors:
  default:
    parameters:
      tag:
        type: string
        default: lts
    docker:
      - image: cimg/node:<< parameters.tag >>
commands:
  install:
    parameters:
      node-version:
        type: string
        default: ""
    steps:
      - ddtest-runtime:
          js: << parameters.node-version >>
  install-yarn:
    steps:
      - ddtest-review: node/install-yarn installs Yarn on the CI machine; local validation uses the local Yarn.
  install-pnpm:
    steps:
      - ddtest-review: node/install-pnpm installs pnpm on the CI machine; local validation uses the local pnpm.
  install-bun:
    steps:
      - ddtest-review: node/install-bun installs Bun on the CI machine; local validation uses the local Bun.
  install-packages:
    parameters:
      pkg-manager:
        type: string
        default: npm
      app-dir:
        type: string
        default: "."
      override-ci-command:
        type: string
        default: ""
    steps:
      - when:
          condition: << parameters.override-ci-command >>
          steps:
            - run:
                working_directory: << parameters.app-dir >>
                command: << parameters.override-ci-command >>
      - unless:
          condition: << parameters.override-ci-command >>
          steps:
`)
	for _, manager := range nodeOrbPackageManagers {
		text.WriteString("            - when:\n                condition:\n                  equal: [" + manager.name + ", << parameters.pkg-manager >>]\n                steps:\n                  - run:\n                      working_directory: << parameters.app-dir >>\n                      command: " + manager.install + "\n")
	}
	if jobs != "" {
		text.WriteString("jobs:\n" + jobs)
	}
	return text.String()
}

// nodeOrbTestJob is the node orb's test job: checkout, setup steps,
// install-packages, post-install steps, then the package manager's run of
// run-command in app-dir, with the reporters test-results-for adds.
func nodeOrbTestJob(executor, executorParameter string) string {
	var text strings.Builder
	text.WriteString("  test:\n    parameters:\n" + executorParameter + `      pkg-manager:
        type: string
        default: npm
      run-command:
        type: string
        default: test
      app-dir:
        type: string
        default: ~/project
      override-ci-command:
        type: string
        default: ""
      test-results-for:
        type: string
        default: other
      setup:
        type: steps
        default: []
      post_install_steps:
        type: steps
        default: []
` + executor + `    steps:
      - checkout
      - steps: << parameters.setup >>
      - install-packages:
          app-dir: << parameters.app-dir >>
          pkg-manager: << parameters.pkg-manager >>
          override-ci-command: << parameters.override-ci-command >>
      - steps: << parameters.post_install_steps >>
`)
	reporters := map[string]string{"other": "", "jest": " --reporters=default --reporters=jest-junit", "mocha": " --reporter mocha-multi --reporter-options spec=-,mocha-junit-reporter=-"}
	for _, manager := range nodeOrbPackageManagers {
		for _, results := range []string{"other", "jest", "mocha"} {
			separator := ""
			if manager.name == "npm" && results != "other" {
				separator = " --"
			}
			text.WriteString("      - when:\n          condition:\n            and:\n              - equal: [" + manager.name + ", << parameters.pkg-manager >>]\n              - equal: [" + results + ", << parameters.test-results-for >>]\n          steps:\n            - run:\n                working_directory: << parameters.app-dir >>\n                command: " + manager.run + " << parameters.run-command >>" + separator + reporters[results] + "\n")
		}
	}
	text.WriteString("      - when:\n          condition:\n            equal: [bun, << parameters.test-results-for >>]\n          steps:\n            - ddtest-unresolved: node/test runs Bun's own test runner, which ddtest does not validate\n")
	return text.String()
}

// nodeOrbRunJob is the node orb's run job (7.x): install-packages, then
// the package manager's run of the script its <manager>-run names.
const nodeOrbRunJob = `  run:
    parameters:
      executor:
        type: string
        default: default
      pkg-manager:
        type: string
        default: npm
      npm-run:
        type: string
        default: ""
      yarn-run:
        type: string
        default: ""
      pnpm-run:
        type: string
        default: ""
      bun-run:
        type: string
        default: ""
      app-dir:
        type: string
        default: ~/project
      override-ci-command:
        type: string
        default: ""
      setup:
        type: steps
        default: []
      post_install_steps:
        type: steps
        default: []
    executor: << parameters.executor >>
    steps:
      - checkout
      - steps: << parameters.setup >>
      - install-packages:
          app-dir: << parameters.app-dir >>
          pkg-manager: << parameters.pkg-manager >>
          override-ci-command: << parameters.override-ci-command >>
      - steps: << parameters.post_install_steps >>
      - when:
          condition: << parameters.npm-run >>
          steps:
            - run:
                working_directory: << parameters.app-dir >>
                command: npm run << parameters.npm-run >>
      - when:
          condition: << parameters.yarn-run >>
          steps:
            - run:
                working_directory: << parameters.app-dir >>
                command: yarn run << parameters.yarn-run >>
      - when:
          condition: << parameters.pnpm-run >>
          steps:
            - run:
                working_directory: << parameters.app-dir >>
                command: pnpm run << parameters.pnpm-run >>
      - when:
          condition: << parameters.bun-run >>
          steps:
            - run:
                working_directory: << parameters.app-dir >>
                command: bun run << parameters.bun-run >>
`

// browserToolsOrbModel is the browser-tools orb: every command installs a
// browser or driver on the CI machine (1.x names use hyphens, 2.x
// underscores) and runs no tests.
func browserToolsOrbModel() string {
	var text strings.Builder
	text.WriteString("commands:\n")
	for _, tool := range []string{"browser-tools", "chrome", "chromedriver", "firefox", "geckodriver", "edge", "edge-driver", "chrome-for-testing"} {
		for _, name := range []string{"install-" + tool, "install_" + strings.ReplaceAll(tool, "-", "_")} {
			text.WriteString("  " + name + ":\n    steps:\n      - ddtest-review: browser-tools/" + name + " installs browsers or drivers on the CI machine; local validation uses the ones installed locally.\n")
		}
	}
	return text.String()
}
