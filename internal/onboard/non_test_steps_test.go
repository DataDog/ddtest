// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Recognized tools that can't start the selected framework, and the ways each
// still may: a Dockerfile RUN, an Angular target naming the framework, a
// publish lifecycle script, a backgrounded command or a find -exec.
func TestRecognizedToolsStartTestsOnlyThroughWhatTheyRun(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"scripts":{"test":"vitest run","prepare":"husky"}}`)
	writeFile(t, root, "Dockerfile", "# syntax=docker/dockerfile:1\nFROM node:22\nRUN --mount=type=cache,target=/root/.npm npm ci --omit=dev \\\n  && rm -rf /tmp/*\n")
	writeFile(t, root, "test/Dockerfile", "FROM node:22\nRUN npm ci && npx vitest run\n")
	writeFile(t, root, "custom/Dockerfile", "# syntax=example.com/frontend\nFROM node:22\n")
	writeFile(t, root, "tabs/Dockerfile", "FROM node:22\nRUN\t--mount=type=cache,target=/root/.npm\tnpm test\n")
	writeFile(t, root, "ci/ruff", "#!/bin/sh\nnpm test\n")
	writeFile(t, root, "ci/eslint", "#!/bin/sh\neslint .\n")
	writeFile(t, root, "angular.json", `{"projects":{"app":{"architect":{"test":{"builder":"@angular-devkit/build-angular:karma"},"lint":{"builder":"@angular-eslint/builder:lint"},"unit":{"builder":"@angular/build:unit-test"},"custom":{"builder":"@acme/builders:test"}}}}}`)
	writeFile(t, root, "bin.js", "")
	for run, want := range map[string]bool{
		"docker build -t app .":                        false,
		"docker buildx build --push -t app .":          false,
		"docker build -f test/Dockerfile .":            true,
		"docker build -f custom/Dockerfile .":          true,
		"docker build -f tabs/Dockerfile .":            true,
		"./ci/ruff check .":                            true,
		"ci/ruff check .":                              true,
		"./ci/eslint":                                  false,
		"npm publish --workspaces":                     true,
		"npm publish ./missing":                        true,
		"npm publish build.tgz":                        true,
		"docker build -t app $CONTEXT":                 true,
		"docker build --unknown-option value .":        true,
		"docker push app && docker logout":             false,
		"docker run node:22 npm test":                  true,
		"docker compose run tests":                     true,
		"sonar-scanner -Dsonar.login=$SONAR_TOKEN":     false,
		"npx --yes sonarqube-scanner":                  false,
		"ng lint":                                      false,
		"ng test --watch=false":                        false,
		"ng run app:unit":                              true,
		"ng run app:custom":                            true,
		"ng e2e":                                       true,
		"npx @acme/publish":                            true,
		"npx @acme/deploy":                             true,
		"npx release":                                  true,
		"npx gh-pages -d dist":                         false,
		"npm publish --access public":                  false,
		"npx np":                                       true,
		"python -m pip install .":                      false,
		"python3 -Im build":                            false,
		"python setup.py sdist":                        true,
		"coverage report":                              false,
		"coverage run -m vitest":                       true,
		"bundle exec rubocop --parallel":               false,
		"gem push pkg/x.gem":                           false,
		"bundle exec rubocop & npx vitest":             true,
		"find . -name '*.js' -exec node bin.js {} \\;": false,
		"find . -name '*.js' -exec npm test \\;":       true,
		"npm view pkg version 2>&1 | grep -q 1.0":      false,
	} {
		require.Equal(t, want, mayStartTestsIn(root, root, runtimeStep{Run: run}, "vitest", 0), run)
	}
	// The unit-test builder runs Karma when the target selects it.
	writeFile(t, root, "angular.json", `{"projects":{"app":{"architect":{"unit":{"builder":"@angular/build:unit-test","options":{"runner":"karma"}}}}}}`)
	require.False(t, mayStartTestsIn(root, root, runtimeStep{Run: "ng run app:unit"}, "vitest", 0))
	// A publish lifecycle script that runs the tests keeps a publication in scope,
	// and so does semantic-release's exec plugin.
	writeFile(t, root, "package.json", `{"scripts":{"test":"vitest run","prepublishOnly":"npm test"}}`)
	require.True(t, mayStartTestsIn(root, root, runtimeStep{Run: "npx gh-pages -d dist"}, "vitest", 0))
	require.True(t, mayStartTestsIn(root, root, runtimeStep{Run: "npm publish"}, "vitest", 0))
	writeFile(t, root, "package.json", `{"scripts":{"test":"vitest run"}}`)
	// So does one in the package a publication names.
	writeFile(t, root, "build/package.json", `{"scripts":{"prepublishOnly":"npx vitest run"}}`)
	writeFile(t, root, "docs/package.json", `{"scripts":{"prepack":"echo docs"}}`)
	require.True(t, mayStartTestsIn(root, root, runtimeStep{Run: "npm publish ./build --tag next"}, "vitest", 0))
	require.True(t, mentionsFramework(root, root, "npm publish build", "vitest", 0))
	require.False(t, mayStartTestsIn(root, root, runtimeStep{Run: "npm publish ./docs --access public"}, "vitest", 0))
	require.False(t, mentionsFramework(root, root, "npm publish docs", "vitest", 0))
	writeFile(t, root, ".releaserc.json", `{"plugins":[["@semantic-release/exec",{"prepareCmd":"npm test"}]]}`)
	require.True(t, mayStartTestsIn(root, root, runtimeStep{Run: "npx semantic-release"}, "vitest", 0))
	// Without the package, its publish scripts can't be read.
	require.True(t, mayStartTests(runtimeStep{Run: "npx gh-pages -d dist"}, "vitest"))
}

// A repository Node script is followed through its local imports: a test of
// its own that neither names the framework nor starts a process can't start
// it; a framework import, a spawned runner, a computed command, an aliased
// import or a worker may.
func TestRepositoryNodeScriptsStartTheFrameworkOnlyThroughWhatTheyLoad(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"scripts":{"test":"vitest run"}}`)
	writeFile(t, root, "test/integration/check.integration.js", "// Run after the Vitest suite.\nimport assert from 'node:assert'\nimport { client } from '../vitest/helpers/client.js'\nconst match = /x/.exec('x')\nassert.ok(client() && match)\n")
	writeFile(t, root, "test/vitest/helpers/client.js", "/**\n * Shared with the Vitest tests.\n */\nexport function client() { return true }\n")
	writeFile(t, root, "scripts/spawn.js", "const { spawnSync } = require('child_process')\nspawnSync('npx', ['vitest', 'run'])\n")
	writeFile(t, root, "scripts/computed.mjs", "import { execSync } from 'node:child_process'\nexecSync(process.argv[2])\n")
	writeFile(t, root, "scripts/api.mjs", "import { startVitest } from 'vitest/node'\nawait startVitest('test')\n")
	writeFile(t, root, "scripts/alias.ts", "import { run } from '@/scripts/run'\nrun()\n")
	writeFile(t, root, "scripts/worker.js", "const { Worker } = require('worker_threads')\nnew Worker('./w.js')\n")
	writeFile(t, root, "scripts/build.sh", "#!/bin/sh\nset -e\nnode test/integration/check.integration.js\ntar czf dist.tgz dist\n")
	writeFile(t, root, "scripts/ci.sh", "#!/bin/sh\nnpm test\n")
	writeFile(t, root, "scripts/tool.py", "print('x')\n")
	writeFile(t, root, "scripts/npm-test.js", "const { spawnSync } = require('node:child_process')\nspawnSync('npm', ['test'], { stdio: 'inherit' })\n")
	writeFile(t, root, "scripts/npm-args.js", "const { spawnSync } = require('node:child_process')\nspawnSync('npm', process.argv.slice(2))\n")
	writeFile(t, root, "scripts/npm-cwd.js", "const { execSync } = require('node:child_process')\nexecSync('npm run build', { cwd: 'packages/app' })\n")
	writeFile(t, root, "scripts/npm-view.js", "const { execFileSync, exec } = require('node:child_process')\nexecFileSync('npm', ['view', 'pkg', 'version'], { encoding: 'utf8' })\nexec('git describe', (error) => {})\n")
	writeFile(t, root, "scripts/wrapper.mjs", "import { run } from '@acme/ci'\nrun()\n")
	writeFile(t, root, "scripts/upload.mjs", "import fs from 'node:fs'\nimport path from 'path'\nimport { glob } from 'glob'\nimport type { Options } from '@acme/types'\nfs.copyFileSync(path.join('a'), 'b')\n")
	for run, want := range map[string]bool{
		"node test/integration/check.integration.js": false,
		"node scripts/spawn.js":                      true,
		"node scripts/computed.mjs lint":             true,
		"node scripts/api.mjs":                       true,
		"tsx scripts/alias.ts":                       true,
		"node scripts/worker.js":                     true,
		"node --require ./setup.js scripts/x.js":     true,
		"bash -e scripts/build.sh":                   false,
		"./scripts/build.sh":                         false,
		"sh scripts/ci.sh":                           true,
		"./scripts/tool.py":                          true,
		"node scripts/missing.js":                    true,
		"node scripts/npm-test.js":                   true,
		"node scripts/npm-args.js":                   true,
		"node scripts/npm-cwd.js":                    true,
		"node scripts/npm-view.js":                   false,
		"node scripts/wrapper.mjs":                   true,
		"node scripts/upload.mjs":                    false,
	} {
		require.Equal(t, want, mayStartTestsIn(root, root, runtimeStep{Run: run}, "vitest", 0), run)
	}
	// The test-like rules for Mocha preparation and outside jobs stay, and
	// read packages and process arguments the same way.
	require.True(t, nodeScriptMayRun(root, root, filepath.Join(root, "test/integration/check.integration.js"), "vitest", map[string]bool{}))
	require.True(t, nodeScriptMayRun(root, root, filepath.Join(root, "scripts/wrapper.mjs"), "vitest", map[string]bool{}))
	require.True(t, nodeScriptMayRun(root, root, filepath.Join(root, "scripts/npm-test.js"), "vitest", map[string]bool{}))
	require.False(t, nodeScriptMayRun(root, root, filepath.Join(root, "scripts/upload.mjs"), "vitest", map[string]bool{}))
}

