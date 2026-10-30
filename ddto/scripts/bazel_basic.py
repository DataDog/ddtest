"""Run the basic Bazel test of ddtest instrument.

It copies testdata/bazel, a minimal test-only rule and a workspace, to a
temporary directory and runs Bazel there with the ddtest executable under
test, in Bzlmod or WORKSPACE mode. It then checks:
  - every test's status and suite, Testify's suites and goleak's leak;
  - the span copies of dd-trace-go v2 and v1.74, and one session per test
    binary, which the CI guards leave to Mini;
  - a tool built with the instrumented SDK, in the exec configuration and as
    data, which imports testing without Mini;
  - a go_binary that keeps the default SDK and has none of ddtest's code;
  - the race detector's configuration;
  - no compiler temporary path in the instrumented binaries;
  - the same test and span events as ddtest instrument go test outside Bazel;
  - in Bzlmod, Mini's Go API from go_deps with the override;
  - with --buildifier, the format and lint of Mini's generated BUILD files
    and of the fixture's Starlark.

usage: python scripts/bazel_basic.py --ddtest PATH --goroot GOROOT --mode bzlmod|workspace
"""

import argparse
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile

FIXTURE = Path(__file__).resolve().parent.parent / "testdata" / "bazel"

# Bazel targets, their Go packages and the tests that they report, keyed by
# suite and name.
EXPECTED = {
    "//basic:basic_test": ("example.com/basic/basic", {
        ("basic_test.go", "TestPass"): "pass",
        ("basic_test.go", "TestFail"): "fail",
        ("basic_test.go", "TestSkip"): "skip",
        ("basic_test.go", "TestSub"): "pass",
        ("basic_test.go", "TestSub/a"): "pass",
        ("basic_test.go", "TestSub/b"): "pass",
        ("basic_test.go", "TestParallel"): "pass",
        ("basic_test.go", "TestParallel/x"): "pass",
        ("basic_test.go", "TestParallel/y"): "pass",
    }),
    "//suite:suite_test": ("example.com/basic/suite", {
        ("suite_test.go", "TestExampleSuite"): "fail",
        ("suite_test.go/ExampleSuite", "TestExampleSuite/TestPass"): "pass",
        ("suite_test.go/ExampleSuite", "TestExampleSuite/TestFail"): "fail",
        ("suite_test.go/ExampleSuite", "TestExampleSuite/TestSkip"): "skip",
        ("suite_test.go", "TestRegular"): "pass",
    }),
    # goleak reports the test's leak, and none of Mini's goroutines.
    "//leak:leak_test": ("example.com/basic/leak", {
        ("leak_test.go", "TestNoLeak"): "pass",
        ("leak_test.go", "TestLeaks"): "pass",
        ("verify_test.go", "TestVerifyNoneClean"): "fail",
    }),
    "//traced:traced_test": ("example.com/basic/traced", {
        ("traced_test.go", "TestWork"): "pass",
        ("traced_test.go", "TestManualSpan"): "pass",
    }),
    "//tracedv1:tracedv1_test": ("example.com/basic/tracedv1", {
        ("tracedv1_test.go", "TestManualSpanV1"): "pass",
    }),
    "//tools:tools_test": ("example.com/basic/tools", {
        ("tools_test.go", "TestExecTool"): "pass",
        ("tools_test.go", "TestDataTool"): "pass",
    }),
}

# The application's spans that Mini copies, by the test whose context they
# come from.
EXPECTED_SPANS = {
    "//traced:traced_test": {
        ("TestWork", "work.parent", "parent"),
        ("TestWork", "work.child", "child"),
        ("TestManualSpan", "manual.parent", "manual.parent"),
        ("TestManualSpan", "manual.child", "manual.child"),
    },
    "//tracedv1:tracedv1_test": {
        ("TestManualSpanV1", "manual.v1.parent", "manual.v1.parent"),
        ("TestManualSpanV1", "manual.v1.child", "manual.v1.child"),
    },
}

# Test metadata that Bazel and ddtest instrument go test must agree on. Paths,
# commands and session names differ by design (see docs/bazel-plan.md).
COMPARED_META = ("test.status", "test.framework", "test.type", "test.skip_reason", "error.type", "error.message")

RACE_TARGETS = ("//basic:basic_test", "//suite:suite_test")

