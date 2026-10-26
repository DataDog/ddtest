"""Native pytest outcomes, independent of Datadog's event intake."""
import json
import os
from pathlib import Path

import pytest

_tests = {}
_errors = []
_coverage_threshold_disabled = False


def pytest_addoption(pluginmanager):
    config = pluginmanager.get_plugin("pytestconfig")
    options = getattr(config, "known_args_namespace", None)
    disabled = os.environ.get("PYTEST_DISABLE_PLUGIN_AUTOLOAD") or getattr(
        options, "disable_plugin_autoload", False
    )
    if not disabled:
        return
    names = (
        "ddtrace", "ddtrace.contrib.pytest.plugin", "ddtrace.contrib.internal.pytest.plugin",
        "ddtrace.testing.internal.pytest.entry_point",
    )
    explicit = list(getattr(options, "plugins", None) or [])
    explicit.extend(os.environ.get("PYTEST_PLUGINS", "").split(","))
    if any(
        pluginmanager.has_plugin(name) or pluginmanager.is_blocked(name)
        or name in explicit or "no:" + name in explicit
        for name in names
    ):
        return
    pluginmanager.import_plugin("ddtrace", consider_entry_points=True)


def pytest_configure(config):
    global _coverage_threshold_disabled
    probe = os.environ.get("DDTEST_PYTEST_PROBE")
    if probe:
        # Keep the original config, conftests, plugins and selection options.
        config.args[:] = [probe]
        if hasattr(config.option, "cov_fail_under"):
            config.option.cov_fail_under = 0
            _coverage_threshold_disabled = True


def pytest_collectreport(report):
    if report.failed:
        _errors.append(str(report.longrepr))


@pytest.hookimpl(hookwrapper=True, tryfirst=True)
def pytest_runtest_logreport(report):
    yield
    # Retry attempts are not additional native tests. The final ordinary report
    # determines the runner outcome; telemetry checks each attempt separately.
    if report.outcome not in ("passed", "failed", "skipped"):
        return
    if report.when != "call" and report.passed:
        return
    file, _, name = report.nodeid.partition("::")
    previous = _tests.get(report.nodeid)
    if report.when == "teardown" and report.skipped and previous:
        return
    _tests[report.nodeid] = {
        "file": str(Path(file).resolve()),
        "name": name,
        "status": report.outcome,
        "failure": str(report.longrepr) if report.failed else "",
    }


def pytest_sessionfinish(session, exitstatus):
    if hasattr(session.config, "workerinput"):
        return
    Path(os.environ["DDTEST_PYTEST_RESULTS"]).write_text(json.dumps({
        "complete": True,
        "tests": list(_tests.values()),
        "errors": _errors,
        "coverage_threshold_disabled": _coverage_threshold_disabled,
    }))