// A Node script's processes inherit the folder it was invoked from, not its
// own: npm run checks in tools/release.js runs the root package's checks
// when CI runs the script from the root, and tools' own when CI runs it
// from tools.
func TestNodeScriptProcessesRunWhereTheScriptWasInvoked(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"scripts":{"test":"vitest run","checks":"vitest run --project checks"}}`)
	writeFile(t, root, "tools/package.json", `{"scripts":{"checks":"echo ok"}}`)
	writeFile(t, root, "tools/release.js", "const { spawnSync } = require('node:child_process')\nspawnSync('npm', ['run', 'checks'], { stdio: 'inherit' })\n")
	require.True(t, mayStartTestsIn(root, root, runtimeStep{Run: "node tools/release.js"}, "vitest", 0))
	require.False(t, mayStartTestsIn(root, filepath.Join(root, "tools"), runtimeStep{Run: "node release.js"}, "vitest", 0))
	require.True(t, nodeScriptMayRun(root, root, filepath.Join(root, "tools/release.js"), "vitest", map[string]bool{}))
	require.False(t, nodeScriptMayRun(root, filepath.Join(root, "tools"), filepath.Join(root, "tools/release.js"), "vitest", map[string]bool{}))
}

// A Node script's project modules are followed with the same rules, however
// TypeScript and Node name them; only a module ddtest can't find stays a
// possible test path. Accepted libraries start no runner, unless a use of
// theirs loads or spawns modules ddtest doesn't read.
func TestNodeScriptsFollowProjectModulesAndAcceptKnownLibraries(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"scripts":{"test":"vitest run"}}`)
	writeFile(t, root, "utils/upload_chrome.js", "const fs = require('fs')\nconst { default: fetch } = require('node-fetch')\nconst axios = require('axios')\nconst archiver = require('archiver')\nconst AdmZip = require('adm-zip')\nfetch('https://example.com', { body: fs.readFileSync(process.argv[2]) })\n")
	writeFile(t, root, "test/integration/fetch.integration.mjs", "import http from 'http'\nimport { createClient } from '../../services/client.js'\nimport config from '../../config.json' with { type: 'json' }\ncreateClient(config)\n")
	writeFile(t, root, "services/client.ts", "import { parse } from './url'\nimport type { Options } from '@acme/types'\nexport const createClient = (options: Options) => parse(options)\n")
	writeFile(t, root, "services/url/index.ts", "import URI from 'urijs'\nimport chalk from 'chalk'\nexport const parse = (value: string) => new URI(value)\n")
	writeFile(t, root, "config.json", `{"vitest": true}`)
	writeFile(t, root, "scripts/deep.mjs", "import { run } from './lib/run.mjs'\nrun()\n")
	writeFile(t, root, "scripts/lib/run.mjs", "import { run } from '@acme/ci'\nexport { run }\n")
	writeFile(t, root, "scripts/missing-module.mjs", "import { run } from './lib/missing.mjs'\nrun()\n")
	writeFile(t, root, "scripts/cli.js", "const { program } = require('commander')\nprogram.command('upload <file>').action(() => {})\nprogram.parse()\n")
	writeFile(t, root, "scripts/subcommands.js", "const { program } = require('commander')\nprogram.command('check', 'runs the checks')\nprogram.parse()\n")
	writeFile(t, root, "scripts/commands.js", "require('yargs').commandDir('commands').parse()\n")
	writeFile(t, root, "scripts/chdir.js", "const { execSync } = require('child_process')\nprocess.chdir('packages/app')\nexecSync('npm run build')\n")
	writeFile(t, root, "scripts/execa.mjs", "import { execa } from 'execa'\nawait execa('git', ['status'])\n")
	for run, want := range map[string]bool{
		"node utils/upload_chrome.js chrome.zip":      false,
		"node test/integration/fetch.integration.mjs": false,
		"node scripts/deep.mjs":                       true,
		"node scripts/missing-module.mjs":             true,
		"node scripts/cli.js upload x":                false,
		"node scripts/subcommands.js check":           true,
		"node scripts/commands.js":                    true,
		"node scripts/chdir.js":                       true,
		"node scripts/execa.mjs":                      true,
	} {
		require.Equal(t, want, mayStartTestsIn(root, root, runtimeStep{Run: run}, "vitest", 0), run)
	}
	require.False(t, nodeScriptMayRun(root, root, filepath.Join(root, "utils/upload_chrome.js"), "vitest", map[string]bool{}))
	require.True(t, nodeScriptMayRun(root, root, filepath.Join(root, "scripts/deep.mjs"), "vitest", map[string]bool{}))
}