# Gazelle limits a module's internal packages to its own repository. go_deps
# widens that for the nested modules that import them; a WORKSPACE says so
# itself, as one with dd-trace-go v2 does.
WORKSPACE_VISIBILITY = {
    "io_opentelemetry_go_collector_pdata": "@io_opentelemetry_go_collector_pdata_pprofile//:__subpackages__",
}

# A test that uses Mini's Go API from go_deps, whose root module overrides
# go_deps' copy with the generated Mini. Without the override, rules_go links
# two copies with no error; rules_test_optimization's rule checks that.
API = {"//api:api_test": ("example.com/basic/api", {("api_test.go", "TestContext"): "pass"})}

# The prefix of the compiler mode's temporary directory and files.
COMPILER_TEMP = b"ddtest-instrument-"


class Failure(Exception):
    pass


def check(condition, message):
    if not condition:
        raise Failure(message)


class Bazel:
    def __init__(self, args, workspace):
        self.workspace = workspace
        self.startup = [args.bazel]
        if args.output_user_root:
            self.startup.append("--output_user_root=" + args.output_user_root)
        self.flags = [
            "--repo_env=DDTEST_EXECUTABLE=" + str(Path(args.ddtest).resolve()),
            "--repo_env=DDTEST_GOROOT=" + str(Path(args.goroot).resolve()),
            # Nothing is written next to the workspace's files.
            "--symlink_prefix=/",
            "--lockfile_mode=off",
        ]
        if args.mode == "workspace":
            self.flags.append("--config=workspace")
        if args.repository_cache:
            self.flags.append("--repository_cache=" + args.repository_cache)
        self.env = dict(os.environ)
        # go_sdk.host() and go_register_toolchains(go_version = "host") take
        # the go command on PATH.
        self.env["PATH"] = str(Path(args.goroot) / "bin") + os.pathsep + self.env["PATH"]

    def run(self, command, *args, codes=(0,), capture=False):
        argv = self.startup + [command] + (self.flags if command != "shutdown" else []) + list(args)
        print("+", " ".join(argv), flush=True)
        result = subprocess.run(argv, cwd=self.workspace, env=self.env, text=True,
                                stdout=subprocess.PIPE if capture else None)
        check(result.returncode in codes, "%s exited with %d, want %s" % (" ".join(argv), result.returncode, codes))
        return result.stdout if capture else result.returncode

    def info(self, key, extra=()):
        return self.run("info", *extra, key, capture=True).strip()

    def files(self, *targets, extra=()):
        out = self.run("cquery", *extra, "--output=files", " + ".join(targets), capture=True)
        root = Path(self.info("execution_root", extra))
        return [root / line for line in out.split()]


def payload_events(directory):
    events = []
    for path in sorted(Path(directory).glob("payloads/tests/*.json")):
        events.extend(json.loads(path.read_text())["events"])
    return events


def summarize(events):
    """Tests by (suite, name), with compared metadata, and copied spans by test."""
    tests, spans, sessions = {}, set(), 0
    by_span = {}
    for event in events:
        content = event["content"]
        meta = content.get("meta") or {}
        if event["type"] == "test":
            key = (meta.get("test.suite"), meta.get("test.name"))
            check(key not in tests, "test %s reported twice" % (key,))
            tests[key] = {name: meta.get(name) for name in COMPARED_META}
            by_span[content.get("trace_id")] = meta.get("test.name")
        elif event["type"] == "test_session_end":
            sessions += 1
    for event in events:
        content = event["content"]
        if event["type"] == "span":
            meta = content.get("meta") or {}
            check(meta.get("_dd.origin") == "ciapp-test", "span %s has origin %s" % (content.get("name"), meta.get("_dd.origin")))
            spans.add((by_span.get(content.get("trace_id")), content.get("name"), content.get("resource")))
    return tests, spans, sessions


def check_bazel_events(testlogs, targets=EXPECTED):
    summaries = {}
    for target, (_, expected) in targets.items():
        package, name = target[2:].split(":")
        outputs = Path(testlogs) / package / name / "test.outputs"
        tests, spans, sessions = summarize(payload_events(outputs))
        statuses = {key: meta["test.status"] for key, meta in tests.items()}
        check(statuses == expected, "%s: tests %s, want %s" % (target, statuses, expected))
        # dd-trace-go's own CI Visibility would add a session of its own.
        check(sessions == 1, "%s: %d sessions, want 1" % (target, sessions))
        check(spans == EXPECTED_SPANS.get(target, set()), "%s: spans %s, want %s" % (target, spans, EXPECTED_SPANS.get(target, set())))
        summaries[target] = (tests, spans)
    return summaries


