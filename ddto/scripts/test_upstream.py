"""Contracts for the offline provenance audit and upstream comparisons."""

import hashlib
from pathlib import Path
import tempfile
import unittest

import upstream


class SourceAuditTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        (self.root / "LICENSE").write_text("retained license\n")
        (self.root / "source.go").write_text("package example\n")
        self.manifest = {
            "commit": "a" * 40,
            "licenses": ["LICENSE"],
            "files": [
                {"path": name, "sha256": upstream.digest(self.root / name)}
                for name in ("LICENSE", "source.go")
            ],
        }

    def test_detects_modified_missing_and_untracked_code(self):
        self.assertEqual(upstream.verify_library(self.root, self.manifest), [])
        (self.root / "source.go").write_text("package changed\n")
        (self.root / "new.go").write_text("package new\n")
        (self.root / "LICENSE").unlink()
        errors = upstream.verify_library(self.root, self.manifest)
        self.assertIn("local source changed: source.go", errors)
        self.assertIn("source absent from manifest: new.go", errors)
        self.assertIn("license missing from source/manifest: LICENSE", errors)

    def test_rejects_source_paths_outside_the_recorded_root(self):
        for path in ("../source.go", "/source.go", "..\\source.go"):
            with self.subTest(path=path), self.assertRaises(ValueError):
                upstream.contained(self.root, path)

    def test_patch_requires_exact_base_and_keeps_original_paths(self):
        source = self.root / "base"
        (source / "internal").mkdir(parents=True)
        raw = b"package example\n// upstream\n"
        original = source / "internal/source.go"
        original.write_bytes(raw)
        entry = self.manifest["files"][1]
        entry.update(source_path="internal/source.go", source_sha256=hashlib.sha256(raw).hexdigest())
        patch = upstream.local_patch(self.root, self.manifest, source)
        self.assertIn("--- upstream/internal/source.go", patch)
        self.assertIn("+++ local/source.go", patch)
        self.assertIn("-// upstream", patch)
        original.write_text("package newer\n")
        with self.assertRaises(ValueError):
            upstream.local_patch(self.root, self.manifest, source)
        original.unlink()
        self.assertEqual(upstream.compare_source(self.manifest, source), [("missing", "internal/source.go")])

    def test_feature_port_keeps_base_and_selects_exact_revision(self):
        entry = self.manifest["files"][1]
        entry.update(source_path="internal/source.go", source_sha256="b" * 64)
        self.manifest["feature_ports"] = [{
            "name": "feature", "commit": "c" * 40,
            "files": [{"path": "source.go", "source_path": "internal/source.go", "source_sha256": "d" * 64}],
        }]
        self.assertEqual(upstream.verify_library(self.root, self.manifest), [])
        self.assertEqual(upstream.source_entries(self.manifest)[0]["sha256"], "b" * 64)
        self.assertEqual(upstream.source_entries(self.manifest, "c" * 40)[0]["sha256"], "d" * 64)
        self.manifest["feature_ports"][0]["commit"] = "main"
        self.assertIn("feature port needs an exact commit", upstream.verify_library(self.root, self.manifest))

    def test_feature_patch_uses_original_paths_and_requires_recorded_hash(self):
        source = self.root / "feature"
        (source / "internal").mkdir(parents=True)
        raw = b"package upstream_feature\n"
        (source / "internal/source.go").write_bytes(raw)
        self.manifest["feature_ports"] = [{"name": "feature", "commit": "c" * 40, "files": [{
            "path": "source.go", "source_path": "internal/source.go", "source_sha256": hashlib.sha256(raw).hexdigest(),
        }]}]
        patch = upstream.local_patch(self.root, self.manifest, source, "c" * 40)
        self.assertIn("--- upstream/internal/source.go", patch)
        self.assertIn("+++ local/source.go", patch)
        (source / "internal/source.go").write_text("package drift\n")
        with self.assertRaises(ValueError):
            upstream.local_patch(self.root, self.manifest, source, "c" * 40)

    def test_feature_port_rejects_untracked_local_source_and_missing_original_hash(self):
        self.manifest["feature_ports"] = [{"commit": "c" * 40, "files": [{"path": "missing.go", "source_path": "internal/missing.go"}]}]
        errors = upstream.verify_library(self.root, self.manifest)
        self.assertIn("feature port file absent from manifest: missing.go", errors)
        self.assertIn("feature port needs a source hash: missing.go", errors)


if __name__ == "__main__":
    unittest.main()
