# GlobalID/Minitest completion — 2026-10-26

This follows the [Ruby improvement evaluation](ruby-improvements-2026-10-26.md). Money/RSpec is complete and was not rerun. This work focuses on the two blockers in GlobalID/Minitest, preserving its test sources, isolation, commands and matrix. Actual GitHub CI and Datadog backend processing remain not exercised.

## What the incomplete runs taught us

A focused fixture using the real `ActiveSupport::Testing::Isolation` executes seven native tests but receives only four events with asynchronous delivery. A CI-visible initializer using Datadog's public synchronous writer and the configured CI transport delivers all seven events before forked children call `exit!`. The same initializer runs in testdrive and CI; neither tests nor isolation hooks are patched. This controlled reproduction establishes a fork-delivery problem that the earlier trial reports could only suggest.

It also exposed two ddtest defects: instrumented Ruby options placed the Datadog preload after application initializers, and temporary tracer installation inherited relative application preloads during native extension compilation. Testdrive now preserves the CI preload order and clears test preloads only during disposable tracer installation. Regression tests reproduce both defects; a native integration runs real isolation both with bundled tracing and with fallback tracer installation, checking unchanged original manifests and test bytes.

Ruby 2.7 was not proven unsupported by the locally selected native ffi package. Exact same-version portable ffi metadata admits Ruby 2.7, with identical runtime dependency requirements. Testdrive now checks that metadata conservatively; offline, mismatched and still-incompatible metadata stays inconclusive. A direct tracer requirement still rejects an incompatible target. This static check does not certify target compilation or application execution.

A separate real Ruby 2.7.8 container confirms the installer distinction: stock RubyGems 3.1.6 selects an incompatible native ffi package and fails; RubyGems 3.4.22 with Bundler 2.4.22 installs and loads datadog-ci 1.42.0 using the portable ffi 1.17.4 build. Onboarding documents that CI resolver setting and keeps the numeric Ruby 2.7 tests and instrumentation. This is install/load evidence, not full GlobalID execution on Ruby 2.7.

## Controlled validation

The unchanged pinned GlobalID suite with only CI integration, initializer and report-ignore edits reaches `COMPLETE LOCALLY`: five original Gemfile configurations; 875 native executions per mode (874 passes and one equal existing Rails-main failure); all 875 telemetry events matched; 30/30 supported feature checks. Real application coverage is present in all five configurations and linked to 874/875 reporting results. Seven numeric CI Ruby selectors remain instrumented, including 2.7; only head and TruffleRuby are excluded from instrumentation. Source preservation, report exclusions and owned cleanup pass, with a verified final receipt.

The earlier seven-test/four-event report and an intermediate full report with complete telemetry but unresolved Ruby 2.7 metadata are retained separately. The native metadata lookup initially exposed a missing RubyGems RemoteFetcher require, which was fixed before the final signed binary and trials. No partial report is relabeled successful.

## Fresh uncoached agent trials

Source `668c25e2f9868bdcca9e7a56a3701ce50d77ece9` was clean at build. Linux arm64 binary SHA-256: `2a8a3a43ea9a770fa69d20b45008e6278f43a4f3fa39c6907a4aad6607b2f67a`. Both trials use immutable image `sha256:004d012b985de5b49b052c7fbcc843ccdaae0c9101bc3d0c73cdf0c5dc38b52f`, Ruby 3.3.11, Node 24.19.0, Claude 2.1.220 and Codex 0.163.0. The image supplies Bundler 2.5.22; project setup may install the lockfile's Bundler version, which is recorded in activity. The public project pin is `3155f04179c59bc79a6c60b01a40ee296905f069`. Each trial starts from a fresh clone and private dependencies, using the CLI default model and explicitly authorized existing model authentication.

