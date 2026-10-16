"""Experimental, conservative pytest inventory without importing pytest or tests.

This is not a Python evaluator. Unsupported syntax/configuration invalidates the
entire inventory and asks Go to use normal collection. Keep identity behavior
covered by the tracer-backed compatibility tests when extending this subset.
"""

import ast
from collections import Counter, defaultdict
import configparser
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


SAFE_OPTIONS = {
    "--ddtrace", "--quiet", "--verbose", "-s", "--no-header", "--no-summary",
    "--disable-warnings", "--disable-pytest-warnings", "--showlocals", "-l",
    "--full-trace",
}
VALUE_OPTIONS = {
    "--color": {"yes", "no", "auto"},
    "--code-highlight": {"yes", "no"},
    "--capture": {"fd", "sys", "no", "tee-sys"},
    "--tb": {"auto", "long", "short", "line", "native", "no"},
}
MAX_CASES = 100_000


def check_options(options, allow_paths=False):
    positional = False
    iterator = iter(options)
    for option in iterator:
        if not positional:
            if option in SAFE_OPTIONS or re.fullmatch(r"-[qv]+", option):
                continue
            if re.fullmatch(r"-r[aAfEsxXpP]+", option):
                continue
            key, separator, value = option.partition("=")
            if key in VALUE_OPTIONS:
                if not separator:
                    value = next(iterator, "")
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
        # Inspect entry point metadata without importing any plugin. Include
        # PYTHONPATH distributions even though the helper runs in isolated mode.
        paths = [str(Path.cwd())] + os.environ.get("PYTHONPATH", "").split(os.pathsep) + sys.path
        for distribution in importlib.metadata.distributions(path=[p for p in paths if p]):
            if any(ep.group == "pytest11" for ep in distribution.entry_points):
                if distribution.metadata["Name"].lower() != "ddtrace":
                    raise Unsupported("installed pytest plugin may customize collection: " + distribution.metadata["Name"])


def check_config(directory, root):
    """Reject ambiguous roots and all configuration outside a small allowlist."""
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
        if parser.has_section(section):
            check_config_values(dict(parser.items(section)), directory, root)
        elif name in ("pytest.ini", ".pytest.ini") and directory != root:
            raise Unsupported("pytest root outside the working directory")
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
                check_config_values(table["ini_options"], directory, root)


def check_config_values(values, directory, root):
    if directory != root:
        raise Unsupported("pytest configuration outside the working directory")
    for key, value in values.items():
        if key in {"testpaths", "python_files", "markers", "filterwarnings", "minversion"}:
            continue
        if key == "addopts":
            check_options(shlex.split(value) if isinstance(value, str) else value)
            continue
        if key in {"python_classes", "python_functions"}:
            expected = ["Test"] if key == "python_classes" else ["test"]
            if (shlex.split(value) if isinstance(value, str) else value) == expected:
                continue
        raise Unsupported("unsupported pytest configuration key: " + key)


def literal(node):
    value = ast.literal_eval(node)
    # Sets have unstable repr ordering; bytes/complex IDs are outside this subset.
    def supported(value):
        if value is None or type(value) in (str, int, float, bool):
            return True
        if type(value) in (tuple, list):
            return all(supported(v) for v in value)
        if type(value) is dict:
            return all(supported(k) and supported(v) for k, v in value.items())
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


def check_mark(node):
    # The tracer can omit marked/skipped cases from its discovery report. Do not
    # infer its inventory from collection node IDs alone.
    raise Unsupported("non-parametrize decorators or parameter marks require pytest collection")


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


def parametrization(decorator):
    if not isinstance(decorator, ast.Call) or dotted_name(decorator.func) != "pytest.mark.parametrize":
        check_mark(decorator)
        return None
    arguments = dict(zip(("argnames", "argvalues", "indirect", "ids", "scope"), decorator.args))
    if len(decorator.args) > 5:
        raise Unsupported("unsupported parametrization arguments")
    for keyword in decorator.keywords:
        if keyword.arg not in {"argnames", "argvalues", "ids", "indirect", "scope"} or keyword.arg in arguments:
            raise Unsupported("unsupported parametrization arguments")
        arguments[keyword.arg] = keyword.value
    if "indirect" in arguments:
        indirect = literal(arguments["indirect"])
        if indirect is not False and not (isinstance(indirect, (list, tuple)) and not indirect):
            raise Unsupported("indirect parametrization requires pytest collection")
    if "scope" in arguments and literal(arguments["scope"]) not in (None, "function"):
        raise Unsupported("non-function parametrization scope")
    names = literal(arguments["argnames"])
    string_names = isinstance(names, str)
    comma_names = string_names and "," in names
    names = [name.strip() for name in names.split(",") if name.strip()] if string_names else names
    if comma_names and len(names) == 1:
        raise Unsupported("single-argument trailing comma semantics differ across pytest versions")
    single = string_names and len(names) == 1
    if not isinstance(names, (list, tuple)) or not names or any(not isinstance(n, str) or not n.isidentifier() or n == "request" for n in names) or len(set(names)) != len(names):
        raise Unsupported("unsupported parameter names")
    values = arguments["argvalues"]
    if not isinstance(values, (ast.List, ast.Tuple)) or not values.elts:
        raise Unsupported("parameter values must be a nonempty literal list or tuple")
    if len(values.elts) > MAX_CASES:
        raise Unsupported("too many parameter cases")
    explicit_ids = literal(arguments["ids"]) if "ids" in arguments else None
    if explicit_ids is not None and (not isinstance(explicit_ids, (list, tuple)) or len(explicit_ids) != len(values.elts)):
        raise Unsupported("unsupported parameter IDs")
    cases, ids = [], []
    for index, entry in enumerate(values.elts):
        case_id = None
        if isinstance(entry, ast.Call) and dotted_name(entry.func) == "pytest.param":
            row = [literal(v) for v in entry.args]
            keywords = set()
            for keyword in entry.keywords:
                if keyword.arg in keywords:
                    raise Unsupported("duplicate pytest.param keyword")
                keywords.add(keyword.arg)
                if keyword.arg == "id":
                    case_id = literal(keyword.value)
                    if case_id is not None and not isinstance(case_id, str):
                        raise Unsupported("pytest.param ID must be a string")
                elif keyword.arg == "marks":
                    marks = keyword.value.elts if isinstance(keyword.value, (ast.List, ast.Tuple)) else [keyword.value]
                    for mark in marks:
                        check_mark(mark)
                else:
                    raise Unsupported("unsupported pytest.param argument")
        else:
            row = literal(entry)
            row = [row] if single else row
        if not isinstance(row, (list, tuple)) or len(row) != len(names):
            raise Unsupported("parameter value arity mismatch")
        if case_id is None and explicit_ids is not None:
            case_id = explicit_ids[index]
            if case_id is not None:
                case_id = escaped_id(case_id)
                if case_id is None:
                    raise Unsupported("unsupported parameter ID type")
        if case_id is None:
            parts = [escaped_id(value) for value in row]
            case_id = "-".join(part if part is not None else name + str(index)
                               for part, name in zip(parts, names))
        elif isinstance(entry, ast.Call):
            case_id = escaped_id(case_id)
        cases.append(dict(zip(names, row)))
        ids.append(case_id)
    return list(zip(unique_ids(ids), cases))