// A job that runs no test ddtest can find is outside this validation even
// when its scripts use packages ddtest doesn't read, or it runs a package
// merely named publish: its caveat names them, and its earlier steps don't
// become preparation of tests. In a job that also runs tests they block, and
// so do the repository's own packages, a package runner other than a
// publication and names that suggest tests.
func TestUnreadPackagesKeepOnlyTestJobsInScope(t *testing.T) {
	root := newJestRepository(t, `jobs:
  firefox:
    steps:
      - run: cp config.example.json config.json
      - run: git archive --format=zip -o source.zip HEAD
      - run: npm run upload:firefox firefox.zip source.zip
  publish:
    steps:
      - run: npx @acme/publish
  wrapper:
    steps:
      - run: node scripts/wrapper.mjs
  test:
    steps:
      - run: node scripts/wrapper.mjs
      - run: npm test
  release:
    steps:
      - run: npm test
      - run: npx @acme/publish
  workspace:
    steps:
      - run: node scripts/workspace.mjs
  runner:
    steps:
      - run: npx @acme/deploy --run-tests
  named:
    steps:
      - run: node scripts/named.mjs
`)
	writeFile(t, root, "package.json", `{"scripts":{"test":"jest","upload:firefox":"node utils/upload_firefox.js"},"devDependencies":{"jest":"30.0.0"},"workspaces":["packages/*"]}`)
	writeFile(t, root, "packages/ci/package.json", `{"name":"@acme/ci-local"}`)
	writeFile(t, root, "utils/upload_firefox.js", "const fs = require('fs')\nconst nJwt = require('njwt')\nconsole.log(nJwt.create({}, fs.readFileSync('key')))\n")
	writeFile(t, root, "scripts/wrapper.mjs", "import { run } from '@acme/ci'\nrun()\n")
	writeFile(t, root, "scripts/workspace.mjs", "import { run } from '@acme/ci-local'\nrun()\n")
	writeFile(t, root, "scripts/named.mjs", "import { run } from '@acme/test-runner'\nrun()\n")
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	unresolved := strings.Join(scope.Unresolved, "\n")
	caveats := strings.Join(scope.Caveats, "\n")
	require.NotContains(t, unresolved, "firefox /")
	require.NotContains(t, unresolved, "publish /")
	require.NotContains(t, unresolved, "wrapper /")
	require.Contains(t, caveats, "firefox: CI job without the Datadog setup that runs no jest test ddtest can find (it uses packages ddtest doesn't read: njwt); it is outside this validation")
	require.Contains(t, caveats, "publish: CI job without the Datadog setup that runs no jest test ddtest can find (it uses packages ddtest doesn't read: @acme/publish)")
	require.Contains(t, caveats, "wrapper: CI job without the Datadog setup that runs no jest test ddtest can find (it uses packages ddtest doesn't read: @acme/ci)")
	require.NotContains(t, caveats, "Runs before the job's")
	require.Contains(t, unresolved, "test / step 1 (node scripts/wrapper.mjs)")
	require.Contains(t, unresolved, "release / step 2 (npx @acme/publish)")
	require.Contains(t, unresolved, "workspace / step 1 (node scripts/workspace.mjs)")
	require.Contains(t, unresolved, "runner / step 1 (npx @acme/deploy --run-tests)")
	require.Contains(t, unresolved, "named / step 1 (node scripts/named.mjs)")
	// A file: dependency is the repository's own package too.
	writeFile(t, root, "package.json", `{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.0.0","@acme/ci":"file:./tools/ci"}}`)
	require.True(t, repositoryPackage(root, root, "@acme/ci"))
	require.False(t, repositoryPackage(root, root, "njwt"))
	// So is any package when a workspace pattern can't be expanded.
	writeFile(t, root, "pnpm-workspace.yaml", "packages:\n  - 'packages/**'\n")
	require.True(t, repositoryPackage(root, root, "njwt"))
}

// A step that can't start the selected framework is outside validation after
// the job's last test or in a job without tests: a caveat, never a blocker.
// Before the tests it may still change them, so it blocks and says why.
func TestNonTestStepsAreCaveatsOutsideTheTestPath(t *testing.T) {
	root := newJestRepository(t, `jobs:
  test:
    steps:
      - run: npm ci
      - run: node scripts/generate.sh.js
      - run: npm test
      - run: npm run build
      - run: docker build -t app .
  image:
    steps:
      - run: docker build -t app .
      - run: docker push app
  publish:
    steps:
      - run: npx gh-pages -d dist
  hidden:
    steps:
      - run: npm test
      - run: node scripts/spawn.js
`)
	writeScripts(t, root, map[string]string{"test": "jest", "build": "node scripts/build.js"})
	writeFile(t, root, "scripts/build.js", "require('fs').writeFileSync('dist.js', '')\n")
	writeFile(t, root, "scripts/generate.sh.js", "require('fs').writeFileSync('fixtures.json', '{}')\nrequire('child_process').execSync('git describe')\n")
	writeFile(t, root, "scripts/spawn.js", "require('child_process').execSync('npx jest --ci')\n")
	writeFile(t, root, "Dockerfile", "FROM node:22\nCOPY . .\nRUN npm ci --omit=dev\n")
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	unresolved := strings.Join(scope.Unresolved, "\n")
	caveats := strings.Join(scope.Caveats, "\n")
	require.Contains(t, caveats, ".github/workflows/test.yml / test: step 4 (npm run build), step 5 (docker build -t app .) run after the job's last jest test and can't start jest; outside this validation.")
	require.Contains(t, caveats, ".github/workflows/test.yml / image: CI job without the Datadog setup that never runs jest")
	require.Contains(t, caveats, ".github/workflows/test.yml / publish: CI job without the Datadog setup that never runs jest")
	require.NotContains(t, unresolved, "docker")
	require.NotContains(t, unresolved, "publish")
	// A script that spawns Jest after the tests is a possible test: it blocks.
	require.Contains(t, unresolved, "hidden / step 2 (node scripts/spawn.js)")
	// A generator before the tests can't start one but may change them.
	require.Contains(t, unresolved, "test / step 2 (node scripts/generate.sh.js): Runs before the job's jest tests and may change what they run, which ddtest can't replay")
}

