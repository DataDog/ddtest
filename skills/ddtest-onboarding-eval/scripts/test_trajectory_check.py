import json
from pathlib import Path
import tempfile
import unittest

import trajectory_check

PATCH = 'diff --git a/ci.yml b/ci.yml\n--- a/ci.yml\n+++ b/ci.yml\n@@ -1 +1 @@\n-old\n+new\n'


class TrajectoryTests(unittest.TestCase):
    def evaluate(self, patch=PATCH, prescription='', review=False):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / 'changes.patch').write_text(patch)
            records = json.dumps({'result': {'instruction': prescription}}) + '\n'
            (root / 'ddtest-results.jsonl').write_text(records)
            (root / 'status.txt').write_text(' M ci.yml\n' if patch else '')
            if review:
                item = next(trajectory_check.hunks(patch))
                (root / 'trajectory-review.json').write_text(json.dumps({
                    'patch_sha256': trajectory_check.digest(patch.encode()),
                    'prescriptions_sha256': trajectory_check.digest(records.encode()),
                    'hunks': [{'id': item['id'], 'row': 1, 'evidence': prescription, 'verdict': 'prescribed'}]}))
            return trajectory_check.evaluate(root)

    def test_only_exact_prescribed_hunks_pass_automatically(self):
        self.assertEqual(self.evaluate(prescription=PATCH)['status'], 'pass')
        self.assertEqual(self.evaluate(patch=PATCH.replace('+new', '+unrelated'), prescription=PATCH)['status'], 'review')
        self.assertEqual(self.evaluate(prescription='Change ci.yml')['status'], 'review')
        self.assertEqual(self.evaluate(patch='', prescription='No source edits needed')['status'], 'pass')

    def test_manual_review_is_bound_to_both_the_patch_and_the_prescription(self):
        self.assertEqual(self.evaluate(prescription='Replace old with new in ci.yml.', review=True)['status'], 'pass')
        self.assertEqual(self.evaluate(prescription='too short', review=True)['status'], 'review')

    def test_an_extra_quoted_path_cannot_be_ignored_beside_a_prescribed_file(self):
        quoted = 'diff --git "a/extra\\tfile" "b/extra\\tfile"\n--- "a/extra\\tfile"\n+++ "b/extra\\tfile"\n@@ -1 +1 @@\n-old\n+unprescribed\n'
        result = self.evaluate(patch=PATCH + quoted, prescription=PATCH)
        self.assertEqual(result['status'], 'review')
        self.assertEqual([item['path'] for item in result['hunks']], ['ci.yml', 'extra\tfile'])

    def test_one_prescription_cannot_authorize_the_same_edit_at_another_location(self):
        duplicate = PATCH + '@@ -10 +10 @@\n-old\n+new\n'
        self.assertEqual(self.evaluate(patch=duplicate, prescription=PATCH)['status'], 'review')

    def test_binary_untracked_files_stay_unverified(self):
        for content in (b'header\x00binary', b'\xff\xfe'):
            with tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                (root / 'changes.patch').write_text('')
                (root / 'status.txt').write_text('?? artifact.bin\n')
                (root / 'ddtest-results.jsonl').write_text('{"result": {"instruction": "Write the prescribed artifact.bin"}}\n')
                retained = root / 'retained/untracked/artifact.bin'
                retained.parent.mkdir(parents=True)
                retained.write_bytes(content)
                result = trajectory_check.evaluate(root)
                self.assertEqual(result['status'], 'review')
                self.assertTrue(result['non_text_changes'])

    def test_nontext_edits_are_not_silently_ignored(self):
        self.assertEqual(self.evaluate(patch='diff --git a/a b/a\nold mode 100644\nnew mode 100755\n')['status'], 'review')
        self.assertEqual(self.evaluate(patch='diff --git a/a b/a\nBinary files a/a and b/a differ\n')['status'], 'review')
        self.assertEqual(self.evaluate(patch=PATCH.replace('--- a/ci.yml', 'new file mode 100755\n--- a/ci.yml'), prescription=PATCH)['status'], 'review')


    def test_retained_untracked_content_is_checked_and_bound_to_its_review(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'changes.patch').write_text('')
            (root / 'status.txt').write_text('?? coverage.js\n')
            records = json.dumps({'result': {'instruction': 'Create coverage.js with the prescribed coverage config.'}}) + '\n'
            (root / 'ddtest-results.jsonl').write_text(records)
            retained = root / 'retained/untracked/coverage.js'
            retained.parent.mkdir(parents=True)
            retained.write_text('module.exports = {all: true};\n')
            review = trajectory_check.evaluate(root)
            self.assertEqual(len(review['hunks']), 1)
            self.assertEqual(review['hunks'][0]['path'], 'coverage.js')
            self.assertEqual(review['status'], 'review')
            (root / 'trajectory-review.json').write_text(json.dumps({
                'patch_sha256': review['patch_sha256'], 'prescriptions_sha256': review['prescriptions_sha256'],
                'hunks': [{'id': review['hunks'][0]['id'], 'row': 1, 'evidence': 'Create coverage.js with the prescribed coverage config.', 'verdict': 'prescribed'}]}))
            self.assertEqual(trajectory_check.evaluate(root)['status'], 'pass')
            retained.write_text('An unrelated change after review')
            self.assertEqual(trajectory_check.evaluate(root)['status'], 'review')


class IndependentReportingTests(unittest.TestCase):
    def test_review_is_hash_bound_and_cannot_override_contradictions(self):
        import catalog_check
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "final-response.md").write_text("A faithful paraphrase.")
            report = root / catalog_check.REPORT
            report.parent.mkdir(parents=True)
            report.write_text('{"summary":{"status":"COMPLETE LOCALLY"}}')
            review = {"verdict": "pass", "rationale": "Counts, verdict, limitations and human actions agree with the retained report.",
                      "final_response_sha256": trajectory_check.digest((root / "final-response.md").read_bytes()),
                      "report_sha256": trajectory_check.digest(report.read_bytes())}
            (root / "reporting-review.json").write_text(json.dumps(review))
            automatic = {"status": "review", "missing": ["unchanged summary.final_response"], "differences": [], "unsupported": []}
            self.assertTrue(catalog_check.independent_reporting_review(root, automatic))
            self.assertFalse(catalog_check.independent_reporting_review(root, {**automatic, "status": "fail"}))
            self.assertFalse(catalog_check.independent_reporting_review(root, {**automatic, "differences": ["invented count"]}))
            (root / "final-response.md").write_text("Changed after the review")
            self.assertFalse(catalog_check.independent_reporting_review(root, automatic))

    def test_a_harness_failure_is_not_an_agent_verdict_disagreement(self):
        import catalog_check
        records = [{"id": "E04", "agent": "codex", "outcome": "harness_failure", "actual": {"verdict": None}, "notes": []},
                   {"id": "E04", "agent": "claude", "outcome": "pass", "actual": {"verdict": "COMPLETE LOCALLY"}, "notes": []}]
        self.assertEqual(catalog_check.disagreements(records), [])
