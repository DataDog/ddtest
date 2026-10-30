"""Native pytest outcomes, independent of Datadog's event intake."""
import json
import os
import shutil
import tempfile
from pathlib import Path

import pytest

_tests = []
_active = {}
_errors = []
_coverage_threshold_disabled = False
_rootpath = Path.cwd()


def pytest_addoption(pluginmanager):
    config = pluginmanager.get_plugin("pytestconfig")
    controlled = os.environ.get("DDTEST_PYTEST_INSTRUMENTED")
    if controlled is not None:
        # Configure the registered options directly: inherited -- markers and
        # wrapper arguments must not turn our control flags into test paths.
        config.option.ddtrace = controlled == "true"
        setattr(config.option, "no-ddtrace", controlled != "true")
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


@pytest.hookimpl(tryfirst=True)
def pytest_load_initial_conftests(early_config, args):
    owned = os.environ.get("DDTEST_PYTEST_CONFIG_OVERRIDES")
    if owned is not None:
        # Our copied defaults were supplied before the user's environment
        # options. Remove that transport pair after pytest has consumed it;
        # a -- in the defaults must not reinterpret overrides as collection paths.
        start = len(early_config.getini("addopts"))
        overrides = json.loads(owned)
        if args[start:start + len(overrides)] == overrides:
            del args[start:start + len(overrides)]

    if early_config.pluginmanager.hasplugin("cacheprovider"):
        # Seed each run independently: --lf/--ff/--nf must observe the same
        # starting state, including when --cache-clear is requested.
        cache = Path(os.path.expandvars(os.path.expanduser(early_config.getini("cache_dir"))))
        if not cache.is_absolute():
            cache = early_config.rootpath / cache
        isolated = tempfile.mkdtemp(prefix="pytest-cache-", dir=Path(os.environ["DDTEST_PYTEST_RESULTS"]).parent)
        # Preserve cache values and cache.mkdir data, without copying unrelated
        # files when cache_dir points at an existing project directory.
        for name in ("v", "d"):
            if (cache / name).is_dir():
                shutil.copytree(cache / name, Path(isolated) / name)
        early_config._inicache["cache_dir"] = isolated


def pytest_configure(config):
    global _coverage_threshold_disabled, _rootpath
    _rootpath = config.rootpath
    probe = os.environ.get("DDTEST_PYTEST_PROBE")
    if probe:
        # Keep the original config, conftests and plugins for isolated probes.
        config.args[:] = [probe]
        config.option.keyword = ""
        config.option.markexpr = ""
        if hasattr(config.option, "cov_fail_under"):
            config.option.cov_fail_under = 0
            _coverage_threshold_disabled = True


def pytest_collectreport(report):
    if report.failed:
        _errors.append(str(report.longrepr))


def pytest_runtest_logstart(nodeid, location):
    # Each item execution gets its own result, even when selectors repeat a node.
    # The reports within that execution (phases and tracer retries) share it.
    _active.pop((nodeid, None, None), None)


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
    # xdist's controller reports identify both the worker and collection item;
    # duplicate node IDs can overlap, and each worker can execute several copies.
    key = (report.nodeid, getattr(report, "worker_id", None), getattr(report, "item_index", None))
    previous = _active.get(key)
    if report.when == "teardown" and report.skipped and previous:
        return
    result = {
        "file": str((_rootpath / file).resolve()),
        "name": name,
        "status": report.outcome,
        "failure": "\n".join(filter(None, (
            previous["failure"] if previous and report.when == "teardown" else "",
            str(report.longrepr) if report.failed else "",
        ))),
    }
    if previous is None:
        _tests.append(result)
        _active[key] = result
    else:
        previous.update(result)


def pytest_sessionfinish(session, exitstatus):
    if hasattr(session.config, "workerinput"):
        return
    Path(os.environ["DDTEST_PYTEST_RESULTS"]).write_text(json.dumps({
        "complete": True,
        "tests": _tests,
        "errors": _errors,
        "coverage_threshold_disabled": _coverage_threshold_disabled,
    }))
