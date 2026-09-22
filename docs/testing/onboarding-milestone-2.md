# Milestone 2 validation — 2026-09-22

All nine supported frameworks completed the public `onboard → testdrive --yes →
report.html` flow on independent open-source repositories. Both commands exited
zero for every row below. Each run received real tracer events, retained decoded
JSON traffic and test output, and generated a local HTML report.

This was a manually selected open-source sample, not a statistically random
compatibility study. Browser and RSpec checks used representative subsets as
identified below. These results do not imply every version or configuration of
each framework is compatible.

## Environment and checks

- macOS arm64; Go 1.26.5; Node.js 24.14.0; Python 3.11; Ruby 3.4.7.
- Pinned tracers: `dd-trace@6.15.0`, `ddtrace==4.15.1`, `datadog-ci@1.39.0`.
- `make test` and `make lint` pass.
- The opt-in public CLI matrix runs real fixtures for all nine frameworks,
  including paths containing spaces for JavaScript/Python, existing Ruby
  lockfiles, a Cucumber Background, and preserved Cypress task/after-run hooks.
- Existing instrumented Jest and concurrent-session isolation checks pass.
- Root manifests and lockfiles were hashed immediately before and after each
  open-source run, including presence/absence. All nine snapshots were unchanged.
  Dependency installation and application builds happened before those snapshots.
- No Datadog credentials or Agent were used. This validates local instrumentation
  and generated onboarding guidance, not a connection to a live Datadog account.

## Open-source results

“Covered” means a test had associated coverage in received traffic. Zero means
coverage was not reported, not that the test covered no application code. Events
can exceed logical tests because the local intake enables retries.

