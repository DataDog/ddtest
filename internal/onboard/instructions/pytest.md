Configure the existing pytest steps with `datadog/test-visibility-github-action@v3`
after checkout, Python setup, and installation of the project's test dependencies:

Ask a human to connect Datadog and add the API key secret before running CI.

```yaml
- uses: datadog/test-visibility-github-action@v3
  with:
    api_key: ${{ secrets.DD_API_KEY }}
    site: datadoghq.com
    languages: python
    python-tracer-version: '<resolved-release>'
```

Preserve every existing test command, Python/OS matrix, working directory,
pytest option, plugin, reporter, and coverage threshold. The action exports
PYTHONPATH and PYTEST_ADDOPTS=--ddtrace. Merge existing options with these values;
do not overwrite them on the test step. Guard credential-dependent setup for
fork pull requests while preserving their uninstrumented tests.

If CI disables pytest plugin autoload, explicitly load the tracer with
`-p ddtrace` in its existing pytest command or PYTEST_ADDOPTS. Local testdrive
plugin loading does not establish that the CI process loads the plugin.

If CI uses tox, the action's environment must reach the pytest environment.
Extend its existing `pass_env` (`passenv` in INI) with `DD_*`, `PYTHONPATH`, and
`PYTEST_ADDOPTS`; preserve existing entries and apply this to every targeted
pytest environment. For native tox TOML, merge into the appropriate test base:

```toml
[tool.tox.env_run_base]
pass_env = ["DD_*", "PYTHONPATH", "PYTEST_ADDOPTS"]
```

For nox or another isolated wrapper, verify the same variables reach its actual
pytest process. Merely adding the action before a wrapper does not establish
instrumentation. Keep wrapper commands and the test matrix intact.

Prepare and activate the same environment used by CI. uv projects use
`uv run --no-sync python -m pytest` by default; testdrive does not synchronize
project dependencies. Explicit commands can select Python, pytest, uv, or poetry.
CI inventory resolves static matrix expressions such as
`${{ matrix.tox || format('py{0}', matrix.python) }}` and explicit single tox
environments defined in native `pyproject.toml` configuration. It checks inherited
pytest commands, tracer environment forwarding and selected Python versions.
Unknown replacements, custom inheritance and other tox configuration formats
remain unvalidated. Discovery preserves the original tox invocation; it does not
provision or execute tox environments. For tox/nox or other wrappers, enter the
selected test environment and validate its actual Python/pytest command.

1. Run `ddtest testdrive --framework pytest --all --check-only --yes` to inspect
   discovered commands, CI Python requirements, and tracer release agreement.
2. Finish setup edits, then run
   `ddtest testdrive --framework pytest --all --tracer-version <resolved-release> --yes`.
   Use `--command '<existing pytest command>'` for a focused configuration.
   A standalone `--command` run reports PASSED when that command's local checks
   pass, even if the repository has no Datadog CI action. CI findings and other
   unexecuted configurations remain advisories in the report. The default pytest
   invocation also validates one local command; it does not imply `--all`. `--all`,
   `--check-only` and guided onboarding still check their broader CI scope.
3. Inspect the baseline/reporting-only comparison and the separate retry,
   early-flake-detection, skipping, quarantine, disabled, and attempt-to-fix
   experiments. Matching pre-existing failures are acceptable. Missing results,
   missing events, configuration differences, or failed probes block completion.

An installed project ddtrace takes precedence over --tracer-version; align CI
with the release actually validated. Temporary tracer installations and probes
are removed. Keep `.testoptimization/report.html` and
`.testoptimization/testdrive.json`, including on failure. A later --check-only
retains previous execution only as historical evidence. Copy the final verdict
and absolute Markdown report links printed by testdrive. Local validation uses
a mock endpoint and needs no Datadog credentials; remote CI and the real Datadog
backend are not exercised.

`testdrive --report` reads saved pytest evidence as `historical_report` with
`freshness_unverified`; it does not rerun tests or certify current source,
dependencies or Python. Rerun testdrive before claiming current success. The
structured `--agent` onboarding coordinator remains JavaScript-only.
