// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// jenkinsRepository is a Jest repository whose only CI is the Jenkinsfile;
// the test runs in it with Jenkins selected.
func jenkinsRepository(t *testing.T, jenkinsfile string) string {
	t.Helper()
	root := newJestRepository(t, jestWorkflow)
	require.NoError(t, os.RemoveAll(filepath.Join(root, ".github")))
	writeFile(t, root, "Jenkinsfile", jenkinsfile)
	t.Chdir(root)
	useCIProvider(t, "jenkins")
	return root
}

// jenkinsJobs reads the jobs of a Jenkinsfile.
func jenkinsJobs(t *testing.T, source string) map[string]runtimeJob {
	t.Helper()
	return jenkinsWorkflow(t, source).Jobs
}

func jenkinsWorkflow(t *testing.T, source string) ciWorkflow {
	t.Helper()
	workflow, err := readJenkinsfile(t.TempDir(), "Jenkinsfile", source)
	require.NoError(t, err)
	return workflow
}

// jenkinsRuns lists a job's commands, and the problems of unresolved steps.
func jenkinsRuns(job runtimeJob) []string {
	var runs []string
	for _, step := range job.Steps {
		switch {
		case step.Setup != nil:
			runs = append(runs, "SETUP")
		case step.ResolutionError != "":
			runs = append(runs, "UNRESOLVED "+step.ResolutionError)
		case step.Run != "":
			runs = append(runs, step.Run)
		}
	}
	return runs
}

const jenkinsDatadogOption = "  options {\n    datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVASCRIPT']])\n  }\n"

func TestJenkinsConfigurationFiles(t *testing.T) {
	root := t.TempDir()
	for _, file := range []string{"Jenkinsfile", "Jenkinsfile.release", "ci.Jenkinsfile", "jenkins/Jenkinsfile", ".ci/nightly.jenkinsfile", "docs/Jenkinsfile", "README.md", "jenkins/helper.groovy"} {
		writeFile(t, root, file, "node {}\n")
	}
	files, err := jenkins{}.configurationFiles(root)
	require.NoError(t, err)
	require.Equal(t, []string{".ci/nightly.jenkinsfile", "Jenkinsfile", "Jenkinsfile.release", "ci.Jenkinsfile", "jenkins/Jenkinsfile"}, files)
	workflows, err := jenkins{}.readWorkflows(root)
	require.NoError(t, err)
	require.Len(t, workflows, 5)
	require.Contains(t, workflows[0].Caveats[0], "Jenkins selects each job's Jenkinsfile (its Script Path) in the job configuration")
	empty, err := jenkins{}.configurationFiles(t.TempDir())
	require.NoError(t, err)
	require.Empty(t, empty)
}

// Each leaf stage is a job; each top-level statement of an sh script one
// step, sharing that script's shell, and every sh a fresh shell.
func TestJenkinsDeclarativeStages(t *testing.T) {
	jobs := jenkinsJobs(t, `pipeline {
  agent { label 'linux' }
  environment {
    CI = 'true'
    TOKEN = credentials('npm-token')
    REVISION = sh(returnStdout: true, script: 'git rev-parse HEAD').trim()
  }
  stages {
    stage('Install') { steps { sh 'npm ci' } }
    stage('Checks') {
      parallel {
        stage('Lint') { steps { sh 'npm run lint' } }
        stage('Unit') {
          environment { SUITE = 'unit' }
          steps {
            sh '''
              export FORCE_COLOR=0
              cd packages/app
              npm test -- --suite "$SUITE"
            '''
            sh 'npm run report'
          }
        }
      }
    }
    stage('Nested') {
      stages {
        stage('Inner') { steps { sh "echo ${env.CI}" } }
      }
    }
    stage('Never') {
      when { expression { return false } }
      steps { sh 'npm run never' }
    }
    stage('Maybe') {
      when { branch 'main' }
      steps { sh 'npm run maybe' }
    }
  }
}`)
	require.Equal(t, []string{"Checks / Lint", "Checks / Unit", "Install", "Maybe", "Nested / Inner", "Never"}, slices.Sorted(maps.Keys(jobs)))
	unit := jobs["Checks / Unit"]
	require.Equal(t, "the Jenkins agents labeled \"linux\"", unit.Host)
	require.Equal(t, "true", unit.Env["CI"])
	require.Equal(t, "unit", unit.Env["SUITE"])
	require.NoError(t, unresolvedCIError(unit.Env["CI"]))
	require.Equal(t, "${{ secrets.TOKEN }}", unit.Env["TOKEN"], "a credential is a secret only the build has")
	require.Error(t, unresolvedCIError(unit.Env["REVISION"]))
	require.Equal(t, []string{`npm test -- --suite "$SUITE"`, "npm run report"}, jenkinsRuns(unit))
	require.Equal(t, "packages/app", unit.Steps[2].WorkingDirectory, "cd carries within one sh script")
	require.Equal(t, "0", unit.Steps[2].Env["FORCE_COLOR"])
	require.Empty(t, unit.Steps[3].WorkingDirectory, "every sh starts a fresh shell")
	require.Empty(t, unit.Steps[3].Env["FORCE_COLOR"])
	require.Equal(t, []string{"echo true"}, jenkinsRuns(jobs["Nested / Inner"]), "a known environment value interpolates")
	require.Equal(t, "false", jobs["Never"].If)
	require.Empty(t, jobs["Maybe"].If, "a when ddtest cannot decide is a possible path")
}

func TestJenkinsMatrixAndPost(t *testing.T) {
	workflow := jenkinsWorkflow(t, `pipeline {
  agent any
  stages {
    stage('Test') {
      matrix {
        axes {
          axis { name 'NODE'; values '20', '22' }
          axis { name 'OS'; values 'linux', 'mac' }
        }
        excludes {
          exclude {
            axis { name 'NODE'; values '20' }
            axis { name 'OS'; notValues 'linux' }
          }
        }
        stages {
          stage('Run') { steps { sh 'npm test' } }
        }
      }
    }
    stage('Report') {
      steps { sh 'npm run coverage' }
      post {
        always { sh 'npm run upload' }
        failure { sh 'npm run diagnose' }
      }
    }
  }
  post {
    success { sh 'npm run notify' }
  }
}`)
	require.Equal(t, []string{"Report", "Test / Run (20, linux)", "Test / Run (22, linux)", "Test / Run (22, mac)", "post"}, slices.Sorted(maps.Keys(workflow.Jobs)))
	require.Equal(t, "22", workflow.Jobs["Test / Run (22, mac)"].Env["NODE"])
	require.Equal(t, "mac", workflow.Jobs["Test / Run (22, mac)"].Env["OS"])
	require.Equal(t, []string{"npm run coverage", "npm run upload"}, jenkinsRuns(workflow.Jobs["Report"]), "post conditions of a passing build run after the steps")
	require.Equal(t, []string{"npm run notify"}, jenkinsRuns(workflow.Jobs["post"]))
	require.Contains(t, strings.Join(workflow.Review, "\n"), "the post { failure } block at Jenkinsfile:25 runs only when the build does not pass")

	large := "pipeline {\n  agent any\n  stages {\n    stage('M') {\n      matrix {\n        axes {\n"
	for _, axis := range []string{"A", "B", "C"} {
		large += "          axis { name '" + axis + "'; values '1', '2', '3', '4', '5', '6' }\n"
	}
	large += "        }\n        stages { stage('t') { steps { sh 'npm test' } } }\n      }\n    }\n  }\n}\n"
	jobs := jenkinsJobs(t, large)
	require.Equal(t, []string{"UNRESOLVED Jenkinsfile M: ddtest cannot expand its matrix (it has more than 200 cells), so it cannot tell which stages it runs"}, jenkinsRuns(jobs["M"]))
}

func TestJenkinsAgentsAndContainers(t *testing.T) {
	jobs := jenkinsJobs(t, `pipeline {
  agent none
  tools { nodejs 'NodeJS 22' }
  stages {
    stage('Docker') {
      agent { docker { image 'node:22-alpine' } }
      steps { sh 'npm test' }
    }
    stage('Dockerfile') {
      agent { dockerfile true }
      steps { sh 'npm test' }
    }
    stage('Kubernetes') {
      agent { kubernetes { yaml 'pod.yaml' } }
      steps { container('node') { sh 'npm test' } }
    }
    stage('Host') {
      agent { label 'linux' }
      steps { sh 'npm test' }
    }
    stage('Nowhere') {
      steps { sh 'npm test' }
    }
    stage('Windows') {
      agent { label 'windows' }
      steps { bat 'npm test' }
    }
  }
}`)
	docker := jobs["Docker"]
	require.Equal(t, "node:22-alpine", docker.Image)
	require.Contains(t, docker.SetupUnsupported, "it runs in agent { docker { image 'node:22-alpine' } }, a container: the Datadog Jenkins plugin installs the tracer on the build agent and does not instrument tests inside containers")
	require.Contains(t, jobs["Dockerfile"].ImageProblem, "agent { dockerfile … }")
	require.NotEmpty(t, jobs["Kubernetes"].SetupUnsupported)
	require.Empty(t, jobs["Host"].SetupUnsupported)
	require.Equal(t, "the Jenkins agents labeled \"linux\", with the Jenkins tool nodejs 'NodeJS 22' (tools directive) on PATH", jobs["Host"].Host)
	require.Equal(t, []string{"UNRESOLVED Jenkinsfile Nowhere: it runs under agent none, without an agent for shell steps"}, jenkinsRuns(jobs["Nowhere"]))
	require.Equal(t, []string{"UNRESOLVED Jenkinsfile Windows: runs bat, a Windows command; ddtest does not set up or validate Windows agents"}, jenkinsRuns(jobs["Windows"]))
}