// The same classification holds on every provider: an image or publish job
// is a caveat, a script that spawns the framework still blocks.
func TestNonTestJobsAreCaveatsOnEveryProvider(t *testing.T) {
	for provider, config := range map[string]struct{ path, text string }{
		"gitlab":    {".gitlab-ci.yml", "test:\n  image: node:22\n  script:\n    - npm test\nimage:\n  image: docker:27\n  script:\n    - docker build -t app .\n    - docker push app\npublish:\n  image: node:22\n  script:\n    - npx gh-pages -d dist\nhidden:\n  image: node:22\n  script:\n    - node scripts/spawn.js\n"},
		"circleci":  {circleCIFile, "version: 2.1\njobs:\n  test:\n    docker:\n      - image: cimg/node:22.11\n    steps:\n      - run: npm test\n  image:\n    docker:\n      - image: cimg/node:22.11\n    steps:\n      - run: docker build -t app .\n  publish:\n    docker:\n      - image: cimg/node:22.11\n    steps:\n      - run: npx gh-pages -d dist\n  hidden:\n    docker:\n      - image: cimg/node:22.11\n    steps:\n      - run: node scripts/spawn.js\n" + circleWorkflow("      - test\n      - image\n      - publish\n      - hidden\n")},
		"buildkite": {buildkiteFile, "steps:\n  - label: test\n    command: npm test\n  - label: image\n    command: docker build -t app .\n  - label: publish\n    command: npx gh-pages -d dist\n  - label: hidden\n    command: node scripts/spawn.js\n"},
		"jenkins":   {"Jenkinsfile", "pipeline {\n  agent any\n  stages {\n    stage('Test') { steps { sh 'npm test' } }\n    stage('Image') { steps { sh 'docker build -t app .' } }\n    stage('Publish') { steps { sh 'npx gh-pages -d dist' } }\n    stage('Hidden') { steps { sh 'node scripts/spawn.js' } }\n  }\n}\n"},
	} {
		t.Run(provider, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "package.json", `{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.0.0"}}`)
			writeFile(t, root, "Dockerfile", "FROM node:22\nRUN npm ci --omit=dev\n")
			writeFile(t, root, "scripts/spawn.js", "require('child_process').execSync('npx jest --ci')\n")
			writeFile(t, root, config.path, config.text)
			useCIProvider(t, provider)
			scope, err := DiscoverValidationScope(root, "jest")
			require.NoError(t, err)
			unresolved := strings.Join(scope.Unresolved, "\n")
			require.NotContains(t, unresolved, "docker")
			require.NotContains(t, unresolved, "gh-pages")
			require.Contains(t, unresolved, "node scripts/spawn.js")
			require.Len(t, scope.Commands, 1)
			caveats := strings.Join(scope.Caveats, "\n")
			require.Contains(t, caveats, "never runs jest; it is outside this validation")
			// Jenkins' plugin reaches every stage, so ddtest can't say its tests go unreported.
			require.Equal(t, provider != "jenkins", strings.Contains(caveats, "not reported to Datadog"), caveats)
		})
	}
}

// Commands after the tests in the same CI step that can't start the
// framework are named as work outside this validation; one that a later test
// follows, in the step, the script calling it or a later step, may prepare it
// and stays unresolved.
func TestCommandsAfterAStepsTestsAreOutsideIt(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - run: npm run test:ci\n  between:\n    steps:\n      - run: npm run between\n  nested:\n    steps:\n      - run: npm run nested\n  later:\n    steps:\n      - run: npm test && node test/generate.js\n      - run: npx vitest run --project b\n")
	writeScripts(t, root, map[string]string{
		"test": "vitest run", "test:ci": "npm run lint && npm test && npm run test:integration", "lint": "eslint .",
		"test:integration": "node test/integration.js", "between": "npm test && node test/integration.js && vitest run --project b",
		"nested": "npm run test:ci && vitest run --project b",
	})
	writeFile(t, root, "test/integration.js", "import assert from 'node:assert'\nassert.ok(true)\n")
	writeFile(t, root, "test/generate.js", "require('node:fs').writeFileSync('test/fixture.json', '{}')\n")
	scope, err := DiscoverValidationScope(root, "vitest")
	require.NoError(t, err)
	var notes []string
	for _, entry := range scope.Commands {
		notes = append(notes, entry.StepScopeNotes...)
	}
	require.Contains(t, strings.Join(notes, "\n"), "This CI step also runs npm run test:integration after its vitest tests")
	unresolved := strings.Join(scope.Unresolved, "\n")
	require.NotContains(t, unresolved, "test / step 1 (npm run test:ci)")
	require.Contains(t, unresolved, "between / step 1 (npm run between)", "a later test in the step may read what the script wrote")
	// Accepted inside a script, reopened when the calling script tests again.
	require.Contains(t, unresolved, "nested / step 1 (npm run nested): npm run nested -> npm run test:integration -> Cannot statically resolve command: node test/integration.js")
	// A later step's test may read what the generator wrote.
	require.Contains(t, unresolved, "later / step 1 (npm test && node test/generate.js): Runs after this step's vitest tests but before a later vitest test step, which it may prepare")
	require.NotContains(t, strings.Join(notes, "\n"), "node test/generate.js")
}

// The last test is the row's own: on a matrix row whose condition skips a
// later test step, commands after the row's last test are outside it; on a
// row that runs the later step, they may prepare it and stay unresolved.
func TestLastTestStepIsComputedPerMatrixRow(t *testing.T) {
	root := newJestRepository(t, `jobs:
  test:
    strategy:
      matrix:
        suite: [unit, e2e]
    steps:
      - run: npm ci
      - run: npm test && node scripts/generate.js
      - run: node scripts/generate.js
      - if: matrix.suite == 'e2e'
        run: npx jest --selectProjects e2e
`)
	writeScripts(t, root, map[string]string{"test": "jest"})
	writeFile(t, root, "scripts/generate.js", "require('node:fs').writeFileSync('fixture.json', '{}')\n")
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	unresolved := strings.Join(scope.Unresolved, "\n")
	require.NotContains(t, unresolved, `suite: "unit"`)
	require.Contains(t, unresolved, `test / step 2 (npm test && node scripts/generate.js): Runs after this step's jest tests but before a later jest test step, which it may prepare: Cannot statically resolve command: node scripts/generate.js [affects 1 matrix entries; e.g. suite: "e2e"]`)
	require.Contains(t, unresolved, `test / step 3 (node scripts/generate.js): Runs before the job's jest tests and may change what they run, which ddtest can't replay: Cannot statically resolve command: node scripts/generate.js [affects 1 matrix entries; e.g. suite: "e2e"]`)
	require.Contains(t, strings.Join(scope.Caveats, "\n"), "step 3 (node scripts/generate.js) runs after the job's last jest test and can't start jest")
}

// A local script awaiting setup facts after the job's last test can't
// prepare it: when it can't start the framework, it is a caveat.
func TestPostTestScriptsNeedNoSetupFacts(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"scripts":{"test":"vitest run","build":"node build.js"},"devDependencies":{"vitest":"4.1.6"}}`)
	writeFile(t, root, "build.js", "import { build } from 'esbuild'\nawait build({ entryPoints: ['index.js'] })\n")
	writeFile(t, root, circleCIFile, "version: 2.1\njobs:\n  test-build:\n    docker:\n      - image: cimg/node:22.14\n    steps:\n      - run: npm install\n      - run: npm test\n      - run: npm run build\n"+circleWorkflow("      - test-build\n"))
	useCIProvider(t, "circleci")
	scope, err := DiscoverWorkflowScope(root, "vitest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Contains(t, strings.Join(scope.Caveats, "\n"), "step 3 (npm run build) runs after the job's last vitest test and can't start vitest")
	// One that spawns the framework still needs a decision.
	writeFile(t, root, "build.js", "require('child_process').execSync('npx vitest run --project build')\n")
	scope, err = DiscoverWorkflowScope(root, "vitest")
	require.NoError(t, err)
	require.NotEmpty(t, scope.Unresolved)
}

// Another package's job for another framework (an Angular app's Karma and
// lint targets next to a Jest backend) is outside the Jest validation.
func TestOtherPackagesAngularJobIsOutsideJest(t *testing.T) {
	root := newJestRepository(t, `jobs:
  backend:
    defaults:
      run:
        working-directory: server
    steps:
      - run: npm ci
      - run: npm test
  frontend:
    defaults:
      run:
        working-directory: ui
    steps:
      - run: npm ci
      - run: npm run lint
      - uses: browser-actions/setup-chrome@v1
        id: chrome
      - env:
          CHROME_BIN: ${{ steps.chrome.outputs.chrome-path }}
        run: npm test -- --watch=false
