# Contributing

Community contributions to the DDTest tool are welcome! See below for some basic guidelines.

## Want to request a new feature?

Many great ideas for new features come from the community, and we'd be happy to consider yours!

To share your request, you can [open a Github issue](https://github.com/DataDog/ddtest/issues/new) with the details about what you'd like to see. At a minimum, please provide:

- The goal of the new feature
- A description of how it might be used or behave
- Links to any important resources (e.g. Github repos, websites, screenshots, specifications, diagrams)

Additionally, if you can, include:

- A description of how it could be accomplished
- Code snippets that might demonstrate its use or implementation
- Screenshots or mockups that visually demonstrate the feature
- Links to similar features that would serve as a good comparison
- (Any other details that would be useful for implementing this feature!)

## Found a bug?

For any urgent matters (such as outages) or issues concerning the Datadog service or UI, contact our support team via <https://docs.datadoghq.com/help/> for direct, faster assistance.

You may submit bug reports concerning the DDTest tool by [opening a Github issue](https://github.com/DataDog/ddtest/issues/new). At a minimum, please provide:

- A description of the problem
- Steps to reproduce
- Expected behavior
- Actual behavior
- Errors (with stack traces) or warnings received
- Any details you can share about your configuration including:
  - Programming language and test framework together with their versions
  - `ddtest` version
  - Any information on Datadog-related environment variables in your CI

If at all possible, also provide:

- Logs from running your test suite with env DD_TRACE_DEBUG=1
- Screenshots, links, or other visual aids that are publicly accessible
- Code sample or test that reproduces the problem
- An explanation of what causes the bug and/or how it can be fixed

Reports that include rich detail are better, and ones with code that reproduce the bug are best.

## Have a patch?

We welcome code contributions to the library, which you can [submit as a pull request](https://github.com/DataDog/ddtest/pull/new/main). To create a pull request:

1. **Fork the repository** from <https://github.com/DataDog/ddtest>
2. **Make any changes** for your patch.
3. **Write tests** that demonstrate how the feature works or how the bug is fixed.
4. **Update any documentation** such as `Readme.md`, especially for new features.
5. **Submit the pull request** from your fork back to the latest revision of the `main` branch on <https://github.com/DataDog/ddtest>.

Use exactly these PR description sections: `What`, `Why`, and `E2E testing`.
The `E2E testing` section is the manual QA test plan: state prerequisites, list
steps a person can perform on this PR's branch, and describe the expected result
of each scenario. Include relevant failure cases and cleanup. For internal
components, provide a runnable manual harness if there is no public entry point;
for documentation changes, describe the documentation/usage review. Automated
test commands, CI status, and "tests passed" are not a manual QA plan; keep those
results in PR checks or a separate comment.

The pull request will be run through our CI pipeline, and a project member will review the changes with you. At a minimum, to be accepted and merged, pull requests must:

- Have a stated goal and detailed description of the changes made
- Include thorough test coverage and documentation, where applicable
- Pass all tests and code quality checks (linting/coverage/benchmarks) on CI
- Receive at least one approval from a project member with push permissions

We also recommend that you share in your description:

- Any motivations or intent for the contribution
- Links to any issues/pull requests it might be related to
- Links to any webpages or other external resources that might be related to the change
- Screenshots, code samples, or other visual aids that demonstrate the changes or how they are implemented
- Benchmarks if the feature is anticipated to have performance implications
- Any limitations, constraints or risks that are important to consider

## Releasing

Repository maintainers (effective GitHub Maintain or Admin permissions, including
custom roles such as `dd-repo-owner`) can create a release without
creating or pushing a tag locally:

1. Open [Actions → Release](https://github.com/DataDog/ddtest/actions/workflows/release.yml).
2. Select **Run workflow**, leave the branch set to `main`, and enter an unused
   version such as `v1.8.0` (`vMAJOR.MINOR.PATCH`, without leading zeroes).
3. Run the workflow. It checks the initiator's permissions, checks out the latest
   `main`, builds all six binaries, creates the tag at that exact checkout commit,
   and creates a draft release with generated notes and the binaries attached.
4. Follow the link in the workflow summary, review the notes and assets, and
   publish the draft.

The workflow and its dd-octo-sts policy must be merged to `main` before first use.
GitHub allows users with Write access to click **Run workflow**, but the release
job rejects anyone without Maintain or Admin access. Re-runs check both the
original initiator and the person requesting the re-run. Runs from other branches
or forks are skipped. Release runs are serialized, and existing tags are never
moved or overwritten. If `main` advances during a build, the tag still identifies
the commit that was built. Pushing a version tag no longer starts a release.

If a run fails after creating the tag, inspect its logs and any draft release.
The workflow deliberately refuses to reuse that version; recover the draft and
assets manually or choose a new version. Never move a published release tag.

This follows the manual version-input approach in
[datadog-sync-cli](https://github.com/DataDog/datadog-sync-cli/blob/main/.github/workflows/prepare_release.yml)
and the draft-release review step in
[datadog-ci](https://github.com/DataDog/datadog-ci/blob/master/.github/workflows/publish-release.yml).
Unlike those tools, ddtest does not need a version-bump PR: its binary version is
injected by `make release VERSION=...`.

## Final word

Many thanks to all of our contributors, and looking forward to seeing you on Github! :tada:

- Datadog Test Optimization libraries team