// Anything ddtest cannot read statically is an unresolved step, never
// dropped: computed scripts, unknown steps (shared libraries), Groovy
// functions, load, build and tests started in containers by sh.
func TestJenkinsUnresolvedSteps(t *testing.T) {
	jobs := jenkinsJobs(t, `@Library('ci-lib') _
def runTests(target) { sh "npm test ${target}" }
pipeline {
  agent any
  parameters { string(name: 'SUITE', defaultValue: 'unit') }
  stages {
    stage('Computed') { steps { sh "npm test ${env.SHARD}" } }
    stage('Parameter') { steps { sh "npm test -- ${params.SUITE}" } }
    stage('Library') { steps { nodePipeline(node: 22) } }
    stage('Wrapper') { steps { withNpmEnv { sh 'npm test' } } }
    stage('Configured') { steps { nodePipeline { suite = 'unit' } } }
    stage('Function') { steps { script { runTests('unit') } } }
    stage('Load') { steps { load 'ci/steps.groovy' } }
    stage('Build') { steps { build job: 'downstream' } }
    stage('Docker run') { steps { sh 'docker run --rm -v "$PWD:/app" node:22 npm test' } }
    stage('Compose') { steps { sh 'docker-compose -f ci.yml run tests' } }
    stage('Shebang') { steps { sh '''#!/usr/bin/env python3
print("x")
''' } }
    stage('Literal') { steps { sh 'npm test' } }
  }
}`)
	unresolved := func(name string) string {
		runs := jenkinsRuns(jobs[name])
		require.Len(t, runs, 1, name)
		return runs[0]
	}
	require.Contains(t, unresolved("Computed"), "its sh script reads ${env.SHARD}, which only the Jenkins build knows")
	require.Equal(t, "npm test -- unit", unresolved("Parameter"), "a parameter's default is known")
	require.Contains(t, unresolved("Library"), "it calls nodePipeline, which is not a Pipeline step ddtest knows; it may come from the shared library ci-lib")
	// The unknown wrapper is a step of its own, and its steps keep why.
	wrapper := jenkinsRuns(jobs["Wrapper"])
	require.Len(t, wrapper, 2)
	require.Contains(t, wrapper[0], "it calls withNpmEnv, which is not a Pipeline step ddtest knows; it may come from the shared library ci-lib, and may run tests")
	require.Contains(t, wrapper[1], "it runs inside withNpmEnv(…), which is not a Pipeline step ddtest knows")
	require.Contains(t, unresolved("Configured"), "it calls nodePipeline, which is not a Pipeline step ddtest knows; it may come from the shared library ci-lib", "a closure that only configures the library still runs its steps")
	require.Contains(t, unresolved("Function"), "it calls runTests(), a Groovy function of the Jenkinsfile")
	require.Contains(t, unresolved("Load"), "load runs Groovy code from 'ci/steps.groovy'")
	require.Contains(t, unresolved("Build"), "build triggers another Jenkins job")
	require.Contains(t, unresolved("Docker run"), "it runs docker run: tests inside run in a container ddtest cannot validate, and the Datadog Jenkins plugin does not instrument them")
	require.Contains(t, unresolved("Compose"), "it runs docker-compose run")
	require.Contains(t, unresolved("Shebang"), "its sh script runs with /usr/bin/env python3, not a shell ddtest reads")
	require.Equal(t, "npm test", unresolved("Literal"))
	// The step keeps the command, so ddtest can tell whether it may run tests.
	require.Equal(t, `docker run --rm -v "$PWD:/app" node:22 npm test`, jobs["Docker run"].Steps[0].Run)
}

func TestJenkinsScriptedPipelines(t *testing.T) {
	workflow := jenkinsWorkflow(t, `node('linux') {
  checkout scm
  stage('Build') {
    sh 'npm ci'
  }
  stage('Test') {
    dir('web') {
      withEnv(['NODE_ENV=test', 'PATH+NODE=/opt/node/bin']) {
        sh 'npm test'
      }
    }
    docker.image('node:22').inside {
      sh 'npm run e2e'
    }
  }
  parallel(
    unit: { sh 'npm run unit' },
    lint: { sh 'npm run lint' },
    failFast: true
  )
  if (env.BRANCH_NAME == 'main') {
    sh 'npm run deploy'
  }
  try {
    sh 'npm run flaky'
  } catch (err) {
    sh 'npm run on-failure'
  }
  env.SUITE = 'all'
  sh "npm run ${env.SUITE}"
}
stage('Outside') {
  node {
    sh 'npm run outside'
  }
}`)
	jobs := workflow.Jobs
	require.Equal(t, []string{"Build", "Outside", "Test", "Test (docker.image('node:22').inside)", "lint", "node (linux)", "unit"}, slices.Sorted(maps.Keys(jobs)))
	test := jobs["Test"]
	require.Equal(t, []string{"npm test"}, jenkinsRuns(test))
	require.Equal(t, "web", test.Steps[0].WorkingDirectory)
	require.Equal(t, "test", test.Steps[0].Env["NODE_ENV"])
	require.Empty(t, test.SetupUnsupported, "the container runs only its own steps")
	inside := jobs["Test (docker.image('node:22').inside)"]
	require.Equal(t, []string{"npm run e2e"}, jenkinsRuns(inside))
	require.Equal(t, "node:22", inside.Image)
	require.Contains(t, inside.SetupUnsupported, "it runs in docker.image('node:22').inside, a container inside Test, whose steps outside the container run on the build agent")
	require.Equal(t, []string{"npm run deploy", "npm run flaky", "npm run on-failure", "npm run all"}, jenkinsRuns(jobs["node (linux)"]), "a catch block can recover, so it is a possible passing path")
	require.Equal(t, "the Jenkins agents labeled \"linux\"", jobs["node (linux)"].Host)
	require.Equal(t, "any Jenkins agent (node, with no label)", jobs["Outside"].Host)
	require.Equal(t, []string{"checkout"}, jenkinsNonTest(workflow))
}

func jenkinsNonTest(workflow ciWorkflow) []string {
	if len(workflow.Caveats) == 0 {
		return nil
	}
	_, list, _ := strings.Cut(workflow.Caveats[0], "outside this validation: ")
	return strings.Split(strings.TrimSuffix(list, "."), ", ")
}

func TestJenkinsRecognizesThePluginSetup(t *testing.T) {
	jobs := jenkinsJobs(t, `pipeline {
  agent { label 'linux' }
  options {
    timeout(time: 1, unit: 'HOURS')
    datadog(collectLogs: true, testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVASCRIPT', 'RUBY', 'GO'], additionalVariables: [DD_SITE: 'datadoghq.eu', NODE_OPTIONS: '--x']])
  }
  stages {
    stage('Test') {
      environment { NODE_OPTIONS = '--max-old-space-size=4096' }
      steps { sh 'npm test' }
    }
    stage('Tools') {
      tools { nodejs '22' }
      steps { sh 'npm test' }
    }
  }
}`)
	test := jobs["Test"]
	require.Equal(t, []string{"SETUP", "npm test"}, jenkinsRuns(test))
	setup := test.Steps[0].Setup
	require.Equal(t, jenkinsPluginReference, setup.Reference)
	require.Equal(t, "js ruby", setup.Inputs["languages"])
	require.Equal(t, "datadoghq.eu", setup.Inputs["site"])
	require.True(t, setup.Floating)
	require.True(t, setup.WorkspaceRoot)
	require.Empty(t, setup.Problem)
	require.Equal(t, []string{jenkinsPluginFact, jenkinsModeFact}, setup.Confirmations)
	require.Equal(t, "-r ${{ env.DD_TRACE_PACKAGE }}", test.Steps[1].Env["NODE_OPTIONS"], "the plugin sets NODE_OPTIONS once per build, over a stage's")
	require.Equal(t, "-rbundler/setup -rdatadog/ci/auto_instrument", test.Steps[1].Env["RUBYOPT"])
	selected, err := setupSelectsLanguage(*setup, nil, "js")
	require.NoError(t, err)
	require.True(t, selected)
	require.Equal(t, []string{jenkinsPluginFact, jenkinsModeFact, jenkinsRuntimeMatches}, jobs["Tools"].Steps[0].Setup.Confirmations, "a PATH change needs the agent runtime confirmed")

	pipelineOptions := jenkinsJobs(t, "pipeline {\n  agent any\n  environment { NODE_OPTIONS = '--max-old-space-size=4096' }\n"+jenkinsDatadogOption+"  stages { stage('T') { steps { sh 'npm test' } } }\n}\n")
	require.Equal(t, "-r ${{ env.DD_TRACE_PACKAGE }} --max-old-space-size=4096", pipelineOptions["T"].Steps[1].Env["NODE_OPTIONS"], "the plugin prepends its preload to the pipeline's options")
	workflow := jenkinsWorkflow(t, "pipeline {\n  agent any\n"+jenkinsDatadogOption+"  stages {\n    stage('T') {\n      steps {\n        withEnv(['NODE_OPTIONS=--inspect']) { sh 'npm test' }\n      }\n    }\n  }\n}\n")
	require.Contains(t, strings.Join(workflow.Review, "\n"), "NODE_OPTIONS set in withEnv at Jenkinsfile:9 is overwritten by the Datadog Jenkins plugin")
	require.Equal(t, "-r ${{ env.DD_TRACE_PACKAGE }}", workflow.Jobs["T"].Steps[1].Env["NODE_OPTIONS"])

	scripted := jenkinsJobs(t, "datadog(testVisibility: [enabled: true, serviceName: 'app', languages: ['RUBY']]) {\n  node {\n    stage('Test') { sh 'bundle exec rspec' }\n  }\n}\nnode { stage('Other') { sh 'bundle exec rspec' } }\n")
	require.Equal(t, "ruby", scripted["Test"].Steps[0].Setup.Inputs["languages"], "testVisibility is the option's legacy name")
	require.Equal(t, []string{"bundle exec rspec"}, jenkinsRuns(scripted["Other"]), "the wrap applies only to the nodes inside it")
}

func TestJenkinsSetupProblems(t *testing.T) {
	for _, tc := range []struct{ name, option, problem string }{
		{name: "computed", option: "datadog(testOptimization: settings)", problem: "is not a literal map"},
		{name: "disabled", option: "datadog(testOptimization: [enabled: false, serviceName: 'a', languages: ['JAVASCRIPT']])", problem: "does not set enabled: true literally"},
		{name: "no languages", option: "datadog(testOptimization: [enabled: true, serviceName: 'a'])", problem: "has no literal languages list"},
		{name: "no service", option: "datadog(testOptimization: [enabled: true, languages: ['JAVASCRIPT']])", problem: "without one the plugin reports the tests under service null"},
		{name: "key", option: "datadog(testOptimization: [enabled: true, serviceName: 'a', languages: ['JAVASCRIPT'], additionalVariables: [DD_API_KEY: 'abc']])", problem: "sets DD_API_KEY in additionalVariables, which writes the key into the Jenkinsfile"},
		{name: "opt out", option: "datadog(testOptimization: [enabled: true, serviceName: 'a', languages: ['JAVASCRIPT'], additionalVariables: [DD_CIVISIBILITY_ENABLED: 'false']])", problem: "disables Test Optimization with DD_CIVISIBILITY_ENABLED=false"},
		{name: "computed variable", option: "datadog(testOptimization: [enabled: true, serviceName: 'a', languages: ['JAVASCRIPT'], additionalVariables: [DD_ENV: env.X]])", problem: "sets DD_ENV in additionalVariables to env.X, which ddtest cannot resolve"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jobs := jenkinsJobs(t, "pipeline {\n  agent any\n  options { "+tc.option+" }\n  stages { stage('T') { steps { sh 'npm test' } } }\n}\n")
			require.Contains(t, jobs["T"].Steps[0].Setup.Problem, tc.problem)
			require.Contains(t, jenkins{}.setupProblem(*jobs["T"].Steps[0].Setup, "js-tracer-version", exactRelease, "", ""), tc.problem)
		})
	}
	jobs := jenkinsJobs(t, "pipeline {\n  agent any\n  environment { DD_API_KEY = 'abc' }\n"+jenkinsDatadogOption+"  stages { stage('T') { steps { sh 'npm test' } } }\n}\n")
	require.Contains(t, jobs["T"].Steps[0].Setup.Problem, "DD_API_KEY is written into the Jenkinsfile (the environment directive at Jenkinsfile:3)")
	workflow := jenkinsWorkflow(t, "pipeline {\n  agent any\n  environment { DD_API_KEY = credentials('dd') }\n"+jenkinsDatadogOption+"  stages { stage('T') { steps { sh 'npm test' } } }\n}\n")
	require.Empty(t, workflow.Jobs["T"].Steps[0].Setup.Problem)
	require.Contains(t, workflow.Review[0], "binds DD_API_KEY from a Jenkins credential; the Datadog Jenkins plugin overwrites DD_API_KEY")
	jobs = jenkinsJobs(t, "pipeline {\n  agent any\n  stages {\n    stage('T') {\n      options { datadog(testOptimization: [enabled: true, serviceName: 'a', languages: ['JAVASCRIPT']]) }\n      steps { sh 'npm test' }\n    }\n  }\n}\n")
	require.Contains(t, jobs["T"].Steps[0].Setup.Problem, "is in a stage's options; ddtest certifies the Datadog Jenkins plugin setup only in the pipeline's top-level options")
	jobs = jenkinsJobs(t, "pipeline {\n  agent { docker { image 'node:22' } }\n"+jenkinsDatadogOption+"  stages { stage('T') { steps { sh 'npm test' } } }\n}\n")
	require.Contains(t, jobs["T"].Steps[0].Setup.Problem, "This stage cannot use the Datadog Jenkins plugin: it runs in agent { docker { image 'node:22' } }")
}

