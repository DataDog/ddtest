"""Static pytest inventory without importing pytest or tests.

This is not a Python evaluator. Unsupported syntax/configuration invalidates the
entire inventory and asks Go to use normal collection. Keep identity behavior
covered by the tracer-backed compatibility tests when extending this subset.

Supported collection semantics were verified against real pytest and the
ddtrace discovery report (see the parity tests in internal/compatibility):
parametrized fixtures expand with value-based ids before parametrize marker
parts, skip marks omit tests from the inventory, local inheritance contributes
methods, and custom python_functions/python_classes patterns select names.
"""

import ast
from collections import Counter, defaultdict
import configparser
import fnmatch
import importlib.metadata
import itertools
import json
import os
from pathlib import Path
import re
import shlex
import sys
import tokenize


class Unsupported(Exception):
    pass


# --- CLI options -------------------------------------------------------------

SAFE_OPTIONS = {
    "--ddtrace", "--quiet", "--verbose", "-s", "--no-header", "--no-summary",
    "--disable-warnings", "--disable-pytest-warnings", "--showlocals", "-l",
    "--full-trace", "-x", "--exitfirst", "--strict-markers", "--strict-config",
    "--setup-show",
}
VALUE_OPTIONS = {
    "--color": {"yes", "no", "auto"},
    "--code-highlight": {"yes", "no"},
    "--capture": {"fd", "sys", "no", "tee-sys"},
    "--tb": {"auto", "long", "short", "line", "native", "no"},
    "--dist": {"load", "loadscope", "loadfile", "loadgroup", "each", "worksteal"},
}

# pytest-cov instrumentation options. They never select, add, or deselect tests;
# a missing newer option only loses the fast path (fallback is the safe direction).
COVERAGE_FLAG_OPTIONS = {"--no-cov", "--cov-append", "--cov-branch", "--cov-reset"}
COVERAGE_VALUE_OPTIONS = {"--cov", "--cov-config", "--cov-context", "--cov-fail-under", "--cov-report", "--cov-source"}

# Options whose values only configure execution, reporting, or instrumentation.
FREEFORM_VALUE_OPTIONS = {
    "--basetemp", "--durations", "--durations-min", "--junitxml", "--maxfail",
    "--maxprocesses", "--max-worker-restart", "--numprocesses",
    "--randomly-seed", "--verbosity",
}

# Plugins verified to affect only execution, reporting, ordering, or fixtures —
# never the collected inventory. Collection-affecting plugins (hypothesis,
# pytest-bdd, pytest-describe, pytest-mypy, pytest-repeat, pytest-split,
# pytest-testmon, ...) must never be added. Names are PEP 503-normalized.
INVENTORY_NEUTRAL_PLUGINS = {
    "ddtrace",  # identity source for this feature; parity-tested
    # instrumentation and execution
    "pytest-cov", "pytest-xdist", "pytest-forked", "pytest-parallel",
    "pytest-timeout", "pytest-rerunfailures", "pytest-flaky", "pytest-retry",
    # output and reporting
    "pytest-sugar", "pytest-instafail", "pytest-pretty", "pytest-clarity",
    "pytest-icdiff", "pytest-emoji", "pytest-tldr", "pytest-html",
    "pytest-json-report", "pytest-metadata", "pytest-tap", "pytest-nunit",
    "pytest-github-actions-annotate-failures",
    # ordering only (reorder hooks, identical inventory)
    "pytest-randomly", "pytest-order", "pytest-ordering",
    # fixture, assertion, and service helpers
    "pytest-mock", "pytest-subtests", "pytest-benchmark", "pytest-env",
    "pytest-lazy-fixture", "pytest-freezegun", "pytest-freezer",
    "pytest-datadir", "pytest-regressions", "pytest-vcr", "pytest-recording",
    "pytest-docker", "pytest-postgresql", "pytest-redis", "pytest-mysql",
    "pytest-mongo", "pytest-httpserver", "pytest-responses", "pytest-qt",
    "pytest-selenium", "pytest-localserver", "pytest-console-scripts",
    "pytest-check", "pytest-assume",
}

DEFAULT_FUNCTION_PATTERNS = ["test"]
DEFAULT_CLASS_PATTERNS = ["Test"]
MAX_CASES = 100_000


def normalize_plugin_name(name):
    return re.sub(r"[-_.]+", "-", name).lower()