The only prompt is `Use ddtest in /trial/bin and onboard this repository to test-optimization for minitest only.` No coaching, follow-up messages or model overrides are supplied. The evaluator skill stays outside the tested checkout and agent configuration. The existing dedicated Colima VM, read-only container root, dropped capabilities, four CPU/six GiB limits and two GiB executable private tmpfs are used. Dependencies and Ruby scratch use private container-native storage to avoid bind-mount overhead and host file-descriptor pressure; reports remain in the clone until collection. Neither real Datadog nor GitHub/cloud credentials or host CLI configuration are supplied. Egress remains unrestricted as disclosed by the evaluator skill. Timings are not a performance comparison.

| Agent | Product verdict | Configurations | Native executions per mode | Matched events | Features | Independent review |
| --- | --- | --- | --- | --- | --- | --- |
| Claude | COMPLETE LOCALLY | 5/5 | 874 passes, one existing failure | 875/875 | 30/30 | Passed; canonical response retained in a blockquote |
| Codex | COMPLETE LOCALLY | 5/5 | 874 passes, one existing failure | 875/875 | 30/30 | Passed; exact canonical response |

Both agents add only the CI action, RubyGems setting, the documented initializer and report ignore. Neither changes tests, helpers, Gemfiles, commands or matrix exclusions. Both retain the seven numeric instrumentation targets including Ruby 2.7, with only head/TruffleRuby excluded. Four configurations have 175 native passes per mode; Rails main has 174 passes and the same one failure in both modes. Every configuration receives 175 events. Application coverage links all 875/875 results in each final agent report (the earlier controlled diagnostic links 874/875).

Codex initially confused the separate core `datadog` 2.47.0 version with `datadog-ci`. Its first readiness/resume path failed during tracer installation before executing suites. The same uncoached agent diagnosed the package mismatch, corrected the CI pin and explicit testdrive argument to 1.42.0, reran all five bundles and verified the final receipt. The failed command/diagnostics remain in activity and are not counted successful. No evaluator-created fresh retry or follow-up coaching was used.

Claude followed check-only readiness, installed missing bundles, ran full validation once, then verified `testdrive --report`. Its canonical text is word-for-word unchanged in a Markdown blockquote; the evaluator's raw matcher marks review, and independent blockquote normalization confirms equality. Its extra prose describes the one existing failure broadly; the saved per-configuration evidence locates that failure only on Rails main. Both final answers explicitly retain the existing failure and the limits of static CI checks.

All selected lockfiles are retained. The root and four other Gemfile lockfiles match across agents; Rails 8.0 differs because Codex explicitly resolves that bundle (Rails 8.0.5.1) while Claude reuses the prepared Rails 8.0.3 environment. These are preserved dependency-resolution differences within the original manifest constraints, not test-source or manifest changes. The two runs establish completion on their recorded environments, not hermetically identical dependencies or a model performance comparison.

## Automated checks

- Real native Minitest isolation integration, bundled and fallback tracer: passed, seven native events and every supported feature retained; unchanged Gemfile, lockfile and tests.
- Existing native RSpec/Minitest integration: passed. These synthetic shared-adapter checks do not reopen Money/RSpec's completed OSS evaluation.
- New Ruby metadata/preload/fallback and onboarding instruction regression checks: passed.
- Evaluator suite: 94 tests, 85 passed and nine optional offline-image Docker cases skipped.
- Final `make lint`: zero issues.
- Final `make test`: only the previously reproduced, unchanged-main `TestMochaNestedGuardDisablesTracingOnlyForNestedCLIs` failure. No Mocha changes were made.

## Evidence and cleanup

Private evidence is retained at `/tmp/ddtest-globalid-focus-erzlec_6`, including signed source/binary/image identity, before/after controlled report pairs, Ruby 2.7 installer evidence, validation logs and each agent's public activity, actual diff, final response, report pair and independent assessment. Relevant small configuration and lockfiles are collected before cleanup; raw telemetry, model credentials, dependency trees and private caches are not archived.

Both evaluator containers and their owned trial checkouts/private caches were removed with verified ownership cleanup after report/configuration collection. The model-free diagnostic container and its marked checkout were also removed. Ruby 2.7 proof containers are gone. Unrelated containers, Docker images, shared caches and the original main checkout remain untouched. Nothing was sent to a real Datadog intake or hosted report service.