// The plugin installs the latest tracer: setupProblem accepts no release,
// but a release a language reads from the project must be exact, and the
// site is the one additionalVariables or the confirmed mode names.
func TestJenkinsSetupProblemTracerAndSite(t *testing.T) {
	setup := (&jenkinsSetup{languages: []string{"JAVASCRIPT"}, service: "a"}).ciSetup()
	require.Empty(t, jenkins{}.setupProblem(setup, "js-tracer-version", exactRelease, "", "datadoghq.com"))
	pinned := setup
	pinned.Inputs = maps.Clone(setup.Inputs)
	pinned.Inputs["ruby-tracer-version"] = ""
	require.Contains(t, jenkins{}.setupProblem(pinned, "ruby-tracer-version", exactRubyRelease.MatchString, "1.42.0", ""), "is not an exact tracer release")
	pinned.Inputs["ruby-tracer-version"] = "1.42.0"
	require.Empty(t, jenkins{}.setupProblem(pinned, "ruby-tracer-version", exactRubyRelease.MatchString, "1.42.0", ""))

	useCIProvider(t, "jenkins")
	useCIStatements(t, nil, "jenkins-plugin-mode=agentless:datadoghq.eu")
	require.Contains(t, jenkins{}.setupProblem(setup, "js-tracer-version", exactRelease, "", "datadoghq.com"), "The Datadog Jenkins plugin reports to datadoghq.eu (--ci-confirm jenkins-plugin-mode=agentless:datadoghq.eu), but the selected Datadog site is datadoghq.com")
	require.Empty(t, jenkins{}.setupProblem(setup, "js-tracer-version", exactRelease, "", "datadoghq.eu"))
	useCIStatements(t, nil, "jenkins-plugin-mode=agent")
	require.Empty(t, jenkins{}.setupProblem(setup, "js-tracer-version", exactRelease, "", "datadoghq.com"), "in Agent mode the Agent selects the site")
	site := setup
	site.Inputs = maps.Clone(setup.Inputs)
	site.Inputs["site"] = "us5.datadoghq.com"
	require.Contains(t, jenkins{}.setupProblem(site, "js-tracer-version", exactRelease, "", "datadoghq.com"), "reports to us5.datadoghq.com (DD_SITE in its additionalVariables)")
}

func TestJenkinsFacts(t *testing.T) {
	useCIProvider(t, "jenkins")
	for _, tc := range []struct {
		facts []string
		err   string
	}{
		{facts: []string{"jenkins-plugin", "jenkins-plugin-mode=agentless:datadoghq.com", "jenkins-agent-runtime-matches"}},
		{facts: []string{"jenkins-plugin-mode=agent"}},
		{facts: []string{"jenkins-plugin-mode=agentless"}, err: "--ci-confirm jenkins-plugin-mode needs a value: jenkins-plugin-mode=agentless:<site>|agent"},
		{facts: []string{"jenkins-plugin-mode=agentless:not a site"}, err: "needs a value"},
		{facts: []string{"jenkins-plugin=yes"}, err: "--ci-confirm jenkins-plugin takes no value"},
		{facts: []string{"plugin"}, err: "--ci-confirm plugin is not a Jenkins fact; facts: jenkins-plugin, jenkins-plugin-mode, jenkins-agent-runtime-matches"},
	} {
		var facts []CIFact
		for _, value := range tc.facts {
			fact, err := ParseCIConfirm(value)
			require.NoError(t, err)
			facts = append(facts, fact)
		}
		err := UseCIStatements(nil, facts)
		if tc.err != "" {
			require.ErrorContains(t, err, tc.err, tc.facts)
			continue
		}
		require.NoError(t, err, tc.facts)
	}
	useCIStatements(t, nil, "jenkins-plugin", "jenkins-plugin-mode=agentless:datadoghq.com")
	stated, _ := StatedFacts(nil, "javascript")
	require.Equal(t, []string{
		"The Datadog Jenkins plugin 5.6.2 or newer is installed on the Jenkins controller (--ci-confirm)",
		"The Datadog Jenkins plugin is configured in Agentless mode for datadoghq.com, with the API key as a Jenkins credential (--ci-confirm)",
	}, stated)
}

// With the plugin in place, ddtest asks for the facts only Jenkins shows
// before it certifies anything, and only for jobs that run the tests.
func TestJenkinsAsksToConfirmThePlugin(t *testing.T) {
	root := jenkinsRepository(t, "pipeline {\n  agent any\n"+jenkinsDatadogOption+"  stages {\n    stage('Lint') { steps { sh 'echo lint' } }\n    stage('Test') { tools { nodejs '22' }\n      steps { sh 'npm test' } }\n  }\n}\n")
	required := RequiredCIConfirmations(root, "javascript", "jest")
	require.NotNil(t, required)
	require.Equal(t, []string{"Jenkinsfile / Test"}, required.Jobs)
	var keys []string
	for _, fact := range required.Facts {
		keys = append(keys, fact.Key)
	}
	require.Equal(t, []string{jenkinsPluginFact, jenkinsModeFact, jenkinsRuntimeMatches}, keys)
	result := CIConfirmationRequiredResult(required)
	require.Equal(t, "needs_input", result.Status)
	require.Equal(t, "ci_setup_confirmation_required", result.Code)
	require.Equal(t, "ask_user", result.NextAction)
	require.Equal(t, []string{"ci_confirm:jenkins-plugin", "ci_confirm:jenkins-plugin-mode=agentless:<site>|agent", "ci_confirm:jenkins-agent-runtime-matches"}, result.RequiredInput)
	require.Contains(t, result.Instruction, "jenkins-plugin: Is the Datadog plugin 5.6.2 or newer installed on the Jenkins controller?")
	require.Contains(t, result.Instruction, "rerun the same command adding --ci-confirm jenkins-plugin --ci-confirm jenkins-plugin-mode=agentless:<site>|agent --ci-confirm jenkins-agent-runtime-matches")

	useCIStatements(t, nil, "jenkins-plugin", "jenkins-plugin-mode=agent")
	required = RequiredCIConfirmations(root, "javascript", "jest")
	require.Len(t, required.Facts, 1)
	useCIStatements(t, nil, "jenkins-plugin", "jenkins-plugin-mode=agent", "jenkins-agent-runtime-matches")
	require.Nil(t, RequiredCIConfirmations(root, "javascript", "jest"))

	// Without the setup, or with a broken one, nothing is asked yet.
	writeFile(t, root, "Jenkinsfile", "pipeline {\n  agent any\n  stages { stage('Test') { steps { sh 'npm test' } } }\n}\n")
	require.NoError(t, UseCIStatements(nil, nil))
	require.Nil(t, RequiredCIConfirmations(root, "javascript", "jest"))
	writeFile(t, root, "Jenkinsfile", "pipeline {\n  agent any\n  options { datadog(testOptimization: [enabled: true, languages: ['JAVASCRIPT']]) }\n  stages { stage('Test') { steps { sh 'npm test' } } }\n}\n")
	require.Nil(t, RequiredCIConfirmations(root, "javascript", "jest"))
	require.Contains(t, strings.Join(existingSetupProblems(root, "javascript", "jest", ""), "\n"), "Jenkinsfile / Test: The datadog(testOptimization: …) option at Jenkinsfile:3 has no literal serviceName")
}

func TestJenkinsLanguageSupport(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "Jenkinsfile", "node { sh 'pytest' }\n")
	useCIProvider(t, "jenkins")
	for language, supported := range map[string]bool{"javascript": true, "ruby": true, "python": false, "java": false} {
		err := CheckCIProviderSupport(root, language, "framework")
		if supported {
			require.NoError(t, err, language)
			continue
		}
		var unsupported *CIProviderUnsupportedError
		require.ErrorAs(t, err, &unsupported, language)
		require.Equal(t, "Jenkins", unsupported.Provider)
		require.Equal(t, "ci_provider_unsupported", ProviderUnsupportedResult(unsupported).Code)
	}
}

// Every job running the tests in a container: the plugin cannot instrument
// any, so ddtest reports the repository unsupported instead of prescribing.
func TestJenkinsContainerTestJobsAreUnsupported(t *testing.T) {
	root := jenkinsRepository(t, "pipeline {\n  agent { docker { image 'node:22' } }\n  stages {\n    stage('Test') { steps { sh 'npm test' } }\n  }\n}\n")
	err := CheckCIProviderSupport(root, "javascript", "jest")
	var unsupported *CIProviderUnsupportedError
	require.ErrorAs(t, err, &unsupported)
	result := ProviderUnsupportedResult(unsupported)
	require.Equal(t, "unsupported", result.Status)
	require.Equal(t, "ci_setup_unsupported", result.Code)
	require.Contains(t, result.Reason, "every CI job that runs the tests is outside what the Datadog Jenkins plugin instruments. Jenkinsfile / Test: it runs in agent { docker { image 'node:22' } }")
	require.Contains(t, result.Reason, "The user's options are to run those tests where the Datadog Jenkins plugin instruments them, or to set up Test Optimization for them by hand, outside ddtest, which ddtest cannot validate.")
	require.Contains(t, result.Instruction, "Report this result to the user with its reason and stop. Do not move tests between agents, edit CI, add a tracer or set up Test Optimization by hand")

	var out bytes.Buffer
	require.Error(t, StartWithOptions(&out, StartOptions{Format: "json"}))
	var started AgentResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &started), out.String())
	require.Equal(t, "ci_setup_unsupported", started.Code)

	// One host job is enough to set up; the container job stays reported.
	writeFile(t, root, "Jenkinsfile", "pipeline {\n  agent none\n  stages {\n    stage('Docker') { agent { docker { image 'node:22' } }\n      steps { sh 'npm test' } }\n    stage('Host') { agent any\n      steps { sh 'npm test' } }\n  }\n}\n")
	require.NoError(t, CheckCIProviderSupport(root, "javascript", "jest"))
	request := javascriptDiscovery{}.ciSetupRequest(root, "jest", "datadoghq.com", jenkins{}.setupTerms())
	require.Contains(t, strings.Join(request.Notes, "\n"), "The Datadog Jenkins plugin cannot instrument these CI jobs, so ddtest reports them as unsupported and does not validate them; tell the user, and do not move their tests to other agents without the user's approval:\nJenkinsfile / Docker: it runs in agent { docker { image 'node:22' } }")
}

func TestJenkinsProviderChoice(t *testing.T) {
	root := newJestRepository(t, jestWorkflow)
	writeFile(t, root, "Jenkinsfile", "pipeline {\n  agent any\n  stages { stage('Test') { steps { sh 'npm test' } } }\n}\n")
	UseCIProvider("")
	_, err := SelectCIProvider(root, "")
	var choice *CIProviderChoice
	require.ErrorAs(t, err, &choice)
	require.Equal(t, []string{"github", "jenkins"}, choice.Providers)
	writeFile(t, root, "Jenkinsfile", "pipeline {\n  agent any\n  stages { stage('Deploy') { steps { sh 'echo deploy' } } }\n}\n")
	selected, err := SelectCIProvider(root, "")
	require.NoError(t, err)
	require.Equal(t, "github", selected)
}