def check_options(options, allow_paths=False):
    positional = False
    index = 0
    while index < len(options):
        option = options[index]
        index += 1
        if not positional:
            if option in SAFE_OPTIONS or re.fullmatch(r"-[qv]+", option):
                continue
            if re.fullmatch(r"-r[aAfEsxXpP]+", option):
                continue
            # Warning filters are free-form and never select tests.
            if option == "-W":
                if index < len(options):
                    index += 1
                continue
            if re.fullmatch(r"-W\S+", option):
                continue
            # xdist worker count: -n auto, -n 4, -n=2, -nauto.
            if option == "-n" or re.fullmatch(r"-n(=)?(auto|\d+)", option):
                if option == "-n" and index < len(options) and not options[index].startswith("-"):
                    index += 1
                continue
            # -p may only disable plugins here; loading one hides collection code.
            if option == "-p":
                if index >= len(options) or not re.fullmatch(r"no:[A-Za-z0-9_.\-]+", options[index]):
                    raise Unsupported("-p may only disable plugins in static discovery")
                index += 1
                continue
            if re.fullmatch(r"-p(=)?no:[A-Za-z0-9_.\-]+", option):
                continue
            key, separator, value = option.partition("=")
            if key in COVERAGE_FLAG_OPTIONS and not separator:
                continue
            if key in COVERAGE_VALUE_OPTIONS or key in FREEFORM_VALUE_OPTIONS:
                # Values are paths, numbers, or report specs; never consume a
                # following option as one.
                if not separator and index < len(options) and not options[index].startswith("-"):
                    index += 1
                continue
            if key in VALUE_OPTIONS:
                if not separator:
                    if index >= len(options):
                        raise Unsupported("missing pytest presentation option value")
                    value = options[index]
                    index += 1
                if value in VALUE_OPTIONS[key]:
                    continue
                raise Unsupported("unsupported pytest presentation option value")
            if option == "--" and allow_paths:
                positional = True
                continue
        if allow_paths and not option.startswith("-") and "::" not in option and Path(option).exists():
            continue
        raise Unsupported("unsafe or unsupported pytest argument: " + option)


def check_environment():
    check_options(shlex.split(os.environ.get("PYTEST_ADDOPTS", "")))
    if os.environ.get("PYTEST_PLUGINS"):
        raise Unsupported("PYTEST_PLUGINS may customize collection")
    if not os.environ.get("PYTEST_DISABLE_PLUGIN_AUTOLOAD"):
        # Inspect entry point metadata without importing any plugin; include the
        # working directory and PYTHONPATH even though the helper runs in
        # isolated mode. Inventory-neutral plugins are allowed by curated name.
        # Anything else falls back because we cannot see which hooks it
        # registers without importing it.
        paths = [str(Path.cwd())] + os.environ.get("PYTHONPATH", "").split(os.pathsep) + sys.path
        for distribution in importlib.metadata.distributions(path=[p for p in paths if p]):
            if any(ep.group == "pytest11" for ep in distribution.entry_points):
                name = distribution.metadata["Name"] or "<unnamed>"
                if normalize_plugin_name(name) not in INVENTORY_NEUTRAL_PLUGINS:
                    raise Unsupported("installed pytest plugin may customize collection: " + name)


# --- pytest configuration ----------------------------------------------------

def check_config(directory, root):
    """Validate pytest configuration; return custom collection patterns.

    Only the first configuration file with a pytest section, in pytest's own
    precedence order, contributes values; later files are ignored by pytest and
    only validated here. Non-root configuration remains rejected.
    """
    patterns = None
    for name in ("pytest.toml", ".pytest.toml"):
        if (directory / name).exists():
            raise Unsupported("native pytest TOML configuration is not supported")
    for name, section in (("pytest.ini", "pytest"), (".pytest.ini", "pytest"),
                          ("tox.ini", "pytest"), ("setup.cfg", "tool:pytest")):
        path = directory / name
        if not path.exists():
            continue
        parser = configparser.RawConfigParser()
        parser.read(path, encoding="utf-8")
        found = None
        if parser.has_section(section):
            found = check_config_values(dict(parser.items(section)), directory, root)
        elif name in ("pytest.ini", ".pytest.ini") and directory != root:
            raise Unsupported("pytest root outside the working directory")
        if directory == root and patterns is None:
            patterns = found
    path = directory / "pyproject.toml"
    if path.exists():
        if directory != root:
            raise Unsupported("pyproject.toml may change pytest's root directory")
        text = path.read_text(encoding="utf-8")
        try:
            import tomllib
        except ImportError:
            raise Unsupported("pyproject.toml requires Python 3.11+ for static discovery")
        else:
            table = tomllib.loads(text).get("tool", {}).get("pytest", {})
            if table:
                if set(table) != {"ini_options"}:
                    raise Unsupported("unsupported pytest TOML configuration")
                found = check_config_values(table["ini_options"], directory, root)
                if patterns is None:
                    patterns = found
    return patterns


def check_config_values(values, directory, root):
    if directory != root:
        raise Unsupported("pytest configuration outside the working directory")
    patterns = {}
    for key, value in values.items():
        if key in {"testpaths", "python_files", "markers", "filterwarnings", "minversion"}:
            continue
        if key == "addopts":
            check_options(shlex.split(value) if isinstance(value, str) else value)
            continue
        if key in {"python_classes", "python_functions"}:
            entries = shlex.split(value) if isinstance(value, str) else (value or [])
            patterns[key] = list(entries)
            continue
        if key == "python_modules":
            modules = shlex.split(value) if isinstance(value, str) else (value or [])
            if modules:
                raise Unsupported("python_modules is not supported")
            continue
        raise Unsupported("unsupported pytest configuration key: " + key)
    return patterns if ("python_functions" in patterns or "python_classes" in patterns) else None


def pattern_matches(name, pattern):
    # pytest treats patterns without glob characters as name prefixes.
    if any(character in pattern for character in "*?["):
        return fnmatch.fnmatchcase(name, pattern)
    return name.startswith(pattern)


def name_matches(name, patterns):
    return any(pattern_matches(name, pattern) for pattern in patterns)


# --- literals ----------------------------------------------------------------