def check_no_compiler_temp(binaries):
    for binary in binaries:
        check(COMPILER_TEMP not in binary.read_bytes(), "%s names the compiler's temporary files" % binary)


def check_plain_binary(goroot, plain, instrumented):
    """The plain binary has none of ddtest's symbols; the instrumented one has them."""
    go = str(Path(goroot) / "bin" / "go")

    def symbols(binary):
        return subprocess.run([go, "tool", "nm", str(binary)], check=True, text=True, stdout=subprocess.PIPE).stdout

    marks = ("ddtest/ddto", "__ddto", "__dd_ci")
    found = [line for line in symbols(plain).splitlines() if any(mark in line for mark in marks)]
    check(not found, "the plain go_binary has ddtest's symbols: %s" % found[:5])
    check(any(mark in symbols(instrumented) for mark in marks), "the instrumented test has none of ddtest's symbols")


def compare_with_cli(args, workspace, summaries):
    """ddtest instrument go test reports the same tests and spans outside Bazel."""
    outputs = Path(workspace).parent / "cli-outputs"
    env = dict(os.environ)
    env.update({
        "PATH": str(Path(args.goroot) / "bin") + os.pathsep + env["PATH"],
        "GOTOOLCHAIN": "local",
        "GOFLAGS": "-mod=mod",
        "DD_SERVICE": "ddtest-bazel-basic",
        "DD_CIVISIBILITY_ENABLED": "true",
        "DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES": "true",
        "DD_TEST_OPTIMIZATION_MANIFEST_FILE": str(Path(workspace) / "testdata" / "manifest.txt"),
        "TEST_UNDECLARED_OUTPUTS_DIR": str(outputs),
    })
    # The fixture's go.mod requires a published ddto for go_deps, whose runtime
    # ddtest instrument would use outside Bazel; this revision's goes in its
    # place, as Bazel's Mini repository comes from this revision too.
    go = str(Path(args.goroot) / "bin" / "go")
    subprocess.run([go, "mod", "edit", "-replace=github.com/DataDog/ddtest/ddto=" + str(FIXTURE.parent.parent)],
                   cwd=workspace, env=env, check=True)
    packages = ["./" + target[2:].split(":")[0] for target in EXPECTED if target != "//tools:tools_test"]
    argv = [args.ddtest, "instrument", "go", "test", "-count=1"] + packages
    print("+", " ".join(argv), flush=True)
    code = subprocess.run(argv, cwd=workspace, env=env).returncode
    check(code == 1, "ddtest instrument go test exited with %d, want 1" % code)
    tests, spans, _ = summarize(payload_events(outputs))
    # Bazel compiles with -trimpath, so its messages name sources relative to
    # the workspace (docs/bazel-plan.md); go test names them absolutely.
    # Goroutine numbers depend on what the process started before the test.
    def message(text):
        for root in {str(workspace), os.path.realpath(workspace)}:
            text = text.replace(root + os.sep, "")
        return re.sub(r"([Gg]oroutine) \d+", r"\1 N", text)

    def normalized(tests):
        return {key: dict(meta, **{"error.message": message(meta["error.message"] or "")}) for key, meta in tests.items()}

    tests = normalized(tests)
    for target, (bazel_tests, _) in summaries.items():
        if target == "//tools:tools_test":
            continue
        bazel_tests = normalized(bazel_tests)
        names = set(EXPECTED[target][1])
        cli_tests = {key: meta for key, meta in tests.items() if key in names}
        check(cli_tests == bazel_tests, "%s: Bazel reports\n%s\nddtest instrument go test reports\n%s" % (target, bazel_tests, cli_tests))
    # check_bazel_events found these spans in Bazel.
    expected = set().union(*EXPECTED_SPANS.values())
    check(spans == expected, "ddtest instrument go test copies spans %s, Bazel %s" % (spans, expected))


def check_api(bazel):
    """Mini's Go API from go_deps, with the root module's override."""
    bazel.run("test", *API)
    check_bazel_events(bazel.info("bazel-testlogs"), API)
    # A plain build that uses the API links the generated Mini.
    bazel.run("build", "//apibin")
    out = subprocess.run([str(bazel.files("//apibin")[0])], check=True, text=True, stdout=subprocess.PIPE).stdout
    check(out == "propagation: true\n", "apibin printed %r" % out)