func TestJenkinsInstructions(t *testing.T) {
	root := jenkinsRepository(t, "pipeline {\n  agent any\n  stages { stage('Test') { steps { sh 'npm test' } } }\n}\n")
	text := providerSetupInstructions(root, jenkins{}, "javascript", "vitest", "datadoghq.eu", true)
	require.Contains(t, text, "datadog(testOptimization: [enabled: true, serviceName: '<service>', languages: ['JAVASCRIPT']])")
	require.Contains(t, text, "Agentless mode with the Datadog API key stored as a Jenkins credential and the site datadoghq.eu")
	require.Contains(t, text, "add this line before the test command (inside the same sh string, so it runs in the test's shell):\n\n```sh\n"+pluginESMImportCommand+"\n```")
	require.Contains(t, text, "installs the latest dd-trace release with the build agent's default npm when each build runs (it cannot pin a release)")
	require.NotContains(t, text, "Try it locally", "agents run the coordinator")
	require.NotContains(t, text, "ddtest onboard --framework vitest --apply", "onboard --apply does not edit Jenkinsfiles")

	ruby := rubyDiscovery{}.ciSetupRequest(root, "rspec", "datadoghq.com", jenkins{}.setupTerms())
	notes := strings.Join(ruby.Notes, "\n")
	require.Contains(t, notes, `Pin datadog-ci in the bundle: add gem "datadog-ci", "<exact datadog-ci release>", group: :test to the Gemfile at the checkout root`)
	require.Contains(t, notes, "keep the Gemfile at the checkout root and run the rspec tests from there: not inside dir(…), and without BUNDLE_GEMFILE")
	writeFile(t, root, "Gemfile.lock", "GEM\n  specs:\n    datadog-ci (1.40.0)\n")
	ruby = rubyDiscovery{}.ciSetupRequest(root, "rspec", "datadoghq.com", jenkins{}.setupTerms())
	require.Contains(t, ruby.Notes[0], "already pins datadog-ci 1.40.0")

	require.False(t, providerAutomaticSetup(jenkins{}, javascriptDiscovery{}, "jest"), "Jenkins setup is applied from instructions")
	require.True(t, providerAutomaticSetup(buildkite{}, javascriptDiscovery{}, "jest"))
	_, err := planProviderJavaScriptSetup(root, jenkins{}, nil, "jest", "5.0.0", "datadoghq.com", "")
	require.ErrorContains(t, err, "onboard --apply does not edit the Jenkins configuration")
}

// The committed fixture is ral-facilities/scigateway's declarative
// Jenkinsfile: Jest in the Unit Tests stage of a labeled host agent, and an
// E2E stage outside the Jest path.
func TestJenkinsJavaScriptFixture(t *testing.T) {
	root := copyFixture(t, "jenkins-javascript")
	t.Chdir(root)
	useCIProvider(t, "jenkins")
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "jest", scope.Commands[0].Command)
	required := RequiredCIRuntimes(t.Context(), root, "javascript", "jest")
	require.NotNil(t, required)
	require.Equal(t, []string{"Jenkinsfile / Unit Tests"}, required.Jobs)
	require.Equal(t, []string{"the Jenkins agents labeled \"sl7\""}, required.Hosts)
	require.False(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", ""))

	jenkinsfile, err := os.ReadFile(filepath.Join(root, "Jenkinsfile"))
	require.NoError(t, err)
	writeFile(t, root, "Jenkinsfile", strings.Replace(string(jenkinsfile), "    agent { label 'sl7'}\n", "    agent { label 'sl7'}\n"+jenkinsDatadogOption, 1))
	require.True(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", "datadoghq.com"))
	useCIStatements(t, []string{"node=22"}, "jenkins-plugin", "jenkins-plugin-mode=agentless:datadoghq.com")
	require.Nil(t, RequiredCIRuntimes(t.Context(), root, "javascript", "jest"))
	require.Nil(t, RequiredCIConfirmations(root, "javascript", "jest"))
	result := checkCIRuntimes(t.Context(), root, nil, func(_ context.Context, action, version string) (tracerRequirement, error) {
		require.Equal(t, jenkinsPluginReference, action)
		require.Equal(t, "latest", version, "the plugin installs the latest dd-trace")
		return tracerRequirement{Version: "6.22.0", Node: ">=18", Requested: "latest"}, nil
	}, "jest")
	require.Equal(t, StatusCompatible, result.Status, result)
	var unit RuntimeFinding
	for _, finding := range result.Jobs {
		if finding.Job == "Unit Tests" {
			unit = finding
		}
	}
	require.True(t, unit.UserStated)
	require.True(t, unit.TracerFloating)
	require.Equal(t, "dd-trace@6.22.0", unit.Tracer)
	caveats := CISetupCaveats(&result)
	require.Len(t, caveats, 2)
	require.Contains(t, caveats[1], "dd-trace version: the Datadog Jenkins plugin installs the latest dd-trace release when each build runs and cannot pin one; ddtest checked the CI runtime against, and validated locally with, dd-trace@6.22.0")

	// additionalVariables that turn the Jest integration off reach the test.
	writeFile(t, root, "Jenkinsfile", strings.Replace(string(jenkinsfile), "    agent { label 'sl7'}\n", "    agent { label 'sl7'}\n  options {\n    datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVASCRIPT'], additionalVariables: [DD_TRACE_JEST_ENABLED: 'false']])\n  }\n", 1))
	disabled := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "6.22.0", Node: ">=18", Requested: "latest"}, nil
	}, "jest")
	require.Equal(t, StatusInconclusive, disabled.Status)
	for _, finding := range disabled.Jobs {
		if finding.Job == "Unit Tests" {
			require.Equal(t, "JavaScript CI instrumentation is explicitly disabled by DD_TRACE_JEST_ENABLED", finding.Reason)
		}
	}
}

// An ESM framework needs the plugin's loader in the test's sh script.
func TestJenkinsESMLoader(t *testing.T) {
	check := func(script string) RuntimeCheck {
		root := jenkinsRepository(t, "pipeline {\n  agent any\n"+jenkinsDatadogOption+"  stages { stage('Test') { steps { sh '''"+script+"''' } } }\n}\n")
		writeFile(t, root, "package.json", `{"scripts": {"test": "vitest run"}, "devDependencies": {"vitest": "3.2.4"}}`)
		useCIStatements(t, []string{"node=22"})
		return checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
			return tracerRequirement{Version: "6.22.0", Node: ">=18"}, nil
		}, "vitest")
	}
	require.NotEqual(t, StatusCompatible, check("npm test").Status, "without the loader, ESM tests are not traced")
	result := check(pluginESMImportCommand + "\nnpm test")
	require.Equal(t, StatusCompatible, result.Status, result)
	require.Equal(t, "--max-old-space-size=4096", mustNodeOptions(t, "-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_PATH }}/register.js --max-old-space-size=4096"), "local replay drops the plugin's loader")
}

func mustNodeOptions(t *testing.T, value string) string {
	t.Helper()
	resolved, err := resolveNodeOptions(value, nil, nil)
	require.NoError(t, err)
	return resolved
}