def literal(node, constants):
    """Evaluate a supported literal expression, resolving module constants."""
    if isinstance(node, ast.Name):
        if node.id in constants:
            return constants[node.id]
        raise Unsupported("unresolvable name in literal expression: " + node.id)
    if isinstance(node, ast.Constant):
        value = node.value
    elif isinstance(node, (ast.List, ast.Tuple)):
        elements = [literal(element, constants) for element in node.elts]
        value = tuple(elements) if isinstance(node, ast.Tuple) else elements
    elif isinstance(node, ast.Dict):
        value = {literal(key, constants): literal(item, constants)
                 for key, item in zip(node.keys, node.values) if key is not None}
    elif isinstance(node, ast.UnaryOp) and isinstance(node.op, (ast.USub, ast.UAdd)):
        try:
            operand = literal(node.operand, constants)
            value = -operand if isinstance(node.op, ast.USub) else +operand
        except TypeError:
            raise Unsupported("unsupported literal expression")
    else:
        try:
            value = ast.literal_eval(node)
        except (ValueError, TypeError, SyntaxError, MemoryError, RecursionError):
            raise Unsupported("unsupported literal expression")

    def supported(value):
        if value is None or type(value) in (str, int, float, bool):
            return True
        if type(value) in (tuple, list):
            return all(supported(item) for item in value)
        if type(value) is dict:
            return all(supported(key) and supported(item) for key, item in value.items())
        return False

    if not supported(value):
        raise Unsupported("unsupported literal parameter type")
    return value


def dotted_name(node):
    if isinstance(node, ast.Name):
        return node.id
    if isinstance(node, ast.Attribute):
        return dotted_name(node.value) + "." + node.attr
    return ""


def escaped_id(value):
    if isinstance(value, str):
        return value.encode("unicode_escape").decode("ascii")
    if value is None or type(value) in (int, float, bool):
        return str(value)
    return None


def unique_ids(ids):
    counts = Counter(ids)
    suffixes = defaultdict(int)
    result = list(ids)
    for index, value in enumerate(ids):
        if counts[value] > 1:
            separator = "_" if value and value[-1].isdigit() else ""
            candidate = value + separator + str(suffixes[value])
            while candidate in result:
                suffixes[value] += 1
                candidate = value + separator + str(suffixes[value])
            result[index] = candidate
            suffixes[value] += 1
    return result


# --- marks -------------------------------------------------------------------

def mark_decorator(decorator):
    """Return (mark_name, call_or_None) for pytest.mark.<name> decorators."""
    call = decorator if isinstance(decorator, ast.Call) else None
    target = call.func if call is not None else decorator
    if not isinstance(target, ast.Attribute):
        return None
    mark = target.value
    if not (isinstance(mark, ast.Attribute) and isinstance(mark.value, ast.Name)
            and mark.value.id == "pytest" and mark.attr == "mark"):
        return None
    if not target.attr.isidentifier():
        raise Unsupported("unsupported pytest mark: " + target.attr)
    return target.attr, call


def mark_skips_tests(name, call, constants):
    """True when the tracer's discovery report omits tests with this mark."""
    if name == "skip":
        return True
    if name == "skipif":
        if call is None or not call.args:
            raise Unsupported("skipif mark requires a literal bool condition")
        condition = literal(call.args[0], constants)
        if not isinstance(condition, bool):
            raise Unsupported("skipif condition must be a literal bool in static discovery")
        return condition
    return False  # xfail and every other mark keeps tests in the inventory.


def pytestmark_skips(value, constants):
    def single(node):
        mark = mark_decorator(node)
        if mark is None:
            raise Unsupported("unsupported pytestmark value")
        name, call = mark
        if name == "parametrize":
            raise Unsupported("parametrize in pytestmark is not supported")
        return bool(mark_skips_tests(name, call, constants))

    if isinstance(value, (ast.List, ast.Tuple)):
        return any([single(item) for item in value.elts])
    return single(value)


def validate_helper_decorator(decorator):
    """Decorators that are inert for inventory on non-test helpers."""
    if mark_decorator(decorator) is not None:
        return
    if isinstance(decorator, ast.Name) and decorator.id in {"staticmethod", "classmethod", "property"}:
        return
    raise Unsupported("decorators other than pytest.mark.* are not supported")


# --- parametrize -------------------------------------------------------------

