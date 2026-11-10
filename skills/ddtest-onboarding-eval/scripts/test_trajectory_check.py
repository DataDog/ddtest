import json
import subprocess
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

    def test_tracked_shell_script_carriage_returns_require_their_own_prescription(self):
        prescription = ('diff --git a/setup.sh b/setup.sh\n'
                        '--- a/setup.sh\n+++ b/setup.sh\n@@ -1,2 +1,2 @@\n'
                        ' #!/bin/sh\n-echo old\n+echo new\n')
        changed = prescription.replace('+echo new\n', '+echo new\r\n')
        result = self.evaluate(patch=changed, prescription=prescription)
        self.assertEqual(result['status'], 'review')
        self.assertEqual(result['hunks'][0]['status'], 'review')
        self.assertIn('+echo new\r', result['hunks'][0]['patch'])
        self.assertEqual(self.evaluate(patch=changed, prescription=changed)['status'], 'pass')

    def test_hunk_content_separators_are_not_treated_as_patch_newlines(self):
        original = next(trajectory_check.hunks(PATCH))
        for separator in ('\r', '\v', '\f', '\x85', '\u2028', '\u2029'):
            with self.subTest(separator=repr(separator)):
                patch = PATCH.replace('+new', '+new' + separator)
                changed = next(trajectory_check.hunks(patch))
                self.assertNotEqual(original['id'], changed['id'])
                self.assertIn('+new' + separator, changed['patch'])
                self.assertEqual(self.evaluate(patch=patch, prescription=PATCH)['status'], 'review')
                self.assertEqual(self.evaluate(patch=patch, prescription=patch)['status'], 'pass')

    def test_metadata_phrases_inside_text_hunks_are_ordinary_prescribed_text(self):
        for text in ('GIT binary patch', 'Binary files a and b differ', 'old mode 100644', 'rename from cache', 'copy from cache', 'copy to cache'):
            for prefix in ('+', '-', ' '):
                with self.subTest(text=text, prefix=prefix):
                    patch = PATCH.replace('+new', '+' + text) if prefix == '+' else PATCH.replace('-old', '-' + text) if prefix == '-' else PATCH.replace('@@ -1 +1 @@\n', '@@ -1,2 +1,2 @@\n ' + text + '\n')
                    self.assertEqual(self.evaluate(patch=patch, prescription=patch)['status'], 'pass')
        renamed = PATCH.replace('--- a/ci.yml', 'rename from old.yml\nrename to ci.yml\n--- a/ci.yml')
        self.assertEqual(self.evaluate(patch=renamed, prescription=renamed)['status'], 'review')

    def test_git_copy_with_a_prescribed_hunk_requires_review_of_inherited_content(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)

            def git(*args, data=None):
                return subprocess.check_output(['git', *args], cwd=root, input=data, text=True).strip()

            git('init', '-q')
            original = ''.join(f'original line {number}\n' for number in range(40))
            blob = git('hash-object', '-w', '--stdin', data=original)
            before = git('mktree', data=f'100644 blob {blob}\tsource.txt\n')
            source = git('hash-object', '-w', '--stdin', data=original.replace('line 0\n', 'source changed\n'))
            copied = git('hash-object', '-w', '--stdin', data=original.replace('line 39\n', 'copy changed\n'))
            after = git('mktree', data=f'100644 blob {copied}\tcopied.txt\n100644 blob {source}\tsource.txt\n')
            patch = git('-c', 'diff.renames=copies', 'diff', '--no-ext-diff', '--no-textconv', before, after) + '\n'
            self.assertIn('\ncopy from source.txt\ncopy to copied.txt\n', patch)
            self.assertNotIn('original line 20', patch)
            result = self.evaluate(patch=patch, prescription=patch)
            self.assertTrue(all(hunk['status'] == 'pass' for hunk in result['hunks']))
            self.assertEqual(result['status'], 'review')
            self.assertTrue(result['non_text_changes'])

    def test_non_utf8_tracked_patch_cannot_match_replacement_character(self):
        hashes = []
        for byte in (b'\xff', b'\xfe'):
            with self.subTest(byte=byte), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                patch = PATCH.encode().replace(b'+new', b'+new' + byte)
                (root / 'changes.patch').write_bytes(patch)
                (root / 'status.txt').write_text(' M ci.yml\n')
                (root / 'ddtest-results.jsonl').write_text(json.dumps({'result': {'instruction': patch.decode(errors='replace')}}) + '\n')
                result = trajectory_check.evaluate(root)
                self.assertEqual(result['status'], 'review')
                hashes.append(result['patch_sha256'])
        self.assertNotEqual(*hashes)
        valid = PATCH.replace('+new', '+new\ufffd')
        self.assertEqual(self.evaluate(patch=valid, prescription=valid)['status'], 'pass')

    def test_manual_review_is_bound_to_both_the_patch_and_the_prescription(self):
        self.assertEqual(self.evaluate(prescription='Replace old with new in ci.yml.', review=True)['status'], 'pass')
        self.assertEqual(self.evaluate(prescription='too short', review=True)['status'], 'review')

    def test_malformed_attestations_remain_unverified(self):
        for entries in (None, 42, "bad", {}, [None, {"id": []}], [{"id": {}}]):
            with self.subTest(entries=entries), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                records = json.dumps({'result': {'instruction': 'Replace old with new in ci.yml.'}}) + '\n'
                (root / 'changes.patch').write_text(PATCH)
                (root / 'ddtest-results.jsonl').write_text(records)
                (root / 'status.txt').write_text(' M ci.yml\n')
                (root / 'trajectory-review.json').write_text(json.dumps({
                    'patch_sha256': trajectory_check.digest(PATCH.encode()),
                    'prescriptions_sha256': trajectory_check.digest(records.encode()), 'hunks': entries}))
                result = trajectory_check.evaluate(root)
                self.assertEqual(result['status'], 'review')
                self.assertEqual(result['hunks'][0]['status'], 'review')

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



    def test_added_and_deleted_regular_text_files_can_be_certified(self):
        for patch in (
            'diff --git a/ci.yml b/ci.yml\nnew file mode 100644\nindex 0000000..a123456\n--- /dev/null\n+++ b/ci.yml\n@@ -0,0 +1 @@\n+new\n',
            'diff --git a/ci.yml b/ci.yml\ndeleted file mode 100644\nindex a123456..0000000\n--- a/ci.yml\n+++ /dev/null\n@@ -1 +0,0 @@\n-old\n',
        ):
            for mode in ('100644', '100755'):
                current = patch.replace('100644', mode)
                with self.subTest(patch=current):
                    self.assertEqual(self.evaluate(patch=current, prescription=current)['status'], 'pass')
                    self.assertEqual(self.evaluate(patch=current, prescription='Apply the prescribed ci.yml change.', review=True)['status'], 'pass')
                    self.assertEqual(self.evaluate(patch=current, prescription='No source edits needed')['status'], 'review')
            self.assertEqual(self.evaluate(patch=patch.replace('100644', '100755'), prescription=patch)['status'], 'review')


    def test_symlinks_and_permission_changes_cannot_be_certified_as_text(self):
        for metadata in ('new file mode 120000', 'old mode 100644\nnew mode 100755'):
            patch = PATCH.replace('--- a/ci.yml', metadata + '\n--- a/ci.yml')
            with self.subTest(metadata=metadata):
                self.assertEqual(self.evaluate(patch=patch, prescription=patch, review=True)['status'], 'review')


    def test_unrepresented_file_changes_remain_unverified_beside_prescribed_hunks(self):
        for metadata in ('new file mode 100644', 'deleted file mode 100644',
                         'new file mode 120000', 'old mode 100644\nnew mode 100755'):
            patch = PATCH + 'diff --git a/extra b/extra\n' + metadata + '\n'
            with self.subTest(metadata=metadata):
                self.assertEqual(self.evaluate(patch=patch, prescription=PATCH)['status'], 'review')


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
            (root / 'retained/RETENTION.json').write_text(json.dumps({'status': 'ok', 'untracked_executable_modes': {'coverage.js': '000'}}))
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


    def test_untracked_review_is_bound_to_source_executable_permissions(self):
        import run_catalog
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            checkout, output = root / 'repo', root / 'out'
            checkout.mkdir()
            output.mkdir()
            source = checkout / 'setup.sh'
            source.write_text('#!/bin/sh\nexit 0\n')
            source.chmod(0o644)
            (output / 'run.json').write_text(json.dumps({'checkout': str(checkout)}))
            (output / 'changes.patch').write_text('')
            (output / 'status.txt').write_text('?? setup.sh\n')
            instruction = 'Create setup.sh with the prescribed setup command.'
            (output / 'ddtest-results.jsonl').write_text(json.dumps({'result': {'instruction': instruction}}) + '\n')
            run_catalog.retain(output)
            original = trajectory_check.evaluate(output)
            (output / 'trajectory-review.json').write_text(json.dumps({
                'patch_sha256': original['patch_sha256'], 'prescriptions_sha256': original['prescriptions_sha256'],
                'hunks': [{'id': original['hunks'][0]['id'], 'row': 1, 'evidence': instruction, 'verdict': 'prescribed'}]}))
            self.assertEqual(trajectory_check.evaluate(output)['status'], 'pass')
            source.chmod(0o755)
            run_catalog.retain(output)
            changed = trajectory_check.evaluate(output)
            self.assertEqual(changed['status'], 'review')
            self.assertNotEqual(original['hunks'][0]['id'], changed['hunks'][0]['id'])
            self.assertEqual(original['patch_sha256'], changed['patch_sha256'])
            review = json.loads((output / 'trajectory-review.json').read_text())
            review['hunks'][0]['id'] = changed['hunks'][0]['id']
            (output / 'trajectory-review.json').write_text(json.dumps(review))
            self.assertEqual(trajectory_check.evaluate(output)['status'], 'pass')
            (output / 'retained/untracked/setup.sh').chmod(0o644)
            tampered = trajectory_check.evaluate(output)
            review['hunks'][0]['id'] = tampered['hunks'][0]['id']
            (output / 'trajectory-review.json').write_text(json.dumps(review))
            self.assertEqual(trajectory_check.evaluate(output)['status'], 'review')

    def test_untracked_approval_without_retained_mode_provenance_stays_unverified(self):
        import run_catalog
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / 'out'
            output.mkdir()
            (output / 'changes.patch').write_text('')
            (output / 'status.txt').write_text('?? setup.sh\n')
            (output / 'run.json').write_text(json.dumps({'checkout': str(Path(directory) / 'removed-checkout')}))
            instruction = 'Create setup.sh with the prescribed setup command.'
            (output / 'ddtest-results.jsonl').write_text(json.dumps({'result': {'instruction': instruction}}) + '\n')
            retained = output / 'retained/untracked/setup.sh'
            retained.parent.mkdir(parents=True)
            retained.write_text('#!/bin/sh\nexit 0\n')
            original = trajectory_check.evaluate(output)
            (output / 'trajectory-review.json').write_text(json.dumps({
                'patch_sha256': original['patch_sha256'], 'prescriptions_sha256': original['prescriptions_sha256'],
                'hunks': [{'id': original['hunks'][0]['id'], 'row': 1, 'evidence': instruction, 'verdict': 'prescribed'}]}))
            self.assertEqual(trajectory_check.evaluate(output)['status'], 'review')
            # A missing checkout cannot restore executable-mode provenance to old retained bytes.
            run_catalog.retain(output)
            self.assertEqual(trajectory_check.evaluate(output)['status'], 'review')


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
