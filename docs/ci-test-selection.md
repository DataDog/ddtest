# Integration test selection

The CI workflow runs an integration input planner in **shadow mode**. Every
existing test job still runs. The job summary reports which suites have changed
source inputs and which are candidates for reusing earlier evidence. The JSON
artifact, `ci-integration-shadow-plan`, retains every source digest, each suite's
fingerprint and changed paths, and the baseline's run and revision.

The baseline is an artifact from one of the ten most recent successful `CI`
push runs on `main` in this repository. The workflow publishes that artifact
only after every integration matrix passes. Failed, skipped or cancelled jobs
cannot publish evidence. Missing, expired, incompatible or unavailable baseline
artifacts make every suite run in the plan.

## Input ownership

[`scripts/ci_test_plan.json`](../scripts/ci_test_plan.json) declares each suite's
jobs, owned input paths and dependencies on other suites. Shared and
unclassified files affect every suite, including changes to the module files,
CLI entry point, workflow, planner and manifest. Only explicitly listed
documentation paths are ignored; embedded instructions are inputs.
`additional_inputs` adds a consumer without narrowing ownership: generic
onboarding helpers still affect every suite.

For example, a change to `internal/testdrive/python_validation.go` affects the
Python suite. A JavaScript platform change affects every JavaScript suite and
onboarding. Some Jest-named files implement shared validation and feature
probing; those files conservatively affect every suite. Fixtures without an
explicit owner also affect every suite.

Content, path and executable/symlink type are fingerprinted. Modification times
do not matter. Added, deleted and renamed inputs invalidate their consumers.
The planner snapshots tracked and non-ignored untracked files, so local previews
include edits without requiring a commit. Keep preview outputs outside the
checkout.

## Inspect a local plan

The planner needs Python 3 and Git, with no third-party Python packages:

```sh
python3 scripts/ci_test_plan.py --output /tmp/plan.json --summary /tmp/plan.md
# Compare a second snapshot with the first one:
python3 scripts/ci_test_plan.py --baseline /tmp/plan.json --output /tmp/next-plan.json
```

Use `--github-baseline --repository DataDog/ddtest` instead of `--baseline` to
read passed main evidence with an authenticated GitHub CLI. The artifact records
the original source snapshot, before test setup modifies module files.

## Before enabling reuse

`source_unchanged` is not permission to skip a test. Dependencies installed by
the fixtures, floating versions such as `latest`, Go and language toolchains,
runner/browser identities and execution flags must also be resolved and checked.
The plan explicitly records `external_inputs_verified: false` until that work is
implemented. The current artifact certifies source inputs only.

Review shadow decisions against complete runs, especially edits to shared
helpers, embedded scripts and new fixtures. Keep the full integration matrix
while validating the ownership rules. Any later reuse must preserve required
checks and attach the original passed run's provenance to the current result.