def parametrization(call, constants):
    """Expand a pytest.mark.parametrize call.

    Returns (argnames, entries) where entries is a list of
    (case_id, {argname: value}, omitted) triples. IDs are deduplicated over all
    cases before skip-marked ones are marked omitted, matching pytest.
    """
    arguments = dict(zip(("argnames", "argvalues", "indirect", "ids", "scope"), call.args))
    if len(call.args) > 5:
        raise Unsupported("unsupported parametrization arguments")
    for keyword in call.keywords:
        if keyword.arg not in {"argnames", "argvalues", "ids", "indirect", "scope"} or keyword.arg in arguments:
            raise Unsupported("unsupported parametrization arguments")
        arguments[keyword.arg] = keyword.value
    if "indirect" in arguments:
        indirect = literal(arguments["indirect"], constants)
        if indirect is not False and not (isinstance(indirect, (list, tuple)) and not indirect):
            raise Unsupported("indirect parametrization requires pytest collection")
    if "scope" in arguments and literal(arguments["scope"], constants) not in (None, "function"):
        raise Unsupported("non-function parametrization scope")
    names = literal(arguments["argnames"], constants)
    string_names = isinstance(names, str)
    comma_names = string_names and "," in names
    names = [name.strip() for name in names.split(",") if name.strip()] if string_names else names
    if comma_names and len(names) == 1:
        raise Unsupported("single-argument trailing comma semantics differ across pytest versions")
    single = string_names and len(names) == 1
    if not isinstance(names, (list, tuple)) or not names or any(
            not isinstance(name, str) or not name.isidentifier() or name == "request" for name in names) or len(set(names)) != len(names):
        raise Unsupported("unsupported parameter names")

    if isinstance(arguments["argvalues"], (ast.List, ast.Tuple)):
        rows = [("ast", element) for element in arguments["argvalues"].elts]
    else:
        # Module-level constants may carry the value list.
        values = literal(arguments["argvalues"], constants)
        if not isinstance(values, (list, tuple)):
            raise Unsupported("parameter values must be a literal list or tuple")
        rows = [("value", value) for value in values]
    if len(rows) > MAX_CASES:
        raise Unsupported("too many parameter cases")
    explicit_ids = literal(arguments["ids"], constants) if "ids" in arguments else None
    if explicit_ids is not None and (not isinstance(explicit_ids, (list, tuple)) or len(explicit_ids) != len(rows)):
        raise Unsupported("unsupported parameter IDs")

    cases, ids, omitted = [], [], []
    for index, (kind, row) in enumerate(rows):
        case_id = None
        skip_case = False
        if kind == "ast" and isinstance(row, ast.Call) and dotted_name(row.func) == "pytest.param":
            case = [literal(item, constants) for item in row.args]
            seen_keywords = set()
            for keyword in row.keywords:
                if keyword.arg in seen_keywords:
                    raise Unsupported("duplicate pytest.param keyword")
                seen_keywords.add(keyword.arg)
                if keyword.arg == "id":
                    case_id = literal(keyword.value, constants)
                    if case_id is not None and not isinstance(case_id, str):
                        raise Unsupported("pytest.param ID must be a string")
                elif keyword.arg == "marks":
                    marks = keyword.value.elts if isinstance(keyword.value, (ast.List, ast.Tuple)) else [keyword.value]
                    for mark in marks:
                        decoded = mark_decorator(mark)
                        if decoded is None:
                            raise Unsupported("unsupported parameter mark")
                        mark_name, mark_call = decoded
                        if mark_name == "parametrize":
                            raise Unsupported("parametrize as a parameter mark is not supported")
                        if mark_skips_tests(mark_name, mark_call, constants):
                            skip_case = True
                else:
                    raise Unsupported("unsupported pytest.param argument")
        else:
            case = literal(row, constants) if kind == "ast" else row
            case = [case] if single else case
        if not isinstance(case, (list, tuple)) or len(case) != len(names):
            raise Unsupported("parameter value arity mismatch")
        if case_id is None and explicit_ids is not None:
            case_id = explicit_ids[index]
        if case_id is None:
            parts = [escaped_id(value) for value in case]
            case_id = "-".join(part if part is not None else name + str(index)
                               for part, name in zip(parts, names))
        else:
            case_id = escaped_id(case_id)
            if case_id is None:
                raise Unsupported("unsupported parameter ID type")
        cases.append(dict(zip(names, case)))
        ids.append(case_id)
        omitted.append(skip_case)
    entries = [(case_id, case, skip_case)
               for case_id, case, skip_case in zip(unique_ids(ids), cases, omitted)]
    return names, entries


# --- fixtures ----------------------------------------------------------------

FIXTURE_ARGUMENTS = ("scope", "params", "autouse", "ids", "name")


def fixture_decorator(decorator):
    """Return the Call node of a pytest.fixture decorator, or None for the
    bare attribute form. Raises nothing; call is_fixture_decorator first."""
    return decorator if isinstance(decorator, ast.Call) else None


def is_fixture_decorator(decorator):
    target = decorator.func if isinstance(decorator, ast.Call) else decorator
    return (isinstance(target, ast.Attribute) and isinstance(target.value, ast.Name)
            and target.value.id == "pytest" and target.attr == "fixture")


def is_staticmethod(function):
    return any(isinstance(decorator, ast.Name) and decorator.id == "staticmethod"
               for decorator in function.decorator_list)