| Framework | Repository / exact commit | Scope | Logical tests | Events | Covered |
| --- | --- | --- | ---: | ---: | ---: |
| jest | [typestack/class-validator](https://github.com/typestack/class-validator/tree/2e1a5c27dbd65b80e27fe96b49bd6e6641fa3603) | Full Jest suite | 743 | 1549 | 743 |
| mocha | [omichelsen/compare-versions](https://github.com/omichelsen/compare-versions/tree/98e81116ef4197b42dca8c3fde8d1e8166e2a81f) | Full Mocha suite | 336 | 678 | 0 |
| vitest | [unjs/destr](https://github.com/unjs/destr/tree/541b6f9aeada9fc30de9c5a7e086dbfc1c6fcdc7) | Full Vitest suite (lint omitted) | 22 | 44 | 22 |
| playwright | [dropbox/ttvc](https://github.com/dropbox/ttvc/tree/239481cdecb841f63f0b3f3e13be52275333b18d) | One Chromium browser test | 1 | 2 | 0 |
| cypress | [cypress-io/cypress-example-kitchensink](https://github.com/cypress-io/cypress-example-kitchensink/tree/ddaaa92080b68d71d7a1797b4ed20ada18ed2a2a) | Six Todo browser tests | 6 | 12 | 0 |
| cucumber | [cloudevents/sdk-javascript](https://github.com/cloudevents/sdk-javascript/tree/bf5d53f2862248d72d9869cf5173f51b32583faf) | HTTP/Kafka conformance suite | 7 | 14 | 0 |
| pytest | [pallets/itsdangerous](https://github.com/pallets/itsdangerous/tree/672971d66a2ef9f85151e53283113f33d642dabd) | Full pytest suite | 297 | 297 | 297 |
| rspec | [ruby-concurrency/concurrent-ruby](https://github.com/ruby-concurrency/concurrent-ruby/tree/e674fb2688206bb7cb66dee108e1d08184413afb) | AtomicBoolean spec file | 25 | 25 | 25 |
| minitest | [ruby-i18n/i18n](https://github.com/ruby-i18n/i18n/tree/547917dd8d41fab781a81880f22687fc4eac5d85) | Full Minitest suite | 1607 | 1607 | 1607 |

## Reproduction

Build DDTest with Go 1.26.5 and put the resulting binary on PATH. Check out each
linked commit and install that project's dependencies first. Use Node.js 22+ for
the JavaScript tracer and activate the project's Python environment for pytest.
The Ruby checks use Ruby 3.4.7 with native extension build tools.

Repository preparation used:

- class-validator and compare-versions: `npm ci --no-audit --no-fund`.
- destr: `pnpm install --frozen-lockfile` (pnpm 10); the run selects Vitest directly.
- ttvc: `npm install --no-audit --no-fund`, `npm run build`, and
  `node_modules/.bin/playwright install chromium`. The existing Playwright config
  starts its application through `yarn express`. The npm preparation updated its
  Yarn lockfile; the testdrive itself left the prepared dependency files unchanged.
- Cypress Kitchen Sink: `npm ci --no-audit --no-fund`, then `npm start` on port 8080.
  Browser installation is part of project preparation, not a testdrive operation.
- CloudEvents: `git submodule update --init --depth 1`, `npm ci --no-audit --no-fund`,
  `npm run build:schema`, then `npm run build:src`. Its conformance submodule was
  `eddc279339609ed92d128bcd2b0d5c558a7ce396`.
- itsdangerous: create/activate a Python 3.11 venv and run
  `python -m pip install -e . pytest freezegun`.
- concurrent-ruby and i18n: their original Gemfiles were evaluated by the isolated
  bundle; no project bundle or manifest changes were needed.

Run `ddtest onboard --framework FRAMEWORK` in each repository, followed by:

**typestack/class-validator**

```sh
ddtest testdrive --framework jest --yes
```

**omichelsen/compare-versions**

```sh
ddtest testdrive --framework mocha --yes
```

**unjs/destr**

```sh
ddtest testdrive --framework vitest --yes --command 'node_modules/.bin/vitest run'
```

**dropbox/ttvc**

```sh
ddtest testdrive --framework playwright --yes --command 'node_modules/.bin/playwright test test/e2e/stylesheet1 --project=chromium --workers=1'
```

**cypress-io/cypress-example-kitchensink**

```sh
ddtest testdrive --framework cypress --yes --command 'node_modules/.bin/cypress run --spec cypress/e2e/1-getting-started/todo.cy.js'
```

**cloudevents/sdk-javascript**

```sh
ddtest testdrive --framework cucumber --yes --command 'npm run conformance'
```

**pallets/itsdangerous**

```sh
ddtest testdrive --framework pytest --yes
```

**ruby-concurrency/concurrent-ruby**

```sh
ddtest testdrive --framework rspec --yes --command 'bundle exec rspec spec/concurrent/atomic/atomic_boolean_spec.rb'
```

**ruby-i18n/i18n**

```sh
ddtest testdrive --framework minitest --yes
```

Each run prints an absolute `Open report:` link. The report, `test-output.txt`,
and decoded `intake/*.json` files remain under its unique
`.testoptimization/testdrive/<session>/` directory.

Run the reproducible fixture matrix from DDTest itself:

```sh
GOTOOLCHAIN=go1.26.5 make test
GOTOOLCHAIN=go1.26.5 make lint
GOTOOLCHAIN=go1.26.5 \
  DDTEST_RUN_FRAMEWORK_INTEGRATION_TEST=1 \
  DDTEST_RUN_NPM_INTEGRATION_TEST=1 \
  go test ./internal/testdrive \
    -run 'TestPublicFrameworkTestdrives|TestInstrumentedJestFixture' \
    -count=1 -v
```

The integration matrix installs project dependencies in temporary directories,
including Cypress's browser. Python, pip/venv, Ruby, Bundler, Node, npm, and native
gem build tools must already be installed.

## Problems found and addressed

- Python's `uv run tox` and Ruby's `bundle exec rake` workflows were initially
  missed. They are now included as candidate workflows for the coding agent to
  inspect; this does not claim to statically validate every CI job.
- The pinned JavaScript tracer dereferenced a missing `scenario.id` for Cucumber
  Background nodes when impacted-test detection was enabled. Local Cucumber runs
  disable that feature, and generated CI guidance carries the same workaround.
  CloudEvents' conformance scenarios and the independent
  [Cucumber 7 TypeScript starter](https://github.com/hdorgeval/cucumber7-ts-starter/tree/02fb70d50ce0ccbad7ccc51fc856362f5efc5470)
  passed after this change. The starter has no GitHub Actions workflow, so its
  extra check covered testdrive only. A Background is now in the fixture matrix.
- Returning a completely empty known-tests map disabled Jest's existing retry
  behavior. The intake now supplies empty datasets for all supported runner names;
  the existing Jest retry and concurrent-session assertions remain intact.
- Cypress starts its config process beside the generated wrapper. The wrapper now
  restores the project working directory before loading the original config, so
  relative file access and existing after-run hooks keep their original behavior.
  The fixture asserts both a customer task and an after-run file write.
- Ruby's native tracer extension failed to compile under a path containing spaces.
  Testdrive now reports this prerequisite directly. Use a checkout without spaces
  for Ruby; JavaScript and Python fixtures cover paths containing spaces.
- Ruby uses a copied lockfile with relocated local PATH sources, preserving the
  customer's resolved versions where compatible while adding the pinned tracer
  only to the session-owned bundle.

## Deliberate limits

Root projects and GitHub Actions onboarding only. No monorepo orchestration,
implicit application/browser installation, live Datadog connection validation,
public result schema, savings estimates, or Milestone 3/4 feature guarantees.
Only macOS arm64 was used for this open-source sample. Coverage depends on the
framework/tracer and the project's existing coverage setup; unsupported or absent
coverage is shown explicitly rather than inferred from passing tests.