`)
	writeFile(t, root, "server/package.json", `{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.0.0"}}`)
	writeFile(t, root, "ui/package.json", `{"scripts":{"test":"ng test","lint":"ng lint"}}`)
	writeFile(t, root, "ui/angular.json", `{"projects":{"ui":{"architect":{"test":{"builder":"@angular-devkit/build-angular:karma"},"lint":{"builder":"@angular-eslint/builder:lint"}}}}}`)
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Contains(t, strings.Join(scope.Caveats, "\n"), "frontend: CI job without the Datadog setup that never runs jest")
	// An Angular target that runs Jest is in scope.
	writeFile(t, root, "ui/angular.json", `{"projects":{"ui":{"architect":{"test":{"builder":"@angular-builders/jest:run"},"lint":{"builder":"@angular-eslint/builder:lint"}}}}}`)
	scope, err = DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Contains(t, strings.Join(scope.Unresolved, "\n"), "ng test")
}

// Rails database tasks prepare the test database; they can't start tests.
// A repository task file that hooks the framework's task into them may.
func TestRailsDatabaseTasksArePreparedLocally(t *testing.T) {
	root := rubyWorkflowFixture(t, "jobs:\n  test:\n    steps:\n      - run: bin/rails db:reset\n      - run: RAILS_ENV=test bundle exec rails db:create db:schema:load\n      - run: bundle exec rake db:setup\n      - run: bundle exec rspec\n")
	scope, err := DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	review := strings.Join(scope.Review, "\n")
	require.Contains(t, review, "step 1 (bin/rails db:reset): Rails database setup (db:reset) prepares the test database; it can't start rspec. CI's database setup is not replayed: prepare the same test database locally before validation.")
	require.Contains(t, review, "Rails database setup (db:create db:schema:load)")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "lib", "tasks"), 0o755))
	writeFile(t, root, "lib/tasks/db.rake", "Rake::Task['db:setup'].enhance { Rake::Task['spec'].invoke }\n")
	scope, err = DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Contains(t, strings.Join(scope.Unresolved, "\n"), "lib/tasks/db.rake extends database tasks or seeds and names rspec")
}

// A Ruby test step may run lint, audit and gem release tools around the
// test; autocorrection changes the code the tests run, so it stays unresolved.
func TestRubyStepsWithLintAroundTheTests(t *testing.T) {
	root := t.TempDir()
	for run, outside := range map[string]bool{
		"bundle exec rubocop && bundle exec rspec":               true,
		"bundle exec rspec\nbundle exec rubocop --parallel":      true,
		"bundle exec brakeman -q && bundle exec rspec spec/unit": true,
		"bundle exec rspec && gem push pkg/x.gem":                true,
		"bundle exec rubocop -a && bundle exec rspec":            false,
		"bundle exec rspec\nbundle exec rubocop --autocorrect":   false,
		"bundle exec rspec && echo setup":                        false,
		"bundle exec rubocop & bundle exec rspec":                false,
	} {
		resolution := resolveRubyStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: run}, "rspec")
		if outside {
			require.True(t, resolution.Matched, run)
			require.Empty(t, resolution.Reason, run)
			require.Len(t, resolution.Tests, 1, run)
			require.Contains(t, strings.Join(resolution.Tests[0].StepScopeNotes, "\n"), "it can't start rspec and is outside this validation", run)
		} else {
			require.NotEmpty(t, resolution.Reason, run)
		}
	}
}

// pytest jobs follow the same rules: packaging, upload and image steps after
// the tests are caveats, a lint-only job is outside, and a wheel install
// before the tests still blocks.
func TestPytestJobsKeepOnlyTheirTestPath(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pyproject.toml", "[project]\nname = \"x\"\nversion = \"1\"\n")
	writeFile(t, root, "tests/test_x.py", "def test_x():\n    pass\n")
	writeFile(t, root, "Dockerfile", "FROM python:3.12\nCOPY dist/*.whl /tmp/\nRUN pip install /tmp/*.whl\n")
	writeFile(t, root, ".github/workflows/ci.yml", `jobs:
  test:
    steps:
      - uses: actions/setup-python@v5
        with:
          python-version: '3.12'
      - run: pip install -e .
      - run: pytest
      - run: python -m build
      - run: twine upload dist/*
      - run: docker build -t x .
  wheel:
    steps:
      - uses: actions/setup-python@v5
        with:
          python-version: '3.12'
      - run: pip install dist/*.whl
      - run: pytest
`)
	scope, err := DiscoverValidationScope(root, "pytest")
	require.NoError(t, err)
	// python -m build and twine upload dist/* are non-test tools within the
	// step's own resolution, reported for review rather than as caveats.
	require.Contains(t, strings.Join(scope.Caveats, "\n"), ".github/workflows/ci.yml / test: step 6 (docker build -t x .) runs after the job's last pytest test and can't start pytest; outside this validation.")
	require.NotContains(t, strings.Join(scope.Caveats, "\n"), "twine")
	unresolved := strings.Join(scope.Unresolved, "\n")
	require.NotContains(t, unresolved, "twine")
	require.Contains(t, unresolved, "wheel / step 2 (pip install dist/*.whl): Runs before the job's pytest tests and may change what they run")
}

// Documentation builders run project code: Sphinx executes conf.py (and
// the extensions it loads), MkDocs its hooks, so after the tests they may
// still start pytest and stay unresolved, unlike a linter.
func TestDocumentationBuildersMayStartPytest(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pyproject.toml", "[project]\nname = \"x\"\nversion = \"1\"\n")
	writeFile(t, root, "tests/test_x.py", "def test_x():\n    pass\n")
	writeFile(t, root, "docs/conf.py", "import subprocess\nsubprocess.run(['pytest', 'tests'])\n")
	for run, want := range map[string]bool{
		"sphinx-build -b html docs docs/_build": true,
		"python -m sphinx -b html docs out":     true,
		"python3 -Im sphinx docs out":           true,
		"python -m mkdocs build":                true,
		"ruff check .":                          false,
		"python -m ruff check .":                false,
	} {
		require.Equal(t, want, mayStartTestsIn(root, root, runtimeStep{Run: run}, "pytest", 0), run)
	}
	writeFile(t, root, ".github/workflows/ci.yml", `jobs:
  test:
    steps:
      - uses: actions/setup-python@v5
        with:
          python-version: '3.12'
      - run: pip install -e .
      - run: pytest
      - run: sphinx-build -b html docs docs/_build
      - run: python -m sphinx -b html docs out
`)
	scope, err := DiscoverValidationScope(root, "pytest")
	require.NoError(t, err)
	unresolved := strings.Join(scope.Unresolved, "\n")
	require.Contains(t, unresolved, "test / step 4 (sphinx-build -b html docs docs/_build)")
	require.Contains(t, unresolved, "test / step 5 (python -m sphinx -b html docs out)")
	require.NotContains(t, strings.Join(scope.Caveats, "\n"), "sphinx")
}