def parse_fixture(decorator, function, constants, method):
    call = fixture_decorator(decorator)
    if not is_fixture_decorator(decorator):
        return None
    if call is not None and len(call.args) > len(FIXTURE_ARGUMENTS):
        raise Unsupported("unsupported fixture arguments")
    arguments = dict(zip(FIXTURE_ARGUMENTS, call.args)) if call is not None else {}
    keywords = call.keywords if call is not None else []
    for keyword in keywords:
        if keyword.arg not in FIXTURE_ARGUMENTS or keyword.arg in arguments:
            raise Unsupported("unsupported fixture arguments")
        arguments[keyword.arg] = keyword.value
    registered = literal(arguments["name"], constants) if "name" in arguments else None
    if registered is not None and not isinstance(registered, str):
        raise Unsupported("fixture name must be a string")
    autouse = literal(arguments["autouse"], constants) if "autouse" in arguments else False
    if not isinstance(autouse, bool):
        raise Unsupported("fixture autouse must be a literal bool")
    expansion = None
    if "params" in arguments:
        params = literal(arguments["params"], constants)
        if not isinstance(params, (list, tuple)):
            raise Unsupported("fixture params must be a literal list or tuple")
        if len(params) > MAX_CASES:
            raise Unsupported("too many fixture cases")
        explicit_ids = literal(arguments["ids"], constants) if "ids" in arguments else None
        if explicit_ids is not None and (not isinstance(explicit_ids, (list, tuple)) or len(explicit_ids) != len(params)):
            raise Unsupported("unsupported fixture IDs")
        name = registered or function.name
        ids = []
        for index, value in enumerate(params):
            case_id = None
            if explicit_ids is not None:
                case_id = explicit_ids[index]
                if case_id is not None:
                    case_id = escaped_id(case_id)
                    if case_id is None:
                        raise Unsupported("unsupported fixture ID type")
            if case_id is None:
                part = escaped_id(value)
                case_id = part if part is not None else name + str(index)
            ids.append(case_id)
        expansion = list(zip(unique_ids(ids), params))
    args = [argument.arg for argument in function.args.posonlyargs + function.args.args + function.args.kwonlyargs]
    if method and args and not is_staticmethod(function):
        args = args[1:]
    return {
        "name": registered or function.name,
        "expansion": expansion,
        "autouse": autouse,
        "poison": False,
        "args": args,
    }


def resolve_poison(specs, chain):
    """Propagate 'poison': fixtures depending on parametrized fixtures expand
    with combined semantics that static discovery does not emulate."""
    def lookup(name):
        if name in specs:
            return specs[name]
        for level in chain:
            if name in level:
                return level[name]
        return None

    changed = True
    while changed:
        changed = False
        for spec in specs.values():
            if spec["poison"]:
                continue
            for argument in spec["args"]:
                target = lookup(argument)
                if target is not None and (target["poison"] or target["expansion"] is not None):
                    spec["poison"] = True
                    changed = True
                    break


# --- module scanning ---------------------------------------------------------

class ModuleScan:
    def __init__(self):
        self.constants = {}
        self.fixtures = {}
        self.functions = []
        self.classes = {}
        self.pytestmark_skip = False


def scan_module(tree, is_conftest):
    scan = ModuleScan()
    seen = set()

    def assign(target, value):
        if not isinstance(target, ast.Name):
            raise Unsupported("assignment targets must be simple names")
        if target.id == "pytestmark":
            if "pytestmark" in seen:
                raise Unsupported("duplicate pytestmark assignment")
            seen.add("pytestmark")
            scan.pytestmark_skip = pytestmark_skips(value, scan.constants) or scan.pytestmark_skip
            return
        if target.id == "pytest_plugins" or target.id.startswith(("test", "Test", "__")) or target.id in seen:
            raise Unsupported("assignment may customize collection")
        seen.add(target.id)
        scan.constants[target.id] = literal(value, scan.constants)

    for node in tree.body:
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            if node.name in seen:
                raise Unsupported("duplicate definitions in module")
            seen.add(node.name)
            if node.name.startswith("pytest_"):
                raise Unsupported("pytest hooks are not supported: " + node.name)
            fixture_decorators = [d for d in node.decorator_list if is_fixture_decorator(d)]
            if fixture_decorators:
                if len(fixture_decorators) > 1:
                    raise Unsupported("multiple fixture decorators")
                for other in node.decorator_list:
                    if not is_fixture_decorator(other):
                        if is_conftest:
                            raise Unsupported("decorated fixtures are not supported in conftest")
                        validate_helper_decorator(other)
                spec = parse_fixture(fixture_decorators[0], node, scan.constants, method=False)
                if spec["name"] in scan.fixtures:
                    raise Unsupported("duplicate fixture names")
                scan.fixtures[spec["name"]] = spec
            else:
                scan.functions.append(node)
        elif isinstance(node, ast.ClassDef):
            if node.name in seen:
                raise Unsupported("duplicate definitions in module")
            seen.add(node.name)
            if node.name.startswith("pytest_"):
                raise Unsupported("pytest hooks are not supported: " + node.name)
            scan.classes[node.name] = node
        elif isinstance(node, ast.Assign):
            for target in node.targets:
                assign(target, node.value)
        elif isinstance(node, ast.AnnAssign):
            if node.value is not None:
                assign(node.target, node.value)
        elif isinstance(node, (ast.Import, ast.ImportFrom)):
            for alias in node.names:
                bound = alias.asname or alias.name.split(".")[0]
                if isinstance(node, ast.ImportFrom) and (alias.name == "*" or bound.startswith(("test", "Test", "pytest_"))):
                    raise Unsupported("imported tests are not supported")
        elif isinstance(node, ast.Expr) and isinstance(node.value, ast.Constant) and isinstance(node.value.value, str):
            pass  # docstring
        elif not isinstance(node, ast.Pass):
            raise Unsupported("executable module or class-level code requires pytest collection")
    return scan


class ClassContext:
    def __init__(self, node, prefix, collected):
        self.node = node
        self.prefix = prefix
        self.collected = collected
        self.skip = False
        self.fixtures = {}
        self.constants = {}
        self.pytestmark_skip = False
        self.methods = []
        self.nested = []


def local_bases(node, module_classes):
    if node.keywords:
        raise Unsupported("class keywords such as metaclass are not supported")
    for base in node.bases:
        if not isinstance(base, ast.Name) or base.id not in module_classes:
            raise Unsupported("test class inheritance outside the module is not supported")
    return node.bases