def check_buildifier(args, work):
    """Mini's generated BUILD files, and the fixture's, pass buildifier."""
    mini = work / "mini"
    subprocess.run([args.ddtest, "instrument", "bazel", "--mini", str(mini)], check=True)
    files = sorted(str(path) for root in (mini, FIXTURE) for pattern in ("*.bazel", "*.bzl") for path in root.rglob(pattern))
    result = subprocess.run([args.buildifier, "--mode=check", "--lint=warn"] + files, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    check(result.returncode == 0, "buildifier:\n" + result.stdout)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--ddtest", required=True, help="the ddtest executable under test")
    parser.add_argument("--goroot", required=True, help="the Go SDK to instrument and build with")
    parser.add_argument("--mode", required=True, choices=("bzlmod", "workspace"))
    parser.add_argument("--bazel", default="bazelisk")
    parser.add_argument("--output-user-root")
    parser.add_argument("--repository-cache")
    parser.add_argument("--work", help="a directory for the copy; a temporary one by default")
    parser.add_argument("--buildifier", help="also check the BUILD files with this buildifier")
    args = parser.parse_args()
    args.ddtest = str(Path(args.ddtest).resolve())

    temporary, bazel = None, None
    if args.work:
        work = Path(args.work).resolve()
        work.mkdir(parents=True, exist_ok=True)
    else:
        temporary = tempfile.TemporaryDirectory(prefix="ddtest-bazel-basic-")
        work = Path(temporary.name)
    try:
        if args.buildifier:
            check_buildifier(args, work)
        shutil.copytree(FIXTURE, work / "bazel")
        workspace = work / "bazel" / "ws"
        bazel = Bazel(args, workspace)
        if args.mode == "workspace":
            # WORKSPACE mode declares go_deps' repositories from go.mod.
            (workspace / "deps.bzl").write_text('def go_dependencies():\n    pass\n')
            bazel.run("run", "@bazel_gazelle//cmd/gazelle", "--", "update-repos", "-from_file=go.mod",
                      "-to_macro=deps.bzl%go_dependencies", "-build_file_proto_mode=disable_global",
                      "-build_file_generation=clean", "-prune")
            deps = (workspace / "deps.bzl").read_text()
            for name, label in WORKSPACE_VISIBILITY.items():
                anchor = '        name = "%s",\n' % name
                check(anchor in deps, "deps.bzl declares no " + name)
                deps = deps.replace(anchor, anchor + '        build_directives = ["gazelle:go_visibility %s"],\n' % label)
            (workspace / "deps.bzl").write_text(deps)
        # Bazel exits with 3 when tests fail, as some do on purpose.
        bazel.run("test", "--keep_going", "//...", codes=(3,))
        summaries = check_bazel_events(bazel.info("bazel-testlogs"))
        # rules_go strips binaries in fastbuild; relinking keeps the symbols
        # and the debug information that the checks read.
        unstripped = ("--strip=never",)
        bazel.run("build", *unstripped, *EXPECTED, "//plainbin")
        check_no_compiler_temp(bazel.files(*EXPECTED, extra=unstripped))
        check_plain_binary(args.goroot, bazel.files("//plainbin", extra=unstripped)[0],
                           bazel.files("//traced:traced_test", extra=unstripped)[0])
        # With -race, the compiler mode also edits Mini's packages.
        race = ("--@io_bazel_rules_go//go/config:race",)
        bazel.run("test", *race, *RACE_TARGETS, codes=(3,))
        check_bazel_events(bazel.info("bazel-testlogs", race), {target: EXPECTED[target] for target in RACE_TARGETS})
        bazel.run("build", *race, *unstripped, *RACE_TARGETS)
        check_no_compiler_temp(bazel.files(*RACE_TARGETS, extra=race + unstripped))
        if args.mode == "bzlmod":
            # override_repo exists only in Bzlmod.
            check_api(bazel)
        compare_with_cli(args, workspace, summaries)
    except Failure as failure:
        print("FAIL:", failure, file=sys.stderr)
        return 1
    finally:
        if bazel:
            bazel.run("shutdown", codes=(0, 1, 2, 36, 37))
        if temporary:
            temporary.cleanup()
    print("The basic Bazel test passed.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
