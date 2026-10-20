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
For tox/nox or other wrappers, enter the selected test environment and validate
its actual Python/pytest command; unresolved wrappers remain unvalidated.

1. Run `ddtest testdrive --framework pytest --all --check-only --yes` to inspect
   discovered commands, CI Python requirements, and tracer release agreement.
2. Finish setup edits, then run
   `ddtest testdrive --framework pytest --all --tracer-version <resolved-release> --yes`.
   Use `--command '<existing pytest command>'` for a focused configuration.
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