def has_local_init(node, module_classes, seen=None):
    seen = seen or set()
    if node.name in seen:
        raise Unsupported("inheritance cycles are not supported")
    seen = seen | {node.name}
    if any(isinstance(stmt, (ast.FunctionDef, ast.AsyncFunctionDef)) and stmt.name == "__init__"
           for stmt in node.body):
        return True
    return any(has_local_init(module_classes[base.id], module_classes, seen)
               for base in local_bases(node, module_classes))


def test_disabled(node, module_classes):
    stack, seen = [node], set()
    while stack:
        current = stack.pop()
        if current.name in seen:
            continue
        seen.add(current.name)
        for stmt in current.body:
            if isinstance(stmt, ast.Assign):
                for target in stmt.targets:
                    if isinstance(target, ast.Name) and target.id == "__test__":
                        if not isinstance(stmt.value, ast.Constant) or not isinstance(stmt.value.value, bool):
                            raise Unsupported("__test__ must be a literal bool")
                        if stmt.value.value is False:
                            return True
        stack.extend(module_classes[base.id] for base in local_bases(current, module_classes))
    return False


def inherited_methods(node, module_classes, seen=None):
    """Methods visible to a class; leftmost bases win, own methods override."""
    seen = seen or set()
    if node.name in seen:
        raise Unsupported("inheritance cycles are not supported")
    result = {}
    for base in reversed(local_bases(node, module_classes)):
        result.update(inherited_methods(module_classes[base.id], module_classes, seen | {node.name}))
    for stmt in node.body:
        if isinstance(stmt, (ast.FunctionDef, ast.AsyncFunctionDef)) and not any(
                is_fixture_decorator(decorator) for decorator in stmt.decorator_list):
            result[stmt.name] = stmt
    return result


def inherited_class_values(node, module_classes, contexts, attribute, seen=None):
    seen = seen or set()
    if node.name in seen:
        raise Unsupported("inheritance cycles are not supported")
    result = {}
    for base in reversed(local_bases(node, module_classes)):
        result.update(inherited_class_values(module_classes[base.id], module_classes, contexts,
                                             attribute, seen | {node.name}))
    result.update(getattr(contexts[id(node)], attribute))
    return result


def method_kind(function):
    for decorator in function.decorator_list:
        if isinstance(decorator, ast.Name) and decorator.id == "property":
            raise Unsupported("property test functions are not supported")
    if is_staticmethod(function):
        return "static"
    return "method"


def build_class(node, prefix, collected, collector, scan, contexts):
    """Recursively validate a class and record its collection context."""
    module_classes = scan.classes
    collected = collected and name_matches(node.name, collector.class_patterns)
    skip = False
    for decorator in node.decorator_list:
        mark = mark_decorator(decorator)
        if mark is None:
            raise Unsupported("class decorators other than pytest.mark.* are not supported")
        name, call = mark
        if name == "parametrize":
            raise Unsupported("parametrize marks on classes are not supported")
        if collected and mark_skips_tests(name, call, scan.constants):
            skip = True
    if collected:
        local_bases(node, module_classes)
        # pytest skips collection of classes with __init__ or __test__ = False.
        if has_local_init(node, module_classes) or test_disabled(node, module_classes):
            collected = False

    context = ClassContext(node, prefix, collected)
    context.skip = skip
    contexts[id(node)] = context

    seen = set()
    for stmt in node.body:
        if isinstance(stmt, (ast.FunctionDef, ast.AsyncFunctionDef)):
            name = stmt.name
            if name in seen:
                raise Unsupported("duplicate definitions in class")
            seen.add(name)
            if collected and name.startswith(("pytest_", "__")):
                raise Unsupported("unsupported method name in test class: " + name)
            fixture_decorators = [d for d in stmt.decorator_list if is_fixture_decorator(d)]
            if fixture_decorators:
                if len(fixture_decorators) > 1:
                    raise Unsupported("multiple fixture decorators")
                for other in stmt.decorator_list:
                    if not is_fixture_decorator(other):
                        validate_helper_decorator(other)
                spec = parse_fixture(fixture_decorators[0], stmt, {**scan.constants, **context.constants}, method=True)
                if spec["name"] in context.fixtures:
                    raise Unsupported("duplicate fixture names")
                if spec["autouse"] and spec["expansion"] is not None:
                    raise Unsupported("autouse parametrized class fixtures are not supported")
                context.fixtures[spec["name"]] = spec
                continue
            context.methods.append(stmt)
        elif isinstance(stmt, ast.ClassDef):
            if stmt.name in seen:
                raise Unsupported("duplicate definitions in class")
            seen.add(stmt.name)
            # pytest never descends into classes that are not collected.
            context.nested.append(build_class(stmt, prefix + node.name + "::", context.collected,
                                              collector, scan, contexts))
        elif isinstance(stmt, ast.Assign):
            for target in stmt.targets:
                if not isinstance(target, ast.Name):
                    raise Unsupported("assignment targets must be simple names")
                if target.id == "pytestmark":
                    if "pytestmark" in seen:
                        raise Unsupported("duplicate pytestmark assignment")
                    seen.add("pytestmark")
                    context.pytestmark_skip = pytestmark_skips(
                        stmt.value, {**scan.constants, **context.constants}) or context.pytestmark_skip
                    continue
                if target.id == "pytest_plugins" or target.id.startswith(("test", "Test", "__")) or target.id in seen:
                    if target.id == "__test__":
                        if not isinstance(stmt.value, ast.Constant) or not isinstance(stmt.value.value, bool):
                            raise Unsupported("__test__ must be a literal bool")
                        continue
                    raise Unsupported("assignment may customize collection")
                seen.add(target.id)
                context.constants[target.id] = literal(stmt.value, {**scan.constants, **context.constants})
        elif isinstance(stmt, ast.AnnAssign):
            if stmt.value is not None and isinstance(stmt.target, ast.Name):
                if stmt.target.id == "pytestmark":
                    raise Unsupported("annotated pytestmark is not supported")
                if stmt.target.id.startswith(("test", "Test", "__")) or stmt.target.id in seen:
                    raise Unsupported("assignment may customize collection")
                seen.add(stmt.target.id)
                context.constants[stmt.target.id] = literal(stmt.value, {**scan.constants, **context.constants})
        elif isinstance(stmt, (ast.Import, ast.ImportFrom)):
            for alias in stmt.names:
                bound = alias.asname or alias.name.split(".")[0]
                if isinstance(stmt, ast.ImportFrom) and (alias.name == "*" or bound.startswith(("test", "Test", "pytest_"))):
                    raise Unsupported("imported tests are not supported")
        elif isinstance(stmt, ast.Expr) and isinstance(stmt.value, ast.Constant) and isinstance(stmt.value.value, str):
            pass
        elif not isinstance(stmt, ast.Pass):
            raise Unsupported("executable class code requires pytest collection")

    resolve_poison(context.fixtures, [scan.fixtures] + collector.chain)
    return context