// The committed fixture is deanwilson/puppet-multitemplate's scripted
// Jenkinsfile: bundle exec rake spec in the Test stage of a node block.
func TestJenkinsRubyFixture(t *testing.T) {
	root := copyFixture(t, "jenkins-ruby")
	t.Chdir(root)
	useCIProvider(t, "jenkins")
	scope, err := DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "bundle exec rake spec", scope.Commands[0].Command)
	require.Equal(t, []string{"Jenkinsfile / Test"}, RequiredCIRuntimes(t.Context(), root, "ruby", "rspec").Jobs)

	jenkinsfile, err := os.ReadFile(filepath.Join(root, "Jenkinsfile"))
	require.NoError(t, err)
	wrapped := strings.Replace(string(jenkinsfile), "node {", "datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['RUBY']]) {\nnode {", 1) + "}\n"
	writeFile(t, root, "Jenkinsfile", wrapped)
	useCIStatements(t, []string{"ruby=3.3"}, "jenkins-plugin", "jenkins-plugin-mode=agentless:datadoghq.com")
	finding := func() RuntimeFinding {
		for _, job := range RubyCIConfigurationForSite(root, "rspec", "datadoghq.com").Jobs {
			if job.Job == "Test" {
				return job
			}
		}
		t.Fatal("no Test finding")
		return RuntimeFinding{}
	}
	unpinned := finding()
	require.Equal(t, CodeIncompleteCISetup, unpinned.Code)
	require.Contains(t, unpinned.Reason, "runs bundle add datadog-ci, which unfreezes the bundle and installs the latest datadog-ci release")
	require.False(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", "datadoghq.com"))

	lock, err := os.ReadFile(filepath.Join(root, "Gemfile.lock"))
	require.NoError(t, err)
	writeFile(t, root, "Gemfile.lock", strings.Replace(string(lock), "    diff-lcs (1.6.2)\n", "    datadog-ci (1.42.0)\n    diff-lcs (1.6.2)\n", 1))
	pinned := finding()
	require.Equal(t, "not checked", pinned.Status, pinned.Reason)
	require.Equal(t, "1.42.0", pinned.TracerRequested)
	require.Equal(t, "3.3", pinned.Runtime)
	require.Contains(t, pinned.SetupCaveats[1], "uses the datadog-ci release the repository's Gemfile.lock pins (1.42.0)")
	require.True(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", "datadoghq.com"))

	writeFile(t, root, "Jenkinsfile", strings.Replace(wrapped, "sh 'bundle exec rake spec'", "dir('sub') { sh 'bundle exec rake spec' }", 1))
	require.Contains(t, finding().Reason, "adds datadog-ci from the checkout root's bundle, but this test command runs in sub")
	writeFile(t, root, "Jenkinsfile", strings.Replace(wrapped, "sh 'bundle exec rake spec'", "withEnv(['BUNDLE_GEMFILE=gemfiles/rails.gemfile']) { sh 'bundle exec rake spec' }", 1))
	require.Contains(t, finding().Reason, "selects BUNDLE_GEMFILE=gemfiles/rails.gemfile")
	// additionalVariables that turn the RSpec integration off reach the test.
	writeFile(t, root, "Jenkinsfile", strings.Replace(wrapped, "languages: ['RUBY']]", "languages: ['RUBY'], additionalVariables: [DD_TRACE_RSPEC_ENABLED: 'false']]", 1))
	disabled := finding()
	require.Equal(t, "Ruby CI instrumentation is explicitly disabled by DD_TRACE_RSPEC_ENABLED", disabled.Reason)
	require.Equal(t, StatusInconclusive, disabled.Status)
}

// ${WORKSPACE} is the checkout: in an environment value, the shared model's
// expression for it; in a script, the shell's $WORKSPACE.
func TestJenkinsWorkspace(t *testing.T) {
	jobs := jenkinsJobs(t, "pipeline {\n  agent any\n  environment {\n    GEM_DIR = \"${WORKSPACE}/.gem\"\n    npm_config_cache = \"${WORKSPACE}/.npm\"\n    PATH = \"${WORKSPACE}/node_modules/.bin:${env.PATH}\"\n  }\n  stages { stage('T') { steps {\n    dir(\"${WORKSPACE}/web\") { sh \"cd ${env.WORKSPACE}/web; npm test\" }\n  } } }\n}\n")
	job := jobs["T"]
	require.Equal(t, ciProjectDirectory+"/.gem", job.Env["GEM_DIR"])
	require.NotContains(t, job.Env, "npm_config_cache", "an install directory in the workspace is the agent's, not the tests'")
	require.NotContains(t, job.Env, "PATH")
	require.Equal(t, "any Jenkins agent (agent any, with no label)", job.Host, "the checkout's node_modules/.bin is no toolchain change")
	require.Equal(t, "web", job.Steps[0].WorkingDirectory)
	require.Equal(t, []string{"npm test"}, jenkinsRuns(job))
	require.Equal(t, "web", job.Steps[1].WorkingDirectory)
}

// A coding agent that applies the setup by hand gets the validation scope,
// unresolved entries included, even for frameworks whose automatic setup
// prints none.
func TestJenkinsAgentInstructionsListTheScope(t *testing.T) {
	jenkinsRepository(t, "@Library('lib') _\npipeline {\n  agent any\n  stages {\n    stage('Test') { steps { sh 'npm test' } }\n    stage('Publish') { steps { publishImage() } }\n  }\n}\n")
	var guidance bytes.Buffer
	require.NoError(t, run(&guidance, &agentHandoff{}))
	require.Contains(t, guidance.String(), "jest — Jenkinsfile / Test / step 1 (npm test)")
	require.Contains(t, guidance.String(), "Unresolved scope: Jenkinsfile / Publish / step 1 (): Jenkinsfile Publish: it calls publishImage, which is not a Pipeline step ddtest knows; it may come from the shared library lib")
}

// A Kubernetes pod template's node(POD_LABEL) is a pod agent: a container
// the plugin does not instrument, as agent { kubernetes … } is.
func TestJenkinsPodTemplatesAreContainers(t *testing.T) {
	jobs := jenkinsJobs(t, `datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVASCRIPT']]) {
  podTemplate(yaml: 'pod.yaml') {
    node(POD_LABEL) {
      stage('Pod') { sh 'npm test' }
    }
  }
  podTemplate(label: 'mypod', containers: [containerTemplate(name: 'node', image: 'node:22')]) {
    node('mypod') {
      stage('Labeled pod') { sh 'npm test' }
    }
    node('linux') {
      stage('Host') { sh 'npm test' }
    }
  }
}`)
	pod := jobs["Pod"]
	require.Equal(t, "it runs in node(POD_LABEL) of the Kubernetes podTemplate at Jenkinsfile:2, a container: the Datadog Jenkins plugin installs the tracer on the build agent and does not instrument tests inside containers", pod.SetupUnsupported)
	require.Contains(t, pod.ImageProblem, "whose image ddtest does not read")
	require.Empty(t, pod.Host)
	require.Contains(t, pod.Steps[0].Setup.Problem, "This stage cannot use the Datadog Jenkins plugin: it runs in node(POD_LABEL) of the Kubernetes podTemplate")
	require.Contains(t, jobs["Labeled pod"].SetupUnsupported, "it runs in node('mypod') of the Kubernetes podTemplate at Jenkinsfile:7")
	require.Empty(t, jobs["Host"].SetupUnsupported, "a node on another label is a host agent")
	require.Equal(t, "the Jenkins agents labeled \"linux\"", jobs["Host"].Host)

	root := jenkinsRepository(t, "podTemplate {\n  node(POD_LABEL) {\n    stage('Test') { sh 'npm test' }\n  }\n}\n")
	var unsupported *CIProviderUnsupportedError
	require.ErrorAs(t, CheckCIProviderSupport(root, "javascript", "jest"), &unsupported)
	require.Contains(t, ProviderUnsupportedResult(unsupported).Reason, "Jenkinsfile / Test: it runs in node(POD_LABEL) of the Kubernetes podTemplate at Jenkinsfile:1")
}

// The plugin sets additionalVariables after the pipeline's environment and
// withEnv: the test steps, and local validation, see the plugin's values.
func TestJenkinsAdditionalVariables(t *testing.T) {
	workflow := jenkinsWorkflow(t, `pipeline {
  agent any
  environment { DD_ENV = 'pipeline' }
  options {
    datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVASCRIPT'], additionalVariables: [DD_TRACE_JEST_ENABLED: 'false', DD_ENV: 'ci', SUITE: 'unit']])
  }
  stages {
    stage('T') {
      steps {
        withEnv(['DD_TRACE_JEST_ENABLED=true']) { sh 'npm test -- "$SUITE"' }
      }
    }
  }
}`)
	job := workflow.Jobs["T"]
	step := job.Steps[1]
	require.Equal(t, `npm test -- "$SUITE"`, step.Run)
	require.Equal(t, "false", step.Env["DD_TRACE_JEST_ENABLED"], "additionalVariables apply after withEnv")
	require.Equal(t, "ci", step.Env["DD_ENV"], "additionalVariables apply after the environment directive")
	require.Equal(t, "unit", step.Env["SUITE"])
	review := strings.Join(workflow.Review, "\n")
	require.Contains(t, review, "DD_ENV set in the Jenkinsfile is overwritten by the additionalVariables of datadog(…) at Jenkinsfile:5")
	require.Contains(t, review, "DD_TRACE_JEST_ENABLED set in the Jenkinsfile is overwritten by the additionalVariables")
	// Local validation replays the integration switch.
	_, env, err := resolveVariantStep(workflow, job, step, nil, "jest")
	require.NoError(t, err)
	require.Equal(t, "false", env["DD_TRACE_JEST_ENABLED"])

	// A setup ddtest refuses sets nothing.
	refused := jenkinsJobs(t, "pipeline {\n  agent any\n  options { datadog(testOptimization: [enabled: true, serviceName: 'a', languages: ['JAVASCRIPT'], additionalVariables: [DD_API_KEY: 'abc', DD_ENV: 'ci']]) }\n  stages { stage('T') { steps { sh 'npm test' } } }\n}\n")
	require.NotContains(t, refused["T"].Steps[1].Env, "DD_API_KEY")
	require.NotContains(t, refused["T"].Steps[1].Env, "DD_ENV")
}

// A catch can recover, so its steps are a possible passing path; a finally
// runs after either. What the try assigns is unknown in the catch.
func TestJenkinsCatchAndFinally(t *testing.T) {
	jobs := jenkinsJobs(t, `node {
  stage('T') {
    env.SUITE = 'unit'
    try {
      env.SUITE = 'integration'
      sh 'npm run flaky'
    } catch (err) {
      sh "npm run ${env.SUITE}"
      sh 'npm run fallback'
    } finally {
      sh 'npm run cleanup'
    }
    sh "npm run ${env.SUITE}"
  }
  stage('Finally') {
    try {
      env.TARGET = 'e2e'
    } finally {
      sh "npm run ${env.TARGET}"
    }
  }
}`)
	runs := jenkinsRuns(jobs["T"])
	require.Len(t, runs, 5)
	require.Equal(t, "npm run flaky", runs[0])
	require.Contains(t, runs[1], "its sh script reads ${env.SUITE}, which only the Jenkins build knows", "the try may stop before or after its assignment")
	require.Equal(t, []string{"npm run fallback", "npm run cleanup"}, runs[2:4])
	require.Contains(t, runs[4], "its sh script reads ${env.SUITE}", "the try and the catch leave different values")
	require.Equal(t, []string{"npm run e2e"}, jenkinsRuns(jobs["Finally"]), "without a catch, a passing build ran the whole try")
}

// env.X = … changes the build's environment for the rest of the build, from
// any closure; withEnv and an environment directive keep their own value
// inside them, and a value set on some paths only is unknown.
func TestJenkinsEnvironmentAssignmentsPersist(t *testing.T) {
	jobs := jenkinsJobs(t, `pipeline {
  agent any
  environment { FIXED = 'pipeline' }
  stages {
    stage('A') {
      steps {
        script { env.SUITE = 'unit' }
        script { env.SUITE = 'integration'; env.FIXED = 'changed' }
        sh "npm run ${env.SUITE} ${env.FIXED}"
        withEnv(['SUITE=scoped']) {
          sh "npm run ${env.SUITE}"
          script { env.SUITE = 'after' }
          sh "npm run ${env.SUITE}"
        }
        sh "npm run ${env.SUITE}"
        dir('web') { timeout(5) { env.SUITE = 'web' } }
        sh "npm run ${env.SUITE}"
        script {
          if (params.ALL) { env.SUITE = 'maybe' }
        }
        sh "npm run ${env.SUITE}"
      }
    }
    stage('Set') { steps { script { env.LATER = 'e2e'; suite = 'smoke' } } }
    stage('Use') { steps { sh "npm run ${env.LATER}"; sh "npm run ${suite}" } }
  }
}`)
	runs := jenkinsRuns(jobs["A"])
	require.Len(t, runs, 6)
	require.Equal(t, []string{"npm run integration pipeline", "npm run scoped", "npm run scoped", "npm run after", "npm run web"}, runs[:5])
	require.Contains(t, runs[5], "its sh script reads ${env.SUITE}, which only the Jenkins build knows")
	require.Equal(t, []string{"npm run e2e", "npm run smoke"}, jenkinsRuns(jobs["Use"]), "assignments reach later stages")
	require.Equal(t, "web", jobs["A"].Steps[4].Env["SUITE"], "the step runs with the build's value")

	scripted := jenkinsJobs(t, `node {
  stage('T') {
    def suite = 'unit'
    retry(2) { suite = 'retried' }
    sh "npm run ${suite}"
    timeout(5) {
      suite = 'integration'
      def local = 'x'
    }
    sh "npm run ${suite} ${local}"
    parallel(a: { env.MODE = 'a' }, b: { env.MODE = 'b' })
    sh "npm run ${env.MODE}"
  }
}`)
	runs = jenkinsRuns(scripted["T"])
	require.Len(t, runs, 3)
	require.Contains(t, runs[0], "its sh script reads ${suite}", "retry may run its closure more than once")
	require.Contains(t, runs[1], "its sh script reads ${local}", "a closure's def stays in the closure")
	require.Contains(t, runs[2], "its sh script reads ${env.MODE}", "parallel branches race")
	scripted = jenkinsJobs(t, "node {\n  stage('T') {\n    def suite = 'unit'\n    timeout(5) { suite = 'integration' }\n    sh \"npm run ${suite}\"\n  }\n}\n")
	require.Equal(t, []string{"npm run integration"}, jenkinsRuns(scripted["T"]))
}

// What an assignment or a return evaluates runs: a literal sh is a step,
// and an unknown call is unresolved. A method on a value runs nothing.
func TestJenkinsExecutableExpressions(t *testing.T) {
	jobs := jenkinsJobs(t, `@Library('ci-lib') _
def runTests() { sh 'npm test' }
pipeline {
  agent any
  stages {
    stage('Function') { steps { script { def outcome = runTests() } } }
    stage('Output') { steps { script { def out = sh(script: 'npm test', returnStdout: true).trim() } } }
    stage('Return') { steps { script { return sh('npm test -- --testPathPattern=integration') } } }
    stage('Library') { steps { script { def result = nodeTests(suite: 'unit') } } }
    stage('Library method') { steps { script { results = ci.runTests() } } }
    stage('Value') { steps { script {
      def tag = env.BRANCH_NAME.trim()
      def abbreviated = tag.take(7)
      def files = ['a', 'b'].collect { it.toUpperCase() }
      def image = docker.image('node:22')
    } } }
  }
}`)
	require.Contains(t, strings.Join(jenkinsRuns(jobs["Function"]), "\n"), "it calls runTests(), a Groovy function of the Jenkinsfile")
	require.Equal(t, []string{"npm test"}, jenkinsRuns(jobs["Output"]))
	require.Equal(t, []string{"npm test -- --testPathPattern=integration"}, jenkinsRuns(jobs["Return"]))
	require.Contains(t, strings.Join(jenkinsRuns(jobs["Library"]), "\n"), "it calls nodeTests, which is not a Pipeline step ddtest knows; it may come from the shared library ci-lib")
	require.Contains(t, strings.Join(jenkinsRuns(jobs["Library method"]), "\n"), "it calls ci.runTests, which is not a Pipeline step ddtest knows")
	require.NotContains(t, jobs, "Value", "value methods run no step")
}

// Top-level statements run in order: a declaration before the pipeline is
// visible in it, one after it is not.
func TestJenkinsTopLevelDeclarations(t *testing.T) {
	jobs := jenkinsJobs(t, "def suite = 'unit'\nenv.MODE = 'ci'\npipeline {\n  agent any\n  stages { stage('T') { steps { sh \"npm run ${suite} ${env.MODE}\" } } }\n}\n")
	require.Equal(t, []string{"npm run unit ci"}, jenkinsRuns(jobs["T"]))
	jobs = jenkinsJobs(t, "pipeline {\n  agent any\n  stages { stage('T') { steps { sh \"npm run ${later}\" } } }\n}\ndef later = 'unit'\n")
	require.Contains(t, jenkinsRuns(jobs["T"])[0], "its sh script reads ${later}, which only the Jenkins build knows")
}

// A version manager in a test job's sh selects another runtime than the
// agent's default, which the plugin installs the tracer with: the user
// confirms they match.
func TestJenkinsRuntimeManagersNeedTheAgentRuntimeConfirmed(t *testing.T) {
	jobs := jenkinsJobs(t, "pipeline {\n  agent { label 'linux' }\n"+jenkinsDatadogOption+`  stages {
    stage('Nvm') { steps { sh '. "$NVM_DIR/nvm.sh"; nvm use 20 && npm test' } }
    stage('Fnm') { steps { sh '''
      fnm use 20
      npm test
    ''' } }
    stage('N') { steps { sh 'n 20' ; sh 'npm test' } }
    stage('Rbenv') { steps { sh 'eval "$(rbenv init -)" && bundle exec rspec' } }
    stage('Rvm') { steps { sh 'rvm use 3.3 do bundle exec rspec' } }
    stage('Plain') { steps { sh 'nvm ls; npm test' } }
  }
}`)
	for _, name := range []string{"Nvm", "Fnm", "N", "Rbenv", "Rvm"} {
		require.Contains(t, jobs[name].Steps[0].Setup.Confirmations, jenkinsRuntimeMatches, name)
	}
	require.Equal(t, `the Jenkins agents labeled "linux", with the runtime . "$NVM_DIR/nvm.sh" (sh script at Jenkinsfile:7) selects on PATH`, jobs["Nvm"].Host)
	require.Contains(t, jobs["Fnm"].Host, "the runtime fnm use 20 (sh script at Jenkinsfile:8) selects")
	require.Contains(t, jobs["Rbenv"].Host, "rbenv init -")
	require.Equal(t, []string{jenkinsPluginFact, jenkinsModeFact}, jobs["Plain"].Steps[0].Setup.Confirmations, "nvm ls selects nothing")

	root := jenkinsRepository(t, "pipeline {\n  agent any\n"+jenkinsDatadogOption+"  stages {\n    stage('Test') { steps { sh 'nvm use 22 && npm test' } }\n  }\n}\n")
	required := RequiredCIConfirmations(root, "javascript", "jest")
	require.NotNil(t, required)
	var keys []string
	for _, fact := range required.Facts {
		keys = append(keys, fact.Key)
	}
	require.Equal(t, []string{jenkinsPluginFact, jenkinsModeFact, jenkinsRuntimeMatches}, keys)
}

// A container block runs only its own steps in the container: the job's
// steps on the agent stay instrumentable, and the container's are a job of
// their own, unsupported.
func TestJenkinsContainerBlocksAreTheirOwnJobs(t *testing.T) {
	jobs := jenkinsJobs(t, `pipeline {
  agent { label 'linux' }
  stages {
    stage('Test') {
      steps {
        sh 'npm test'
        script {
          docker.image('node:22').inside { sh 'npm pack' }
          container('node') { sh 'npm run e2e' }
        }
        sh 'npm run report'
      }
    }
  }
}`)
	test := jobs["Test"]
	require.Equal(t, []string{"npm test", "npm run report"}, jenkinsRuns(test))
	require.Empty(t, test.SetupUnsupported)
	require.Empty(t, test.Image)
	require.Equal(t, `the Jenkins agents labeled "linux"`, test.Host)
	packaging := jobs["Test (docker.image('node:22').inside)"]
	require.Equal(t, []string{"npm pack"}, jenkinsRuns(packaging))
	require.Equal(t, "node:22", packaging.Image)
	require.Contains(t, packaging.SetupUnsupported, "a container inside Test, whose steps outside the container run on the build agent and are validated as that job")
	e2e := jobs["Test (container('node') of a Kubernetes pod)"]
	require.Equal(t, []string{"npm run e2e"}, jenkinsRuns(e2e))
	require.NotEmpty(t, e2e.ImageProblem)

	// A host test next to a packaging container sets up; a test in the
	// container is reported, naming the job it splits from.
	root := jenkinsRepository(t, "pipeline {\n  agent any\n  stages {\n    stage('Test') {\n      steps {\n        sh 'npm test'\n        script { docker.image('node:22').inside { sh 'npm pack' } }\n      }\n    }\n  }\n}\n")
	require.NoError(t, CheckCIProviderSupport(root, "javascript", "jest"))
	writeFile(t, root, "Jenkinsfile", "pipeline {\n  agent any\n  stages {\n    stage('Test') {\n      steps {\n        sh 'npm test'\n        script { docker.image('node:22').inside { sh 'npm test' } }\n      }\n    }\n  }\n}\n")
	require.NoError(t, CheckCIProviderSupport(root, "javascript", "jest"))
	request := javascriptDiscovery{}.ciSetupRequest(root, "jest", "datadoghq.com", jenkins{}.setupTerms())
	require.Contains(t, strings.Join(request.Notes, "\n"), "Jenkinsfile / Test (docker.image('node:22').inside): it runs in docker.image('node:22').inside, a container inside Test, whose steps outside the container run on the build agent")
	writeFile(t, root, "Jenkinsfile", "pipeline {\n  agent any\n  stages {\n    stage('Test') {\n      steps {\n        sh 'npm ci'\n        script { docker.image('node:22').inside { sh 'npm test' } }\n      }\n    }\n  }\n}\n")
	var unsupported *CIProviderUnsupportedError
	require.ErrorAs(t, CheckCIProviderSupport(root, "javascript", "jest"), &unsupported, "the only test runs in the container")
	require.Contains(t, unsupported.Reason, "Jenkinsfile / Test (docker.image('node:22').inside)")

	// Inside a job that already runs in a container, a container block
	// stays part of it.
	pod := jenkinsJobs(t, "pipeline {\n  agent { kubernetes { yaml 'pod.yaml' } }\n  stages { stage('K') { steps { container('node') { sh 'npm test' } } } }\n}\n")
	require.Equal(t, []string{"npm test"}, jenkinsRuns(pod["K"]))
	require.NotEmpty(t, pod["K"].SetupUnsupported)
}

// The arguments of a step that runs no command still run: echo sh(…) runs
// the sh. In a configuration step's arguments, a call names a plugin
// component and runs nothing.
func TestJenkinsNonCommandStepArguments(t *testing.T) {
	workflow := jenkinsWorkflow(t, `pipeline {
  agent any
  stages {
    stage('Echo') { steps { echo sh(script: 'npm run integration', returnStdout: true) } }
    stage('Write') { steps { writeFile file: 'out.txt', text: sh(script: 'npm test', returnStdout: true).trim() } }
    stage('Library') { steps { echo suiteResults() } }
    stage('Publish') { steps {
      recordIssues tools: [esLint(pattern: 'lint.xml')]
      checkout scmGit(branches: [[name: 'main']], extensions: [cleanBeforeCheckout()])
      junit testResults: 'junit.xml'
    } }
  }
}`)
	jobs := workflow.Jobs
	require.Equal(t, []string{"npm run integration"}, jenkinsRuns(jobs["Echo"]))
	require.Equal(t, []string{"npm test"}, jenkinsRuns(jobs["Write"]))
	require.Contains(t, strings.Join(jenkinsRuns(jobs["Library"]), "\n"), "it calls suiteResults, which is not a Pipeline step ddtest knows")
	require.NotContains(t, jobs, "Publish", "plugin symbols run no step")
}

// env['X'] = … and env.setProperty('X', …) set X as env.X = … does; an
// environment variable whose name ddtest cannot tell makes the environment
// unknown after it.
func TestJenkinsIndexedEnvironmentAssignments(t *testing.T) {
	jobs := jenkinsJobs(t, `node {
  stage('Index') {
    env.SUITE = 'unit'
    env['SUITE'] = 'integration'
    sh "npm run ${env.SUITE}"
    env.setProperty('SUITE', 'e2e')
    sh "npm run ${env['SUITE']}"
    env["MODE"] = 'ci'
    sh 'npm run "$MODE"'
  }
  stage('Computed') {
    env[params.NAME] = 'x'
    sh "npm run ${env.SUITE}"
    sh 'npm test'
  }
  stage('After') { sh 'npm test' }
}`)
	runs := jenkinsRuns(jobs["Index"])
	require.Equal(t, []string{"npm run integration", "npm run e2e", `npm run "$MODE"`}, runs)
	require.Equal(t, "ci", jobs["Index"].Steps[2].Env["MODE"])
	computed := jenkinsRuns(jobs["Computed"])
	require.Len(t, computed, 2)
	require.Contains(t, computed[0], "its sh script reads ${env.SUITE}, which env[params.NAME] = 'x' at Jenkinsfile:12 may set")
	require.Contains(t, computed[1], "it runs after env[params.NAME] = 'x' at Jenkinsfile:12, which sets an environment variable ddtest cannot name")
	require.Contains(t, jenkinsRuns(jobs["After"])[0], "it runs after env[params.NAME] = 'x'", "the build's environment stays unknown")

	// Inside a closure that may run any number of times too.
	jobs = jenkinsJobs(t, "node {\n  stage('T') {\n    env.SUITE = 'unit'\n    retry(2) { env['SUITE'] = 'integration' }\n    sh \"npm run ${env.SUITE}\"\n  }\n}\n")
	require.Contains(t, jenkinsRuns(jobs["T"])[0], "its sh script reads ${env.SUITE}")
}

// A for loop's header binds its variable: unknown in the body and after
// the loop; a header ddtest cannot read is unresolved.
func TestJenkinsForLoopHeaders(t *testing.T) {
	jobs := jenkinsJobs(t, `node {
  stage('In') {
    def suite = 'unit'
    for (suite in ['integration']) { sh "npm run ${suite}" }
    sh "npm run ${suite}"
  }
  stage('Classic') {
    for (int i = 0; i < 3; i++) { sh "npm run shard-${i}" }
    sh 'npm test'
  }
  stage('Iterable') {
    for (String name : suites()) { sh 'npm test' }
  }
  stage('Header') {
    for (a, b in pairs) { sh 'npm test' }
  }
}`)
	in := jenkinsRuns(jobs["In"])
	require.Len(t, in, 2)
	require.Contains(t, in[0], "its sh script reads ${suite}, which only the Jenkins build knows")
	require.Contains(t, in[1], "its sh script reads ${suite}", "the loop assigns suite")
	classic := jenkinsRuns(jobs["Classic"])
	require.Contains(t, classic[0], "its sh script reads ${i}")
	require.Equal(t, "npm test", classic[1])
	require.Contains(t, strings.Join(jenkinsRuns(jobs["Iterable"]), "\n"), "it calls suites, which is not a Pipeline step ddtest knows")
	header := jenkinsRuns(jobs["Header"])
	require.Contains(t, header[0], "the Groovy statement (a, b in pairs) is not one ddtest can read")
	require.Equal(t, "npm test", header[1])
}

// A switch runs one case, and falls through until a break: each case is a
// path of its own, and a value only some cases set is unknown after it.
func TestJenkinsSwitchCases(t *testing.T) {
	jobs := jenkinsJobs(t, `node {
  stage('Break') {
    env.SUITE = 'integration'
    switch (params.KIND) {
      case 'unit':
        env.SUITE = 'unit'
        break
      default:
        sh "npm run ${env.SUITE}"
    }
    sh "npm run ${env.SUITE}"
  }
  stage('Fallthrough') {
    switch (params.KIND) {
      case 'a':
        env.MODE = 'a'
      case 'b':
        sh "npm run ${env.MODE}"
        break
      default:
        env.MODE = 'b'
    }
  }
  stage('Nested break') {
    switch (params.KIND) {
      case 'a':
        env.TARGET = 'first'
        if (params.FAST) { break }
        env.TARGET = 'second'
        break
      default:
        env.TARGET = 'second'
    }
    sh "npm run ${env.TARGET}"
  }
  stage('Agree') {
    switch (params.KIND) {
      case 'a':
        env.ALL = 'same'
        break
      default:
        env.ALL = 'same'
    }
    sh "npm run ${env.ALL}"
  }
}`)
	runs := jenkinsRuns(jobs["Break"])
	require.Len(t, runs, 2)
	require.Equal(t, "npm run integration", runs[0], "default starts with the value before the switch")
	require.Contains(t, runs[1], "its sh script reads ${env.SUITE}", "unit or integration")
	require.Contains(t, jenkinsRuns(jobs["Fallthrough"])[0], "its sh script reads ${env.MODE}", "b is reached directly or from a")
	require.Contains(t, jenkinsRuns(jobs["Nested break"])[0], "its sh script reads ${env.TARGET}", "the break may leave the case before second")
	require.Equal(t, []string{"npm run same"}, jenkinsRuns(jobs["Agree"]))
}

// The plugin prepends its preload to the NODE_OPTIONS of the first step it
// configures on an agent: the pipeline's NODE_OPTIONS stays under a
// stage's, which is reported.
func TestJenkinsNodeOptionsUnderAStageOverride(t *testing.T) {
	workflow := jenkinsWorkflow(t, "pipeline {\n  agent any\n  environment { NODE_OPTIONS = '--conditions=ci' }\n"+jenkinsDatadogOption+`  stages {
    stage('Stage') {
      environment { NODE_OPTIONS = '--inspect' }
      steps { sh 'npm test' }
    }
    stage('WithEnv') { steps { withEnv(['NODE_OPTIONS=--trace-warnings']) { sh 'npm test' } } }
    stage('Pipeline') { steps { sh 'npm test' } }
  }
}`)
	for _, name := range []string{"Stage", "WithEnv", "Pipeline"} {
		require.Equal(t, "-r ${{ env.DD_TRACE_PACKAGE }} --conditions=ci", workflow.Jobs[name].Steps[1].Env["NODE_OPTIONS"], name)
	}
	review := strings.Join(workflow.Review, "\n")
	require.Contains(t, review, "NODE_OPTIONS set in the environment directive at Jenkinsfile:9 is overwritten by the Datadog Jenkins plugin: on each agent it sets NODE_OPTIONS for every step to its preload followed by the NODE_OPTIONS of the first step it configures there")
	require.Contains(t, review, "ddtest validates with the pipeline's NODE_OPTIONS")
	require.Contains(t, review, "NODE_OPTIONS set in withEnv at Jenkinsfile:12")
}

// The plugin sets its tracer variables in every step of the option's scope,
// container steps too, so an option whose scope includes a job that runs
// steps in a container is certified for none of its jobs; a container job
// that runs no steps, or one outside a scripted wrap, does not count.
func TestJenkinsPluginScopeWithContainerJobs(t *testing.T) {
	setupProblem := func(source, job string) string {
		t.Helper()
		found := jenkinsJobs(t, source)[job]
		require.NotEmpty(t, found.Steps, job)
		require.NotNil(t, found.Steps[0].Setup, job)
		return found.Steps[0].Setup.Problem
	}
	mixed := "pipeline {\n  agent none\n" + jenkinsDatadogOption + "  stages {\n    stage('Docker') { agent { docker { image 'node:22' } }\n      steps { sh 'npm run package' } }\n    stage('Host') { agent any\n      steps { sh 'npm test' } }\n  }\n}\n"
	problem := setupProblem(mixed, "Host")
	require.Contains(t, problem, "The datadog(testOptimization: …) option at Jenkinsfile:4 also applies to Jenkinsfile / Docker, which run steps in a container.")
	require.Contains(t, problem, "sets its tracer variables (DD_TRACE_PATH, NODE_OPTIONS) in every step of its scope, container steps included, but installs the tracer only on the build agent")
	require.Contains(t, problem, "tests or not")
	require.Contains(t, problem, "run those stages on a host agent, or keep them out of the option's scope")

	split := "pipeline {\n  agent any\n" + jenkinsDatadogOption + "  stages {\n    stage('Test') { steps {\n      sh 'npm ci'\n      script { docker.image('node:22').inside { sh 'npm test' } }\n    } }\n  }\n}\n"
	require.Contains(t, setupProblem(split, "Test"), "also applies to Jenkinsfile / Test (docker.image('node:22').inside)", "a container block inside a host stage counts too")

	noSteps := "pipeline {\n  agent none\n" + jenkinsDatadogOption + "  stages {\n    stage('Docker') { agent { docker { image 'node:22' } }\n      steps { echo 'nothing to run' } }\n    stage('Host') { agent any\n      steps { sh 'npm test' } }\n  }\n}\n"
	require.Empty(t, setupProblem(noSteps, "Host"))

	scripted := "datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVASCRIPT']]) {\n  node {\n    stage('Test') { sh 'npm test' }\n  }\n}\nnode {\n  stage('Package') { docker.image('node:22').inside { sh 'npm pack' } }\n}\n"
	require.Empty(t, setupProblem(scripted, "Test"), "the container block is outside the wrap")

	root := jenkinsRepository(t, mixed)
	require.False(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", ""))
	require.Contains(t, strings.Join(existingSetupProblems(root, "javascript", "jest", ""), "\n"), "Jenkinsfile / Host: The datadog(testOptimization: …) option at Jenkinsfile:4 also applies to Jenkinsfile / Docker")
}

// A version manager that selects a runtime for the rest of its sh, such as
// nvm use 22 before npm test, keeps the runtime the user states for the
// job's host, whose description names the manager: the statement is the
// runtime the manager selects, and one the manager contradicts conflicts.
func TestJenkinsVersionManagersKeepTheStatedRuntime(t *testing.T) {
	confirmed := []string{"jenkins-plugin", "jenkins-plugin-mode=agentless:datadoghq.com", "jenkins-agent-runtime-matches"}
	check := func(t *testing.T, root string) RuntimeCheck {
		t.Helper()
		return checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
			return tracerRequirement{Version: "6.22.0", Node: ">=18"}, nil
		}, "jest")
	}
	unit := func(t *testing.T, result RuntimeCheck) RuntimeFinding {
		t.Helper()
		require.Len(t, result.Jobs, 1, result)
		return result.Jobs[0]
	}
	pipeline := func(steps string) string {
		return "pipeline {\n  agent any\n" + jenkinsDatadogOption + "  stages { stage('Test') { steps {\n" + steps + "\n  } } }\n}\n"
	}

	for _, script := range []string{"nvm use 22\nnpm test", ". \"$NVM_DIR/nvm.sh\"\nnvm use 22 > /dev/null\nnpm test", "nvm install 22\nnpm test", "nvm use\nnpm test", "fnm use 22\nnpm test", "n 22\nnpm test"} {
		t.Run(script, func(t *testing.T) {
			root := jenkinsRepository(t, pipeline("sh '''"+script+"'''"))
			useCIStatements(t, []string{"node=22"}, confirmed...)
			require.NoError(t, CheckCIRuntimeStatements(root))
			result := check(t, root)
			require.Equal(t, StatusCompatible, result.Status, result)
			finding := unit(t, result)
			require.Equal(t, "22", finding.Node)
			require.True(t, finding.UserStated)
		})
	}

	jobs := jenkinsJobs(t, pipeline("sh '''nvm use 20\nnpm test'''"))
	require.Equal(t, []string{"SETUP", "npm test"}, jenkinsRuns(jobs["Test"]), "the selection is the host's, which its description names")
	require.Equal(t, map[string]ciShellRuntime{"js": {Version: "20", Source: "nvm use 20 in the sh script at Jenkinsfile:7"}}, jobs["Test"].Steps[2].ShellRuntime)
	require.Contains(t, jobs["Test"].Host, "with the runtime nvm use 20 (sh script at Jenkinsfile:7) selects on PATH")

	// A version the manager names that the statement contradicts conflicts,
	// stated for every job or for this one.
	root := jenkinsRepository(t, pipeline("sh '''nvm use 20\nnpm test'''"))
	for _, statement := range []string{"node=22", "Test:node=22"} {
		useCIStatements(t, []string{statement}, confirmed...)
		var conflict *CIRuntimeConflict
		require.ErrorAs(t, CheckCIRuntimeStatements(root), &conflict, statement)
		require.Equal(t, []string{"--ci-runtime " + statement + " contradicts Node 20, which nvm use 20 in the sh script at Jenkinsfile:7 selects for the tests in Jenkinsfile / Test"}, conflict.Conflicts)
	}
	// A selection in another sh ends with its shell; one after the tests
	// selects nothing they run on.
	writeFile(t, root, "Jenkinsfile", pipeline("sh 'nvm use 20'\nsh 'npm test'\nsh '''npm test\nnvm use 18\nnpm ci'''"))
	useCIStatements(t, []string{"node=22"}, confirmed...)
	require.NoError(t, CheckCIRuntimeStatements(root))
	require.Equal(t, StatusCompatible, check(t, root).Status)

	// nvm use without a version reads .nvmrc.
	writeFile(t, root, "Jenkinsfile", pipeline("dir('web') { sh '''nvm use\nnpm test''' }"))
	writeFile(t, root, "web/.nvmrc", "v20.11.1\n")
	var conflict *CIRuntimeConflict
	require.ErrorAs(t, CheckCIRuntimeStatements(root), &conflict)
	require.Contains(t, conflict.Conflicts[0], "contradicts Node 20.11.1, which nvm use in the sh script at Jenkinsfile:7 (from web/.nvmrc) selects")
	writeFile(t, root, "web/.nvmrc", "lts/iron\n")
	require.NoError(t, CheckCIRuntimeStatements(root), "an alias is a version ddtest cannot compare")

	// Without a statement, the user is asked for the host's runtime.
	writeFile(t, root, "Jenkinsfile", pipeline("sh '''nvm use 22\nnpm test'''"))
	useCIStatements(t, nil, confirmed...)
	required := RequiredCIRuntimes(t.Context(), root, "javascript", "jest")
	require.NotNil(t, required)
	require.Equal(t, []string{"Jenkinsfile / Test"}, required.Jobs)

	// A command that also runs something is no selection: nvm exec runs the
	// tests, and nvm use 22 && npm test is one command the shared resolver
	// does not read (as in any provider's run step).
	jobs = jenkinsJobs(t, pipeline("sh '''nvm exec 22 npm test\nnvm use 22 && npm test\nnvm use $(cat .version)\nnpm test'''"))
	require.Equal(t, []string{"SETUP", "nvm exec 22 npm test", "nvm use 22 && npm test", "nvm use $(cat .version)", "npm test"}, jenkinsRuns(jobs["Test"]))
	require.Empty(t, jobs["Test"].Steps[4].ShellRuntime)
}

