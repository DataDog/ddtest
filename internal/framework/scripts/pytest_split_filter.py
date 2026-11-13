"""pytest plugin ddtest loads to run its share of the files it split by test.

DDTEST_SPLIT_FILTER names a JSON file mapping a test file's absolute path to
the tests of it this run keeps or drops:

    {"/repo/tests/test_x.py": {"keep": ["tests/test_x.py::test_a"]},
     "/repo/tests/test_y.py": {"drop": ["tests/test_y.py::TestC::test_m"]}}

An ID matches the item whose name in its file (the node ID after the first
"::") equals the ID's, or lies under it: a parametrization of it (name +
"[...]") or, for a class, its methods (name + "::..."). Unlike pytest's
--deselect, which compares node ID prefixes, "test_a" does not match
"test_ab", nor "TestC::test_m" "TestC::test_m2", and items are matched by
path, compared as real paths, not by node IDs relative to a rootdir other
than the current directory. IDs that match nothing are ignored: a test
removed since the plan neither fails collection nor runs. A file in the
filters that isn't among pytest's arguments stops the run before
collection, since its tests would run unfiltered.

The plugin writes DDTEST_SPLIT_FILTER + ".applied" once it has filtered the
items, so ddtest can tell a run that applied the filter from one in which the
plugin did not load, such as under a wrapper that clears the environment.
"""

import json
import os

import pytest


def _name_in_file(nodeid):
    return nodeid.split("::", 1)[1] if "::" in nodeid else ""


def _matches(name, names):
    """Whether name, or a class or function it lies under, is in names."""
    if name in names:
        return True
    for i, char in enumerate(name):
        if char == "[":
            # Parameters end the name, and may hold "::" themselves.
            return name[:i] in names
        if char == ":" and name.startswith("::", i) and name[:i] in names:
            return True
    return False


def _load_filters(path):
    with open(path) as f:
        rules = json.load(f)
    filters = {}
    for file, rule in rules.items():
        keep = "keep" in rule
        ids = rule["keep"] if keep else rule.get("drop") or []
        filters[os.path.realpath(file)] = (keep, {_name_in_file(i) for i in ids})
    return filters


def pytest_configure(config):
    path = os.environ.get("DDTEST_SPLIT_FILTER")
    if not path:
        return
    root = str(config.invocation_params.dir)
    given = {os.path.realpath(os.path.join(root, arg.split("::", 1)[0])) for arg in config.args}
    missing = sorted(set(_load_filters(path)) - given)
    if missing:
        raise pytest.UsageError(
            "ddtest's split filter names files that aren't among pytest's arguments, "
            "so their tests would run unfiltered: " + ", ".join(missing)
        )


def pytest_collection_modifyitems(config, items):
    path = os.environ.get("DDTEST_SPLIT_FILTER")
    if not path:
        return
    filters = _load_filters(path)
    real = {}
    selected, deselected = [], []
    for item in items:
        file = str(item.path)
        if file not in real:
            real[file] = os.path.realpath(file)
        rule = filters.get(real[file])
        if rule is None:
            selected.append(item)
            continue
        keep, names = rule
        if _matches(_name_in_file(item.nodeid), names) == keep:
            selected.append(item)
        else:
            deselected.append(item)
    if deselected:
        config.hook.pytest_deselected(items=deselected)
        items[:] = selected
    with open(path + ".applied", "w"):
        pass