// A separate documentation job never runs pytest, so its Sphinx or MkDocs
// build can't affect the validated tests: like any job without a pytest
// environment it stays outside validation, named for review, on GitHub and
// GitLab alike. In the test job, before the tests, the same build may start
// or prepare them and blocks.
func TestSeparateDocumentationJobsStayOutsidePytestValidation(t *testing.T) {
	for provider, config := range map[string]struct{ path, text, job string }{
		"github": {".github/workflows/ci.yml", `jobs:
  test:
    steps:
      - uses: actions/setup-python@v5
        with:
          python-version: '3.12'
      - run: pip install -e .
      - run: pytest
  docs:
    steps:
      - uses: actions/setup-python@v5
        with:
          python-version: '3.12'
      - run: pip install -e .[docs] && sphinx-build docs docs/_build
      - run: python -m mkdocs build
`, "docs"},
		"gitlab": {".gitlab-ci.yml", "test:\n  image: python:3.12\n  script:\n    - pip install -e .\n    - pytest\npages:\n  image: python:3.12\n  script:\n    - pip install -e .[docs]\n    - mkdocs build\n    - sphinx-build docs public\n", "pages"},
	} {
		t.Run(provider, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "pyproject.toml", "[project]\nname = \"x\"\nversion = \"1\"\n")
			writeFile(t, root, "tests/test_x.py", "def test_x():\n    pass\n")
			writeFile(t, root, "docs/conf.py", "project = 'x'\n")
			writeFile(t, root, config.path, config.text)
			useCIProvider(t, provider)
			scope, err := DiscoverValidationScope(root, "pytest")
			require.NoError(t, err)
			require.Empty(t, scope.Unresolved)
			require.Len(t, scope.Commands, 1)
			require.Contains(t, strings.Join(scope.Review, "\n"), config.path+" / "+config.job+": no pytest environment identified")
		})
	}
	// Before the tests in the same job, the build blocks.
	root := t.TempDir()
	writeFile(t, root, "pyproject.toml", "[project]\nname = \"x\"\nversion = \"1\"\n")
	writeFile(t, root, "tests/test_x.py", "def test_x():\n    pass\n")
	writeFile(t, root, ".github/workflows/ci.yml", `jobs:
  test:
    steps:
      - uses: actions/setup-python@v5
        with:
          python-version: '3.12'
      - run: pip install -e .
      - run: sphinx-build docs docs/_build
      - run: pytest
`)
	scope, err := DiscoverValidationScope(root, "pytest")
	require.NoError(t, err)
	require.Contains(t, strings.Join(scope.Unresolved, "\n"), "test / step 3 (sphinx-build docs docs/_build)")
}

// Scope results tell the agent to report the blocker and its options, never
// to edit CI until ddtest accepts it.
func TestScopeDecisionWordingForbidsUnprescribedCIEdits(t *testing.T) {
	require.Contains(t, ScopeBlockerInstruction, "Do not edit CI configuration (steps, conditions, variables, error handling such as continue-on-error, or whitespace) to make ddtest accept a step unless a result prescribes that exact edit")
	require.Contains(t, AgentScope, "retry variants of a CI edit until ddtest accepts one")
	require.Contains(t, pytestInstructions, "Do not edit CI configuration (steps, conditions, variables or")
}

// WebdriverIO validation runs the whole package alias, so a command after the
// tests inside it would run locally: it stays unresolved. After the alias, in
// the CI step, it is outside the test path.
func TestCommandsAfterTestsInsideAFullAliasStayUnresolved(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - run: npm test\n      - run: npm run suite && node report.js\n")
	writeFile(t, root, "report.js", "require('node:fs').writeFileSync('report.txt','done')\n")
	writeFile(t, root, "wdio.conf.cjs", "exports.config={framework:'mocha',specs:['./test/*.js']}\n")
	data, err := json.Marshal(map[string]any{"scripts": map[string]string{"test": "wdio run wdio.conf.cjs && node report.js", "suite": "wdio run wdio.conf.cjs"}, "devDependencies": map[string]string{"@wdio/cli": "9.20.0"}})
	require.NoError(t, err)
	writeFile(t, root, "package.json", string(data))
	scope, err := DiscoverValidationScope(root, "webdriverio")
	require.NoError(t, err)
	unresolved := strings.Join(scope.Unresolved, "\n")
	require.Contains(t, unresolved, "step 1 (npm test)", "the alias validation executes runs node report.js")
	require.Contains(t, unresolved, "which validation executes whole")
	require.NotContains(t, unresolved, "step 2", "node report.js runs after the alias, outside the test path")
	var notes []string
	for _, entry := range scope.Commands {
		notes = append(notes, entry.StepScopeNotes...)
	}
	require.Contains(t, strings.Join(notes, "\n"), "This CI step also runs node report.js after its webdriverio tests")
}

