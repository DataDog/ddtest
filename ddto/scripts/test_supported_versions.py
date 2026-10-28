"""The supported window follows its rules and the recorded list matches it."""

import json
import unittest

import supported_versions


SDK = {"module": "github.com/DataDog/dd-trace-go/v2", "minimum": "v2.10.0", "releases": [], "main": ""}
ORCHESTRION = {"module": "github.com/DataDog/orchestrion", "minors": 2, "releases": [], "main": ""}


def proxy(versions):
    def query(*args):
        module = args[-1].split("@")[0]
        if args[0] == "-versions":
            return " ".join([module, *versions[module]]) + "\n"
        return json.dumps({"Path": module, "Version": module.rsplit("/", 1)[-1] + "-main"})
    return query


class WindowTests(unittest.TestCase):
    def test_minimum_keeps_every_later_release(self):
        versions = ["v2.9.2", "v2.10.0", "v2.10.1", "v2.11.0-rc.2", "v2.11.0", "v2.12.0-rc.6", "v2.12.0-dev", "v2.10.10"]
        self.assertEqual(["v2.10.0", "v2.10.1", "v2.10.10", "v2.11.0"],
                         supported_versions.window(SDK, versions, "main")["releases"])

    def test_minors_keep_the_patch_releases_of_the_last_ones(self):
        versions = ["v1.12.2", "v1.13.0", "v1.13.1", "v1.14.0", "v1.14.1-0.20261027170653-324ce59f991a"]
        self.assertEqual(["v1.13.0", "v1.13.1", "v1.14.0"],
                         supported_versions.window(ORCHESTRION, versions, "main")["releases"])

    def test_resolve_queries_releases_and_main(self):
        query = proxy({SDK["module"]: ["v2.10.0", "v2.11.0"], ORCHESTRION["module"]: ["v1.13.0", "v1.14.0"]})
        resolved = supported_versions.resolve({"dd-trace-go": SDK, "orchestrion": ORCHESTRION}, query)
        self.assertEqual(["v2.10.0", "v2.11.0"], resolved["dd-trace-go"]["releases"])
        self.assertEqual("v2-main", resolved["dd-trace-go"]["main"])
        self.assertEqual("orchestrion-main", resolved["orchestrion"]["main"])

    def test_differences_name_new_and_old_releases(self):
        recorded = {"dd-trace-go": {**SDK, "releases": ["v2.10.0", "v2.10.1"], "main": "old"}}
        resolved = {"dd-trace-go": {**SDK, "releases": ["v2.10.1", "v2.11.0"], "main": "new"}}
        self.assertEqual(["dd-trace-go: new releases v2.11.0", "dd-trace-go: releases outside the window v2.10.0"],
                         supported_versions.differences(recorded, resolved))
        self.assertEqual([], supported_versions.differences(resolved, resolved))

    def test_recorded_list_follows_its_rules(self):
        recorded = json.loads(supported_versions.RECORDED.read_text())
        for entry in recorded.values():
            self.assertEqual(entry["releases"], supported_versions.window(entry, entry["releases"], entry["main"])["releases"])
            self.assertTrue(entry["main"].startswith("v") and not supported_versions.release_key(entry["main"]))


if __name__ == "__main__":
    unittest.main()