// Ruby's version managers keep the stated Ruby the same way.
func TestJenkinsRubyVersionManagersKeepTheStatedRuntime(t *testing.T) {
	root := copyFixture(t, "jenkins-ruby")
	t.Chdir(root)
	useCIProvider(t, "jenkins")
	pipeline := func(script string) string {
		return "datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['RUBY']]) {\n  node {\n    stage('Test') {\n      sh '''" + script + "'''\n    }\n  }\n}\n"
	}
	useCIStatements(t, []string{"ruby=3.3"}, "jenkins-plugin", "jenkins-plugin-mode=agentless:datadoghq.com", "jenkins-agent-runtime-matches")
	for _, script := range []string{"rbenv shell 3.3\nbundle exec rake spec", "rvm use 3.3.6 --default\nbundle exec rake spec", "eval \"$(rbenv init -)\"\nbundle exec rake spec", "source ~/.rvm/scripts/rvm\nchruby ruby-3.3\nbundle exec rake spec"} {
		writeFile(t, root, "Jenkinsfile", pipeline(script))
		require.NoError(t, CheckCIRuntimeStatements(root), script)
		jobs := RubyCIConfigurationForSite(root, "rspec", "datadoghq.com").Jobs
		require.Len(t, jobs, 1, script)
		require.Equal(t, "3.3", jobs[0].Runtime, script)
		require.True(t, jobs[0].UserStated, script)
	}
	writeFile(t, root, "Jenkinsfile", pipeline("rvm use 3.2\nbundle exec rake spec"))
	var conflict *CIRuntimeConflict
	require.ErrorAs(t, CheckCIRuntimeStatements(root), &conflict)
	require.Equal(t, []string{"--ci-runtime ruby=3.3 contradicts Ruby 3.2, which rvm use 3.2 in the sh script at Jenkinsfile:4 selects for the tests in Jenkinsfile / Test"}, conflict.Conflicts)
	// rbenv reads .ruby-version.
	writeFile(t, root, "Jenkinsfile", pipeline("eval \"$(rbenv init -)\"\nbundle exec rake spec"))
	writeFile(t, root, ".ruby-version", "ruby-3.2.4\n")
	require.ErrorAs(t, CheckCIRuntimeStatements(root), &conflict)
	require.Contains(t, conflict.Conflicts[0], "contradicts Ruby 3.2.4, which eval \"$(rbenv init -)\" in the sh script at Jenkinsfile:4 (from .ruby-version) selects")
	// rvm use … do runs the tests: no selection.
	writeFile(t, root, "Jenkinsfile", pipeline("rvm use 3.2 do bundle exec rake spec"))
	require.NoError(t, CheckCIRuntimeStatements(root))
}