// semantic-release runs its plugins: only official plugins that run no
// project command, and @semantic-release/npm through the lifecycle scripts of
// the package it publishes, can't start the tests. A configuration selected
// on the command line, a shareable or JavaScript configuration and any other
// plugin may.
func TestSemanticReleaseStartsTestsThroughItsPlugins(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"scripts":{"test":"vitest run"}}`)
	writeFile(t, root, "release-tests.cjs", "module.exports = { plugins: [['@semantic-release/exec', { prepareCmd: 'npm test' }]] }\n")
	writeFile(t, root, "dist/package.json", `{"scripts":{"prepublishOnly":"npx vitest run"}}`)
	run := func(command string) bool {
		return mayStartTestsIn(root, root, runtimeStep{Run: command}, "vitest", 0)
	}
	// No configuration: the default plugins publish the package.
	require.False(t, run("npx semantic-release --dry-run --branches main"))
	require.True(t, run("npx semantic-release --extends ./release-tests.cjs"))
	require.True(t, run("npx semantic-release -e ./release-tests.cjs"))
	require.True(t, run("npx semantic-release --plugins ./release/plugin.js"))
	for configuration, want := range map[string]bool{
		`{"branches":["main"],"plugins":["@semantic-release/commit-analyzer","@semantic-release/release-notes-generator",["@semantic-release/changelog",{"changelogFile":"CHANGELOG.md"}],"@semantic-release/npm","@semantic-release/git","@semantic-release/github"]}`: false,
		`{"plugins":["@semantic-release/commit-analyzer","./release/run-tests.js"]}`:     true,
		`{"plugins":["@semantic-release/commit-analyzer","semantic-release-slack-bot"]}`: true,
		`{"plugins":[["@semantic-release/exec",{"prepareCmd":"npm test"}]]}`:             true,
		`{"extends":"@acme/release-config"}`:                                             true,
		`{"plugins":[["@semantic-release/npm",{"pkgRoot":"dist"}]]}`:                     true,
		`{"plugins":[["@semantic-release/npm",{"pkgRoot":"missing"}]]}`:                  true,
		`{"plugins":"@semantic-release/npm"}`:                                            true,
	} {
		writeFile(t, root, ".releaserc.json", configuration)
		require.Equal(t, want, run("npx semantic-release"), configuration)
	}
	require.NoError(t, os.Remove(filepath.Join(root, ".releaserc.json")))
	// YAML, package.json and an ancestor's configuration are read; a
	// JavaScript one is not.
	writeFile(t, root, ".releaserc.yml", "plugins:\n  - '@semantic-release/commit-analyzer'\n  - - '@semantic-release/exec'\n    - prepareCmd: npm test\n")
	require.True(t, run("npx semantic-release"))
	require.NoError(t, os.Remove(filepath.Join(root, ".releaserc.yml")))
	writeFile(t, root, "package.json", `{"scripts":{"test":"vitest run"},"release":{"plugins":["./release/run-tests.js"]}}`)
	require.True(t, run("npx semantic-release"))
	writeFile(t, root, "docs/package.json", `{"name":"docs"}`)
	require.True(t, mayStartTestsIn(root, filepath.Join(root, "docs"), runtimeStep{Run: "npx semantic-release"}, "vitest", 0))
	writeFile(t, root, "package.json", `{"scripts":{"test":"vitest run","version":"npm test"}}`)
	require.True(t, run("npx semantic-release"), "@semantic-release/npm runs npm version")
	writeFile(t, root, "package.json", `{"scripts":{"test":"vitest run"}}`)
	writeFile(t, root, "release.config.js", "module.exports = { plugins: [] }\n")
	require.True(t, run("npx semantic-release"))
}

// Building a Python project runs its build backend: a setup.py, an in-tree
// backend, an unknown backend or a known backend's hooks may start tests.
// Installing published packages or wheels builds nothing of the project.
func TestPythonBuildsMayRunTheProjectsBackend(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pyproject.toml", "[build-system]\nrequires = [\"hatchling\"]\nbuild-backend = \"hatchling.build\"\n\n[project]\nname = \"x\"\nversion = \"1\"\ndependencies = [\"attrs\"]\n")
	writeFile(t, root, "requirements.txt", "# Runtime\nrequests==2.32.3 --hash=sha256:abc\n-r dev.txt\n")
	writeFile(t, root, "dev.txt", "-i https://pypi.org/simple\nruff\n")
	writeFile(t, root, "editable.txt", "-e .\n")
	writeFile(t, root, "nested/requirements.txt", "-r ../editable.txt\n")
	writeFile(t, root, "custom/pyproject.toml", "[build-system]\nrequires = []\nbuild-backend = \"backend\"\nbackend-path = [\".\"]\n")
	writeFile(t, root, "legacy/setup.py", "import subprocess\nsubprocess.run(['pytest'])\n")
	writeFile(t, root, "hooked/pyproject.toml", "[build-system]\nbuild-backend = \"hatchling.build\"\n\n[tool.hatch.build.hooks.custom]\n")
	writeFile(t, root, "unknown/pyproject.toml", "[build-system]\nbuild-backend = \"acme.backend\"\n")
	writeFile(t, root, "flit/pyproject.toml", "[build-system]\nbuild-backend = \"flit_core.buildapi\"\n\n[project]\nname = \"x\"\ndynamic = [\"version\"]\n")
	writeFile(t, root, "poetry/pyproject.toml", "[build-system]\nbuild-backend = \"poetry.core.masonry.api\"\n\n[tool.poetry]\nname = \"x\"\nbuild = \"build.py\"\n")
	writeFile(t, root, "setuptools/pyproject.toml", "[project]\nname = \"x\"\ndynamic = [\"version\"]\n\n[tool.setuptools.dynamic]\nversion = {attr = \"x.__version__\"}\n")
	for run, want := range map[string]bool{
		"python -m build":                        false,
		"python -m build --sdist --outdir dist":  false,
		"pyproject-build":                        false,
		"python -m pip install .":                false,
		"pip install -e '.[test]'":               false,
		"pip install -r requirements.txt":        false,
		"pip install --upgrade pip build twine":  false,
		"pip install dist/*.whl":                 false,
		"pip install https://example.com/x.tgz":  false,
		"pip3 freeze":                            false,
		"pip --version":                          false,
		"check-manifest":                         false,
		"pip install -r editable.txt":            false,
		"pip install -r nested/requirements.txt": false,
		"twine upload dist/*":                    false,
		"python -m build custom":                 true,
		"python -m pip install ./legacy":         true,
		"pip install -e legacy":                  true,
		"pip install ./hooked":                   true,
		"pip wheel ./unknown":                    true,
		"pip install ./flit":                     true,
		"pip install ./poetry":                   true,
		"pip install ./setuptools":               true,
		"pip install -r missing.txt":             true,
		"pip install dist/x-1.tar.gz":            true,
		"pip install $PACKAGE":                   true,
		"pip install --unknown-option value":     true,
		"python -m build --unknown":              true,
		"check-manifest legacy":                  true,
	} {
		require.Equal(t, want, mayStartTestsIn(root, root, runtimeStep{Run: run}, "pytest", 0), run)
	}
	// The project's own build hook may, and so may requirements that name it.
	writeFile(t, root, "hatch_build.py", "import subprocess\nsubprocess.run(['pytest'])\n")
	for _, run := range []string{"python -m build", "pip install .", "pip install -r editable.txt", "pip install -r nested/requirements.txt", "check-manifest"} {
		require.True(t, mayStartTestsIn(root, root, runtimeStep{Run: run}, "pytest", 0), run)
	}
	// Without a readable directory, a local build may.
	require.True(t, mayStartTests(runtimeStep{Run: "pip install ."}, "pytest"))
	require.False(t, mayStartTests(runtimeStep{Run: "pip install requests"}, "pytest"))
}

// A shell wrapper's commands read the folder a cd moves them to: a plain cd
// to a repository folder first is followed, and any other directory change
// leaves later commands' scripts unread, so they may start the tests.
func TestShellWrappersFollowDirectoryChanges(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"scripts":{"test":"echo see packages"}}`)
	writeFile(t, root, "packages/extra/package.json", `{"scripts":{"test":"vitest run"}}`)
	writeFile(t, root, "packages/docs/package.json", `{"scripts":{"test":"echo docs","build":"echo build"}}`)
	for script, want := range map[string]bool{
		"cd packages/extra && npm test":                      true,
		"cd packages/docs && npm test":                       false,
		"cd packages/missing && npm test":                    true,
		"cd \"$PACKAGE\" && npm test":                        true,
		"echo start\ncd packages/docs\nnpm test":             true,
		"(cd packages/docs && npm run build) && npm test":    true,
		"pushd packages/docs && npm test":                    true,
		"[ -d packages/docs ] && cd packages/docs; npm test": true,
		"npm test": false,
	} {
		writeFile(t, root, "scripts/ci.sh", "#!/bin/sh\n"+script+"\n")
		require.Equal(t, want, mayStartTestsIn(root, root, runtimeStep{Run: "./scripts/ci.sh"}, "vitest", 0), script)
		require.Equal(t, want, mayStartTestsIn(root, root, runtimeStep{Run: script}, "vitest", 0), script)
	}
}