def function_tests(node, prefix, method):
    groups = []
    for decorator in reversed(node.decorator_list):
        group = parametrization(decorator)
        if group is not None:
            groups.append(group)
    count = 1
    parameter_names = set()
    for group in groups:
        names = set(group[0][1])
        if parameter_names & names:
            raise Unsupported("duplicate parametrization argument")
        parameter_names.update(names)
        count *= len(group)
        if count > MAX_CASES:
            raise Unsupported("too many parameter combinations")
    args = node.args
    positional = args.posonlyargs + args.args
    required = positional[:len(positional) - len(args.defaults)]
    if method:
        required = required[1:]
    required += [arg for arg, default in zip(args.kwonlyargs, args.kw_defaults) if default is None]
    if set(arg.arg for arg in required) != parameter_names:
        raise Unsupported("test requires fixtures or invalid parametrization")
    for default in args.defaults + [value for value in args.kw_defaults if value is not None]:
        literal(default)
    for combination in itertools.product(*groups):
        name = prefix + node.name
        parameters = ""
        if groups:
            name += "[" + "-".join(case_id for case_id, _ in combination) + "]"
            values = {key: re.sub(r" at 0[xX][0-9a-fA-F]+", "", repr(value))
                      for _, case in combination for key, value in case.items()}
            parameters = json.dumps({"arguments": values, "metadata": {}}, sort_keys=True)
        yield {"name": name, "parameters": parameters}


def scope_tests(body, prefix="", method=False):
    seen = set()
    for node in body:
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            if node.name in seen or node.name.startswith(("pytest_", "__")):
                raise Unsupported("dynamic collection or duplicate definitions")
            seen.add(node.name)
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            annotations = [arg.annotation for arg in node.args.posonlyargs + node.args.args + node.args.kwonlyargs]
            annotations.append(node.returns)
            if any(annotation is not None and not isinstance(annotation, (ast.Name, ast.Constant)) for annotation in annotations):
                raise Unsupported("dynamic function annotations require pytest collection")
            if node.name.startswith("test"):
                yield from function_tests(node, prefix, method)
            else:
                if node.decorator_list:
                    raise Unsupported("decorated helper or fixture requires pytest collection")
                for default in node.args.defaults + [value for value in node.args.kw_defaults if value is not None]:
                    literal(default)
        elif isinstance(node, ast.ClassDef):
            if node.bases or node.keywords or node.decorator_list or not node.name.startswith("Test"):
                raise Unsupported("unsupported test class or inheritance")
            yield from scope_tests(node.body, prefix + node.name + "::", True)
        elif isinstance(node, (ast.Import, ast.ImportFrom)):
            for alias in node.names:
                bound = alias.asname or alias.name.split(".")[0]
                if isinstance(node, ast.ImportFrom) and (alias.name == "*" or bound.startswith(("test", "Test", "pytest_"))):
                    raise Unsupported("imported tests are not supported")
        elif isinstance(node, ast.Assign):
            for target in node.targets:
                if not isinstance(target, ast.Name) or target.id.startswith(("test", "Test", "__")) or target.id in {"pytestmark", "pytest_plugins"} or target.id in seen:
                    raise Unsupported("assignment may customize collection")
                seen.add(target.id)
            literal(node.value)
        elif isinstance(node, ast.Expr) and isinstance(node.value, ast.Constant) and isinstance(node.value.value, str):
            pass  # docstring
        elif not isinstance(node, ast.Pass):
            raise Unsupported("executable module or class-level code requires pytest collection")


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
    for directory in sorted(directories):
        if (directory / "conftest.py").exists():
            raise Unsupported("conftest.py requires pytest collection")
        check_config(directory, root)
    tests = []
    for path in files:
        with tokenize.open(path) as source:
            tree = ast.parse(source.read(), filename=str(path))
        relative = path.relative_to(root)
        module = ".".join(relative.parts[:-1])
        for test in scope_tests(tree.body):
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