// Expansions are bounded before they are built: a matrix's product, the
// cells of all of a Jenkinsfile's matrices, and a value that interpolates
// others.
func TestJenkinsExpansionsAreBounded(t *testing.T) {
	values := func(n int) string {
		var items []string
		for i := range n {
			items = append(items, "'"+strconv.Itoa(i)+"'")
		}
		return strings.Join(items, ", ")
	}
	matrix := func(name, axes, stages string) string {
		return "stage('" + name + "') {\n  matrix {\n    axes {\n" + axes + "    }\n    stages {\n" + stages + "\n    }\n  }\n}\n"
	}
	huge := "pipeline {\n  agent any\n  stages {\n" + matrix("M", "      axis { name 'A'; values "+values(200)+" }\n      axis { name 'B'; values "+values(1000)+" }\n", "stage('t') { steps { sh 'npm test' } }") + "  }\n}\n"
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	workflow, err := readJenkinsfile(t.TempDir(), "Jenkinsfile", huge)
	runtime.ReadMemStats(&after)
	require.NoError(t, err)
	require.Equal(t, []string{"UNRESOLVED Jenkinsfile M: ddtest cannot expand its matrix (it has more than 200 cells), so it cannot tell which stages it runs"}, jenkinsRuns(workflow.Jobs["M"]))
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(8<<20), "200,000 cells are never built")

	// Nested matrices multiply: past the Jenkinsfile's budget, each is
	// unresolved.
	inner := matrix("Inner", "      axis { name 'B'; values "+values(10)+" }\n", "stage('t') { steps { sh 'npm test' } }")
	nested := jenkinsJobs(t, "pipeline {\n  agent any\n  stages {\n"+matrix("Outer", "      axis { name 'A'; values "+values(200)+" }\n", inner)+"  }\n}\n")
	var tests, unresolved int
	for _, job := range nested {
		for _, run := range jenkinsRuns(job) {
			switch {
			case run == "npm test":
				tests++
			case strings.Contains(run, "with the Jenkinsfile's other matrices, it has more than 1000 cells"):
				unresolved++
			}
		}
	}
	require.Equal(t, jenkinsMatrixBudget-200, tests, "the outer cells and the inner ones within the budget")
	require.Equal(t, 200-(jenkinsMatrixBudget-200)/10, unresolved)

	// A string that doubles a variable at each step stops at the limit.
	doubling := "def v0 = 'xxxxxxxxxxxxxxxx'\n"
	for i := 1; i <= 30; i++ {
		doubling += "def v" + strconv.Itoa(i) + " = \"${v" + strconv.Itoa(i-1) + "}${v" + strconv.Itoa(i-1) + "}\"\n"
	}
	doubling += "node { stage('T') { sh \"echo ${v30}\"\nsh 'npm test' } }\n"
	runtime.GC()
	runtime.ReadMemStats(&before)
	jobs := jenkinsJobs(t, doubling)
	runtime.ReadMemStats(&after)
	runs := jenkinsRuns(jobs["T"])
	require.Len(t, runs, 2)
	require.Contains(t, runs[0], "UNRESOLVED Jenkinsfile T: its sh script reads ${v30}, which only the Jenkins build knows")
	require.Equal(t, "npm test", runs[1])
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(16<<20), "a 16 GiB string is never built")

	// Nested stages repeat their names in the jobs inside: a long one is
	// named by its line.
	jobs = jenkinsJobs(t, "def name = '"+strings.Repeat("x", jenkinsNameLimit+1)+"'\nnode {\n  stage(name) { stage('Unit') { sh 'npm test' } }\n}\n")
	require.Contains(t, jobs, "stage at line 3 / Unit")
	axes := ""
	for i := range jenkinsAxisLimit + 1 {
		axes += "      axis { name 'A" + strconv.Itoa(i) + "'; values '1' }\n"
	}
	jobs = jenkinsJobs(t, "pipeline {\n  agent any\n  stages {\n"+matrix("Wide", axes, "stage('t') { steps { sh 'npm test' } }")+"  }\n}\n")
	require.Contains(t, jenkinsRuns(jobs["Wide"])[0], "it has more than 32 axes")
}