# --- test generation ---------------------------------------------------------

def function_tests(node, prefix, kind, fixtures, constants, autouse, collector):
    parametrize_groups = []
    parametrized_names = set()
    skipped = False
    for decorator in reversed(node.decorator_list):
        mark = mark_decorator(decorator)
        if mark is None:
            if isinstance(decorator, ast.Name) and decorator.id in {"staticmethod", "classmethod"}:
                continue
            raise Unsupported("decorators other than pytest.mark.* are not supported")
        name, call = mark
        if name == "parametrize":
            if call is None:
                raise Unsupported("bare parametrize mark is not supported")
            argnames, entries = parametrization(call, constants)
            if parametrized_names & set(argnames):
                raise Unsupported("duplicate parametrization argument")
            parametrized_names.update(argnames)
            # Fixture params come before parametrize parts in test ids.
            parametrize_groups.append([(case_id, case) for case_id, case, omitted in entries if not omitted])
        elif mark_skips_tests(name, call, constants):
            skipped = True
    if skipped:
        return

    if autouse is not None and autouse[0] in parametrized_names:
        raise Unsupported("autouse fixture name is also parametrized")
    arg_names = [argument.arg for argument in node.args.posonlyargs + node.args.args + node.args.kwonlyargs]
    if kind == "method" and arg_names:
        arg_names = arg_names[1:]

    def resolve(name):
        if name in fixtures:
            return fixtures[name]
        for level in collector.chain:
            if name in level:
                return level[name]
        return None

    fixture_groups = []
    covered = set()
    if autouse is not None and autouse[0] not in arg_names:
        covered.add(autouse[0])
        fixture_groups.append(autouse[1])
    for name in arg_names:
        if name in parametrized_names:
            continue  # direct parametrization wins over same-named fixtures
        spec = resolve(name)
        if spec is None:
            # Unknown fixture names are trusted to be plain: plugins are
            # allowlisted as inventory-neutral and missing fixtures do not
            # prevent collection.
            continue
        if spec["poison"]:
            raise Unsupported("test depends on a parametrized fixture chain")
        if spec["expansion"] is not None and name not in covered:
            covered.add(name)
            fixture_groups.append([(case_id, {spec["name"]: value}) for case_id, value in spec["expansion"]])

    groups = fixture_groups + parametrize_groups
    count = 1
    for group in groups:
        count *= len(group)
        if count > MAX_CASES:
            raise Unsupported("too many parameter combinations")
    for combination in itertools.product(*groups):
        name = prefix + node.name
        parameters = ""
        if groups:
            name += "[" + "-".join(case_id for case_id, _ in combination) + "]"
            values = {}
            for _, case in combination:
                for key, value in case.items():
                    values[key] = re.sub(r" at 0[xX][0-9a-fA-F]+", "", repr(value))
            parameters = json.dumps({"arguments": values, "metadata": {}}, sort_keys=True)
        yield {"name": name, "parameters": parameters}


class StaticCollector:
    def __init__(self, chain, function_patterns, class_patterns):
        self.chain = chain
        self.function_patterns = function_patterns
        self.class_patterns = class_patterns


