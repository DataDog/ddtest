"""The Bzlmod extension of the test-only rule."""

load(":repositories.bzl", "ddtest_repositories")

def _ddtest_impl(_module_ctx):
    ddtest_repositories()

ddtest = module_extension(implementation = _ddtest_impl)
