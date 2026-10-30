"""Repository rules that run the host's `ddtest instrument bazel`.

The ddtest executable under test and the Go SDK to instrument come from
--repo_env=DDTEST_EXECUTABLE and --repo_env=DDTEST_GOROOT.
"""

load("@io_bazel_rules_go//go:deps.bzl", "go_wrap_sdk")

def _ddtest(ctx):
    executable = ctx.getenv("DDTEST_EXECUTABLE")
    if not executable:
        fail("set --repo_env=DDTEST_EXECUTABLE to the ddtest executable under test")
    return ctx.path(executable)

def _instrument_bazel(ctx, args):
    command = [_ddtest(ctx), "instrument", "bazel"] + args
    result = ctx.execute(command)
    if result.return_code:
        fail("%s exited with %d:\n%s%s" % (" ".join([str(arg) for arg in command]), result.return_code, result.stdout, result.stderr))

def _sdk_files_impl(ctx):
    goroot = ctx.getenv("DDTEST_GOROOT")
    if not goroot:
        fail("set --repo_env=DDTEST_GOROOT to the Go SDK to instrument")
    _instrument_bazel(ctx, ["--goroot=" + goroot, "--compiler=" + str(_ddtest(ctx)), "sdk"])
    ctx.file("BUILD.bazel", 'exports_files(["sdk/DDTEST_INSTRUMENT"])\n')

_sdk_files = repository_rule(implementation = _sdk_files_impl)

def _mini_impl(ctx):
    _instrument_bazel(ctx, ["--mini", "--rules-go=io_bazel_rules_go", "."])

ddtest_mini = repository_rule(implementation = _mini_impl)

_GOOS = {"linux": "linux", "mac os x": "darwin"}
_GOARCH = {"amd64": "amd64", "x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}
_OS = {"linux": "linux", "darwin": "macos"}
_CPU = {"amd64": "x86_64", "arm64": "aarch64"}

def _toolchains_impl(ctx):
    goos = _GOOS.get(ctx.os.name.lower())
    goarch = _GOARCH.get(ctx.os.arch)
    if not goos or not goarch:
        fail("the basic Bazel test runs on Linux and macOS hosts, not %s/%s" % (ctx.os.name, ctx.os.arch))
    constraints = '["@platforms//os:%s", "@platforms//cpu:%s"]' % (_OS[goos], _CPU[goarch])
    ctx.file("BUILD.bazel", """# The instrumented SDK, only when the test rule sets sdk_name to "ddto".
config_setting(
    name = "sdk_name_ddto",
    flag_values = {{"@io_bazel_rules_go//go/toolchain:sdk_name": "ddto"}},
)

toolchain(
    name = "go_{platform}",
    exec_compatible_with = {constraints},
    target_compatible_with = {constraints},
    target_settings = [":sdk_name_ddto"],
    toolchain = "@ddtest_go_sdk//:go_{platform}-impl",
    toolchain_type = "@io_bazel_rules_go//go:toolchain",
)
""".format(platform = goos + "_" + goarch, constraints = constraints))

_toolchains = repository_rule(implementation = _toolchains_impl)

def ddtest_repositories():
    """Declares the instrumented SDK, its toolchains and Mini's repository."""
    _sdk_files(name = "ddtest_go_sdk_files")

    # register_toolchains = False: rules_go's own declarations for a wrapped
    # SDK also match the default, empty sdk_name.
    go_wrap_sdk(
        name = "ddtest_go_sdk",
        root_file = "@ddtest_go_sdk_files//:sdk/DDTEST_INSTRUMENT",
        register_toolchains = False,
    )
    _toolchains(name = "ddtest_go_toolchains")
    ddtest_mini(name = "ddtest_mini")