def module_tests(tree, collector):
    scan = scan_module(tree, is_conftest=False)
    contexts = {}
    top_level = [build_class(node, "", True, collector, scan, contexts)
                  for node in scan.classes.values()]

    # Class fixtures join module fixtures for poison analysis.
    all_specs = dict(scan.fixtures)
    for context in contexts.values():
        for name, spec in context.fixtures.items():
            all_specs.setdefault(name, spec)
    resolve_poison(all_specs, collector.chain)
    for spec in all_specs.values():
        if spec["autouse"] and spec["poison"]:
            raise Unsupported("autouse fixtures depending on parametrized fixtures are not supported")

    # A module-level autouse parametrized fixture applies to every test.
    autouse = None
    for name, spec in scan.fixtures.items():
        if spec["autouse"] and spec["expansion"] is not None:
            if autouse is not None:
                raise Unsupported("multiple autouse parametrized fixtures are not supported")
            autouse = (name, [(case_id, {name: value}) for case_id, value in spec["expansion"]])

    if scan.pytestmark_skip:
        return

    def emit_class(context):
        if not context.collected or context.skip or context.pytestmark_skip:
            return
        fixtures = {**scan.fixtures,
                    **inherited_class_values(context.node, scan.classes, contexts, "fixtures")}
        constants = {**scan.constants,
                     **inherited_class_values(context.node, scan.classes, contexts, "constants")}
        methods = inherited_methods(context.node, scan.classes)
        for name in sorted(methods):
            method = methods[name]
            if name_matches(name, collector.function_patterns):
                yield from function_tests(method, context.prefix + context.node.name + "::",
                                          method_kind(method), fixtures, constants, autouse, collector)
            else:
                for decorator in method.decorator_list:
                    validate_helper_decorator(decorator)
        for nested in context.nested:
            yield from emit_class(nested)

    for function in scan.functions:
        if name_matches(function.name, collector.function_patterns):
            kind = "static" if is_staticmethod(function) else "plain"
            yield from function_tests(function, "", kind, scan.fixtures, scan.constants, autouse, collector)
        else:
            for decorator in function.decorator_list:
                validate_helper_decorator(decorator)
    for context in top_level:
        yield from emit_class(context)


def analyze_conftest(path, chain):
    with tokenize.open(path) as source:
        tree = ast.parse(source.read(), filename=str(path))
    scan = scan_module(tree, is_conftest=True)
    contexts = {}
    collector = StaticCollector(chain, [], [])
    for node in scan.classes.values():
        build_class(node, "", False, collector, scan, contexts)
    for context in contexts.values():
        if context.fixtures:
            raise Unsupported("fixtures in conftest classes are not supported")
    for function in scan.functions:
        for decorator in function.decorator_list:
            if mark_decorator(decorator) is None:
                raise Unsupported("decorated helpers are not supported in conftest")
    resolve_poison(scan.fixtures, chain)
    for spec in scan.fixtures.values():
        if spec["autouse"] and (spec["expansion"] is not None or spec["poison"]):
            raise Unsupported("autouse parametrized conftest fixtures are not supported")
    return scan.fixtures


def discover(manifest):
    check_options(manifest.get("options") or [], allow_paths=True)
    check_environment()
    root = Path.cwd().resolve()
    files = []
    directories = {root, *root.parents}
    for filename in manifest["files"]:
        path = Path(filename)
        absolute = Path(os.path.abspath(path))
        if absolute != path.resolve() or not absolute.is_relative_to(root):
            raise Unsupported("symlinks or files outside the working directory")
        if not path.is_file() or path.suffix != ".py":
            raise Unsupported("test selection contains a non-Python file")
        directories.update(absolute.parent.parents)
        directories.add(absolute.parent)
        files.append(absolute)
    # Existing positional paths in --command must not widen the discovery
    # root or bring uninspected collectors into the process.
    for option in manifest.get("options") or []:
        if not option.startswith("-") and Path(option).exists() and Path(option).resolve() not in files:
            raise Unsupported("command positional paths must be among the selected test files")

    patterns = None
    conftest_by_dir = {}
    for directory in sorted(directories, key=lambda d: len(d.parts)):
        found = check_config(directory, root)
        if directory == root and patterns is None:
            patterns = found
        conftest = directory / "conftest.py"
        if not conftest.exists():
            continue
        # Conftests of ancestor directories are visible for fixture
        # shadowing and poison analysis.
        visible = [conftest_by_dir[ancestor]
                   for ancestor in [directory] + list(directory.parents)
                   if ancestor != directory and ancestor in conftest_by_dir]
        conftest_by_dir[directory] = analyze_conftest(conftest, visible)

    function_patterns = (patterns or {}).get("python_functions") or DEFAULT_FUNCTION_PATTERNS
    class_patterns = (patterns or {}).get("python_classes") or DEFAULT_CLASS_PATTERNS

    tests = []
    for path in files:
        # Nearest conftests shadow shallower ones, matching pytest.
        chain = [conftest_by_dir[directory]
                 for directory in [path.parent] + list(path.parent.parents)
                 if directory in conftest_by_dir]
        collector = StaticCollector(chain, function_patterns, class_patterns)
        with tokenize.open(path) as source:
            tree = ast.parse(source.read(), filename=str(path))
        relative = path.relative_to(root)
        module = ".".join(relative.parts[:-1])
        for test in module_tests(tree, collector):
            test.update(module=module, suite=path.name, suiteSourceFile=relative.as_posix())
            tests.append(test)
            if len(tests) > MAX_CASES:
                raise Unsupported("too many statically discovered tests")
    return tests


try:
    with open(sys.argv[1], encoding="utf-8") as source:
        inventory = discover(json.load(source))
    print(json.dumps({"tests": inventory}))
except Exception as error:
    # Never expose an inventory from the files processed before an uncertainty.
    print(json.dumps({"reason": str(error) or type(error).__name__}))
