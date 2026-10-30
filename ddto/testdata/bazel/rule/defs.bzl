"""ddtest_go_test: a go_test built with the instrumented SDK and Mini."""

load("@io_bazel_rules_go//go:def.bzl", "go_test")

_SDK_NAME = "@io_bazel_rules_go//go/toolchain:sdk_name"

def _instrumented_impl(_settings, _attr):
    return {_SDK_NAME: "ddto"}

_instrumented = transition(
    implementation = _instrumented_impl,
    inputs = [],
    outputs = [_SDK_NAME],
)

def _ddtest_test_impl(ctx):
    test = ctx.attr.test[0]
    executable = test[DefaultInfo].files_to_run.executable
    out = ctx.actions.declare_file(ctx.label.name)
    ctx.actions.symlink(output = out, target_file = executable, is_executable = True)
    runfiles = ctx.runfiles(files = [executable] + ctx.files.data + ctx.files.manifest)
    runfiles = runfiles.merge(test[DefaultInfo].default_runfiles)
    for target in ctx.attr.data + [ctx.attr.manifest]:
        runfiles = runfiles.merge(target[DefaultInfo].default_runfiles)
    env = {}
    if RunEnvironmentInfo in test:
        env.update(test[RunEnvironmentInfo].environment)
    env.update({
        "DD_CIVISIBILITY_ENABLED": "true",
        "DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES": "true",
        "DD_TEST_OPTIMIZATION_MANIFEST_FILE": ctx.expand_location("$(rlocationpath %s)" % ctx.attr.manifest_file.label, [ctx.attr.manifest_file]),
    })
    return [
        DefaultInfo(executable = out, runfiles = runfiles),
        RunEnvironmentInfo(environment = env),
    ]

_ddtest_test = rule(
    implementation = _ddtest_test_impl,
    attrs = {
        "test": attr.label(cfg = _instrumented, executable = True, mandatory = True),
        # The test's own data keeps the caller's configuration.
        "data": attr.label_list(allow_files = True),
        "manifest": attr.label(mandatory = True),
        "manifest_file": attr.label(mandatory = True, allow_single_file = True),
        "_allowlist_function_transition": attr.label(default = "@bazel_tools//tools/allowlists/function_transition_allowlist"),
    },
    test = True,
)

# The registration file that ddtest instrument writes, in the external test
# package when a source declares one.
_REGISTRATION = r"""packages=$$(sed -n 's/^package \([A-Za-z0-9_]*\).*/\1/p' $(SRCS) | sort -u)
external=$$(printf '%s\n' "$$packages" | grep '_test$$' | head -1 || true)
name=$${external:-$$(printf '%s\n' "$$packages" | head -1)}
printf 'package %s\nimport __dd_ci_runtime "github.com/DataDog/ddtest/ddto/testopt"\nfunc init() { __dd_ci_runtime.RegisterTestPackage() }\n' "$$name" > $@
"""

def ddtest_go_test(name, srcs, deps = [], data = [], instrumented_data = [], tags = [], **kwargs):
    """A go_test with Mini, built with the instrumented SDK.

    Args:
      name: the test's name.
      srcs: its Go sources.
      deps: its dependencies; Mini's testopt is added unless they name it.
      data: data in the caller's configuration.
      instrumented_data: data built with the instrumented SDK.
      tags: tags.
      **kwargs: other go_test attributes.
    """

    # After go_deps' copy is overridden with Mini, a test that uses Mini's Go
    # API already depends on it.
    mini = Label("@ddtest_mini//testopt")
    if mini not in [native.package_relative_label(dep) for dep in deps]:
        deps = deps + [mini]
    registration = name + "_ddtest_registration"
    native.genrule(
        name = registration,
        srcs = [src for src in srcs if src.endswith(".go")],
        outs = [name + "/zz_dd_ci_visibility_test.go"],
        cmd = _REGISTRATION,
        tags = ["manual"],
    )
    go_test(
        name = name + "_go",
        srcs = srcs + [":" + registration],
        deps = deps,
        data = instrumented_data,
        tags = tags + ["manual"],
        **kwargs
    )
    _ddtest_test(
        name = name,
        test = ":" + name + "_go",
        data = data,
        manifest = "//testdata:manifest",
        manifest_file = "//testdata:manifest.txt",
        tags = tags,
    )