// Official checkout, setup, cache and artifact actions can't start tests, at
// a tag or a commit SHA: a job of them and an unread publish package runs no
// test and gets the caveat. Any other action keeps its job in scope.
func TestSetupOnlyActionsKeepAJobOutsideTheTests(t *testing.T) {
	root := newJestRepository(t, `jobs:
  publish:
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@39370e3970a6d050c480ffad4ff0ed4d3fdee5af
        with:
          node-version: 22
      - uses: actions/cache/restore@v4
      - run: npx @acme/deploy
      - uses: actions/upload-artifact@v4
  unknown:
    steps:
      - uses: actions/checkout@v4
      - uses: acme/deploy-action@v1
      - run: npx @acme/deploy
  unpinned:
    steps:
      - uses: actions/checkout
      - run: npx @acme/deploy
  test:
    steps:
      - uses: actions/checkout@v4
      - run: npm test
`)
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	unresolved := strings.Join(scope.Unresolved, "\n")
	caveats := strings.Join(scope.Caveats, "\n")
	require.NotContains(t, unresolved, "publish /")
	require.Contains(t, caveats, "publish: CI job without the Datadog setup that runs no jest test ddtest can find (it uses packages ddtest doesn't read: @acme/deploy)")
	require.Contains(t, unresolved, "unknown / ", "a third-party action may run anything")
	require.NotContains(t, caveats, "unknown:")
	require.Contains(t, unresolved, "unpinned / ", "an action without a ref is not recognized")
	for uses, want := range map[string]bool{
		"actions/checkout@v4": true, "Actions/Checkout@v4": true, "actions/checkout@0ad4b8fadaa221de15dcec353f45205ec38ea70b": true,
		"actions/download-artifact@v4": true, "ruby/setup-ruby@v1": true, "actions/cache/save@v4": true,
		"actions/checkout": false, "actions/checkout@": false, "acme/checkout@v4": false, "./.github/actions/setup": false,
		"docker://node:22": false, "cypress-io/github-action@v6": false, "actions/github-script@v7": false,
	} {
		require.Equal(t, want, setupOnlyAction(uses), uses)
	}
}

// lux-design-system's styleguide job (CircleCI) builds the documentation with
// vue-cli-plugin-styleguidist: a Vue CLI plugin command is a published
// package ddtest doesn't read, so that job is a caveat naming it, while the
// same command still blocks in a job that runs the tests. Vue CLI's own build,
// serve and lint commands run none; test:unit and test:e2e may.
func TestVueCLIPluginCommandsAreUnreadPackages(t *testing.T) {
	useCIProvider(t, "circleci")
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"scripts":{"test":"playwright test","styleguide:build":"npm run design_tokens:docs:build && vue-cli-service styleguidist:build","design_tokens:docs:build":"node src/utils/generateDesignTokenDocs.mjs","e2e":"vue-cli-service test:e2e","bundle":"vue-cli-service build --target lib"},"devDependencies":{"@playwright/test":"1.62.1","@vue/cli-service":"5.0.8","vue-cli-plugin-styleguidist":"~4.72.4"}}`)
	writeFile(t, root, "src/utils/generateDesignTokenDocs.mjs", "import { open } from \"node:fs/promises\"\nconst docs = await open(\"docs/design_tokens.md\", \"w\")\nawait docs.close()\n")
	job := func(name, command string) string {
		return "  " + name + ":\n    docker:\n      - image: cimg/node:24.19.0\n    steps:\n      - checkout\n      - run:\n          command: " + command + "\n"
	}
	writeFile(t, root, circleCIFile, "version: 2.1\njobs:\n"+
		job("playwright", "npx playwright test")+
		job("styleguide", "npm run styleguide:build")+
		job("bundle", "npm run bundle")+
		job("e2e", "npm run e2e")+
		"  docs_then_tests:\n    docker:\n      - image: cimg/node:24.19.0\n    steps:\n      - run: npm run styleguide:build\n      - run: npx playwright test\n"+
		circleWorkflow("      - playwright\n      - styleguide\n      - bundle\n      - e2e\n      - docs_then_tests\n"))
	scope, err := DiscoverValidationScope(root, "playwright")
	require.NoError(t, err)
	unresolved := strings.Join(scope.Unresolved, "\n")
	caveats := strings.Join(scope.Caveats, "\n")
	require.NotContains(t, unresolved, "/styleguide")
	require.Contains(t, caveats, "styleguide: CI job without the Datadog setup that runs no playwright test ddtest can find (it uses packages ddtest doesn't read: vue-cli-service styleguidist:build)")
	require.NotContains(t, unresolved, "/bundle")
	require.Contains(t, caveats, "bundle: CI job without the Datadog setup that never runs playwright")
	require.Contains(t, unresolved, "/e2e")
	require.Contains(t, unresolved, "docs_then_tests / step 1 (npm run styleguide:build)")
}

// Report converters read the results a test run left and start no test
// runner, in any language's CI: a conversion after the tests is outside
// them. A scoped package of the same name, or an unknown converter, may.
func TestReportConvertersCannotStartTests(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"scripts":{"test":"cucumber-js"}}`)
	for run, want := range map[string]bool{
		"cat report.json | ./node_modules/.bin/cucumber-junit > junit.xml":            false,
		"npx cucumber-junit@1.7.1 < report.json > junit.xml":                          false,
		"npx junit-merge -d reports -o junit.xml":                                     false,
		"npx jrm junit.xml 'reports/*.xml'":                                           false,
		"npx mochawesome-merge reports/*.json > report.json && npx marge report.json": false,
		"npx xunit-viewer -r reports -o report.html":                                  false,
		"allure generate allure-results --clean":                                      false,
		"tail -n 20 report.json | tee summary.txt":                                    false,
		"npx @acme/cucumber-junit < report.json":                                      true,
		"cat report.json | ./node_modules/.bin/report-tool > junit.xml":               true,
	} {
		require.Equal(t, want, mayStartTestsIn(root, root, runtimeStep{Run: run}, "cucumber", 0), run)
	}
	require.False(t, javaShellMayRunTests(root, root, "cat target/report.json | cucumber-junit > junit.xml\njunit2html junit.xml report.html\n", nil, false, 0))
	require.True(t, javaShellMayRunTests(root, root, "cat target/report.json | report-tool > junit.xml\n", nil, false, 0))
}

// A GitLab job that converts the Cucumber report in a later script line, as
// onboarding prescribes for a report pipe, keeps that line outside the
// tests: it is a caveat, not unresolved scope.
func TestPostTestReportConversionIsOutsideTheTests(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"devDependencies":{"@cucumber/cucumber":"11.0.1","nyc":"17.1.0","cucumber-junit":"1.7.1"}}`)
	writeFile(t, root, gitLabCIFile, "acceptance:\n  image: node:24\n  script:\n    - npm install\n    - ./node_modules/.bin/cucumber-js ./features --format json:report.json\n    - cat report.json | ./node_modules/.bin/cucumber-junit > junit.xml\n")
	useCIProvider(t, gitLabCIProvider)
	scope, err := DiscoverValidationScope(root, "cucumber")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Contains(t, strings.Join(scope.Caveats, "\n"), "step 3 (cat report.json | ./node_modules/.bin/cucumber-junit > junit.xml) runs after the job's last cucumber test and can't start cucumber")
}