// Expressions run the steps they call: an assertion's, a ternary's
// branches, and a GString's interpolations. Syntax ddtest cannot read that
// calls a step is unresolved, never dropped.
func TestJenkinsStepsInExpressions(t *testing.T) {
	jobs := jenkinsJobs(t, `pipeline {
  agent any
  stages {
    stage('Assert') { steps { script {
      assert sh(script: 'npm test', returnStatus: true) == 0
      assert true : sh(script: 'npm run explain', returnStdout: true)
    } } }
    stage('Ternary') { steps { script {
      def result = params.FAST ? sh('npm test') : sh('npm test -- --runInBand')
      params.E2E ? sh('npm run e2e') : echo('skipped')
    } } }
    stage('Ternary scope') { steps { script {
      def mode = params.FAST ? 'fast' : 'full'
      sh "npm run test:${mode}"
    } } }
    stage('Interpolation') { steps {
      echo "result: ${sh(script: 'npm test', returnStdout: true)}"
      sh "echo ${sh(script: 'npm run unit', returnStdout: true).trim()}"
      dir(sh(script: 'npm run where', returnStdout: true).trim()) { sh 'npm run inside' }
    } }
    stage('Opaque') { steps { script {
      throw new Exception(sh(script: 'npm test', returnStdout: true))
      def file = new File(runSuite())
      def pattern = /${sh(script: 'npm test', returnStdout: true)}/
      echo "${a; sh('npm test')}"
    } } }
    stage('Values') { steps { script {
      def year = "${new Date().format('yyyy')}"
      def file = new File(pwd(), 'out.txt')
      echo "${env.BRANCH_NAME == 'main' ? 'release' : 'snapshot'} ${params.X?.trim()}"
    } } }
  }
}`)
	require.Equal(t, []string{"npm test", "npm run explain"}, jenkinsRuns(jobs["Assert"]))
	require.Equal(t, []string{"npm test", "npm test -- --runInBand", "npm run e2e"}, jenkinsRuns(jobs["Ternary"]))
	require.Contains(t, jenkinsRuns(jobs["Ternary scope"])[0], "its sh script reads ${mode}, which only the Jenkins build knows", "a branch-dependent value is unknown after it")
	interpolation := jenkinsRuns(jobs["Interpolation"])
	require.Equal(t, []string{"npm test", "npm run unit"}, interpolation[:2])
	require.Contains(t, interpolation[2], "UNRESOLVED")
	require.Equal(t, "npm run where", interpolation[3])
	require.Contains(t, interpolation[4], "it runs inside dir(sh(script: 'npm run where', returnStdout: true).trim()), whose directory ddtest cannot resolve")
	opaque := jenkinsRuns(jobs["Opaque"])
	require.Len(t, opaque, 4)
	require.Contains(t, opaque[0], "the Groovy expression new Exception(sh(script: 'npm test', returnStdout: true)) is not one ddtest can read, and it calls sh, which may run tests")
	require.Contains(t, opaque[1], "it calls runSuite")
	require.Contains(t, opaque[2], "the Groovy expression /${sh(script: 'npm test', returnStdout: true)}/ is not one ddtest can read, and it calls sh")
	require.Contains(t, opaque[3], "the Groovy expression a; sh('npm test') is not one ddtest can read, and it calls sh")
	require.NotContains(t, jobs, "Values", "constructors and value methods run no step")
}
