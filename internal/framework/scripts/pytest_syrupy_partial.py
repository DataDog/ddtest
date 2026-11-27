"""pytest plugin ddtest loads when a worker runs only part of the suite.

syrupy fails the session on unused snapshots, which only a run of the whole
suite can judge. In a run of part of it, this plugin makes syrupy warn about
them instead, as --snapshot-warn-unused does. It sets syrupy's own option
inside pytest, so it applies only when syrupy is active in the effective
configuration: with syrupy absent or turned off, by -p no:syrupy on the command
line, in PYTEST_ADDOPTS or in a configuration file, it does nothing.
"""


def pytest_configure(config):
    option = config.option
    if hasattr(option, "warn_unused_snapshots") and not getattr(option, "update_snapshots", False):
        option.warn_unused_snapshots = True
