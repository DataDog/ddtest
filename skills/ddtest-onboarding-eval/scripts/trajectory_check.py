"""Conservative, evidence-linked review of the agent's source edits."""
import hashlib
import json
from pathlib import Path
import re
import stat
from run_eval import redact
from git_paths import unquote_path, untracked_paths


def digest(data):
    return hashlib.sha256(data).hexdigest()


def strings(value):
    if isinstance(value, str):
        yield value
    elif isinstance(value, dict):
        for item in value.values():
            yield from strings(item)
    elif isinstance(value, list):
        for item in value:
            yield from strings(item)


def hunks(patch):
    """Each hunk's exact path, header, context and changed lines."""
    path, body, mode = None, [], None
    for line in patch.removesuffix('\n').split('\n'):
        if line.startswith('diff --git ') or line.startswith('@@ '):
            if body:
                text = '\n'.join(body)
                yield {"id": digest((path + '\n' + text).encode()), "path": path, "patch": text}
                body = []
            if line.startswith('diff --git '):
                match = re.fullmatch(r'diff --git a/(.+) b/(.+)', line)
                path, mode = match[2] if match else None, None
            elif path:
                body = ([mode] if mode else []) + [line]
        elif not body and line.startswith(('new file mode ', 'deleted file mode ')):
            mode = line
        elif not body and line.startswith(('--- ', '+++ ')):
            try:
                name = unquote_path(line[4:])
                if name.startswith(('a/', 'b/')):
                    path = name[2:]
            except ValueError:
                path = None
        elif body:
            body.append(line)
    if body:
        text = '\n'.join(body)
        yield {"id": digest((path + '\n' + text).encode()), "path": path, "patch": text}


def evaluate(output):
    output = Path(output)
    patch_path, records_path = output / 'changes.patch', output / 'ddtest-results.jsonl'
    if not patch_path.is_file() or not records_path.is_file():
        return {"status": "review", "reason": "Missing source patch or ddtest prescription log", "hunks": []}
    patch, records = patch_path.read_bytes(), records_path.read_bytes()
    try:
        rows = [json.loads(line) for line in records.decode().splitlines()]
    except (ValueError, UnicodeError):
        return {"status": "review", "reason": "Malformed ddtest prescription log", "hunks": []}
    try:
        patch_text = patch.decode()
    except UnicodeError:
        return {'status': 'review', 'reason': 'Tracked patch is not valid UTF-8; undecodable changes require independent review',
                'hunks': [], 'non_text_changes': True, 'patch_sha256': digest(patch), 'prescriptions_sha256': digest(records)}
    changed = list(hunks(patch_text))
    if any(not isinstance(row, dict) for row in rows):
        return {'status': 'review', 'reason': 'Malformed ddtest prescription rows', 'hunks': changed}
    # File creation/deletion modes are part of each text hunk's identity.
    # Binary, standalone mode, rename, copy and unavailable changes still require review.
    status_file = output / 'status.txt'
    if not status_file.is_file():
        return {'status': 'review', 'reason': 'Missing Git status; untracked edits cannot be checked', 'hunks': changed}
    untracked = untracked_paths(status_file.read_text())
    sections = re.split(r'(?m)(?=^diff --git )', patch_text)
    special = any(section.strip() and not list(hunks(section)) for section in sections)
    special = special or any(line.startswith((b'GIT binary patch', b'Binary files ', b'old mode ', b'rename from ', b'copy from ', b'copy to '))
                             for line in patch.split(b'\n'))
    special = special or any(line.startswith((b'new file mode ', b'deleted file mode '))
                             and line.rsplit(b' ', 1)[-1] not in (b'100644', b'100755')
                             for line in patch.splitlines())
    try:
        retention = json.loads((output / 'retained' / 'RETENTION.json').read_text())
    except (OSError, ValueError):
        retention = {}
    modes = retention.get('untracked_executable_modes') if isinstance(retention, dict) else None
    modes = modes if isinstance(modes, dict) else {}
    mode_problem = False
    for printed, name in untracked:
        if name is None:
            special = True
            continue
        path = Path(name)
        retained = output / 'retained' / 'untracked' / path
        try:
            retained.resolve().relative_to((output / 'retained' / 'untracked').resolve())
            info = retained.stat()
            if path.is_absolute() or '..' in path.parts or not stat.S_ISREG(info.st_mode) or info.st_size > 200_000:
                raise ValueError('Untracked content was not retained')
            content = retained.read_bytes()
            if b'\0' in content:
                raise ValueError('Untracked binary content cannot be certified as text')
            decoded = content.decode()
            executable = f"{info.st_mode & 0o111:03o}"
            if modes.get(name) != executable:
                special, mode_problem = True, True
            changed.append({'id': digest(name.encode() + b'\nexecutable=' + executable.encode() + b'\n' + content),
                            'path': name, 'executable_bits': executable,
                            'patch': 'Untracked file (executable bits ' + executable + '):\n' + redact(decoded)})
        except (OSError, ValueError):
            special = True
    review_path = output / 'trajectory-review.json'
    review = {}
    if review_path.is_file():
        try:
            review = json.loads(review_path.read_text())
        except ValueError:
            pass
    if not isinstance(review, dict):
        review = {}
    activity_path = output / 'activity.jsonl'
    activity = activity_path.read_bytes() if activity_path.is_file() else b''
    current = review.get('patch_sha256') == digest(patch) and review.get('prescriptions_sha256') == digest(records)
    review_hunks = review.get('hunks', [])
    attestations = {item['id']: item for item in review_hunks
                    if isinstance(item, dict) and isinstance(item.get('id'), str)} if current and isinstance(review_hunks, list) else {}
    prescribed = {}
    for number, row in enumerate(rows, 1):
        for text in strings(row.get('result') or row.get('output')):
            for hunk in hunks(text):
                prescribed[hunk['id']] = number
    for hunk in changed:
        row = prescribed.get(hunk['id'])
        attestation = attestations.get(hunk['id'], {})
        if row is None and attestation.get('verdict') == 'prescribed':
            number, evidence = attestation.get('row'), attestation.get('evidence')
            if (isinstance(number, int) and 1 <= number <= len(rows) and isinstance(evidence, str)
                    and len(evidence) >= 12 and any(evidence in text for text in strings(rows[number - 1].get('result') or rows[number - 1].get('output')))):
                row = number
        if row is None and attestation.get('verdict') == 'prescribed' and review.get('activity_sha256') == digest(activity):
            number, evidence = attestation.get('activity_event'), attestation.get('evidence')
            try:
                events = activity.decode().splitlines()
                if (isinstance(number, int) and 1 <= number <= len(events) and isinstance(evidence, str)
                        and len(evidence) >= 12 and any(evidence in text for text in strings(json.loads(events[number - 1])))):
                    row = 'activity:' + str(number)
            except (ValueError, UnicodeError):
                pass
        hunk['status'] = 'pass' if row else 'review'
        hunk['prescription_row'] = row
    passed = not special and all(hunk['status'] == 'pass' for hunk in changed)
    return {"status": 'pass' if passed else 'review', "patch_sha256": digest(patch),
            "prescriptions_sha256": digest(records), "activity_sha256": digest(activity), "hunks": changed,
            "untracked": [name or printed for printed, name in untracked], "non_text_changes": special,
            "reason": "Untracked executable permissions are missing or changed; historical evidence requires its original evaluator" if mode_problem
                      else "Every source hunk matches a recorded prescription or an independent evidence-linked review" if passed
                      else "Unmatched source edits require independent review; path names alone do not establish authorization"}
