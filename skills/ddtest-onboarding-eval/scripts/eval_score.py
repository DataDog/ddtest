"""Repeated-trial pass rates against expectations fixed before execution."""
import hashlib
import json


def expand(cases, trials):
    if trials < 1:
        raise ValueError('--trials must be at least 1')
    return [{**case, 'catalog_id': case['id'], 'trial': trial,
             'id': case['id'] if trials == 1 else f"{case['id']}-trial-{trial:02d}"}
            for case in cases for trial in range(1, trials + 1)]


def oracle(cases, agents, speed_trims, model=None, intake_guard=None, trials=1, case_image=False):
    if trials < 1:
        raise ValueError("--trials must be at least 1")
    def expected(value):
        return {key: value.get(key) for key in ('verdict', 'blockers')}
    values = []
    for case in cases:
        base = case['expected']
        values.append({'id': case['id'], 'repo': case['repo'], 'commit': case['commit'], 'framework': case['framework'],
                       'image': case['image'], 'setup': case['setup_commands'], 'readiness': case['readiness_commands'],
                       'trims': case['trims'], 'harness': case['harness'],
                       'expected': {agent: expected({**base, **(base.get('agents', {}).get(agent) or {})}) for agent in agents}})
    document = {'schema_version': 3, 'speed_trims': speed_trims, 'trials_per_case_agent': trials,
                'case_image': case_image, 'cases': values}
    if model:
        document['requested_model'] = model
    if intake_guard:
        document['intake_guard'] = intake_guard
    encoded = json.dumps(document, sort_keys=True, separators=(',', ':')).encode()
    return {**document, 'sha256': hashlib.sha256(encoded).hexdigest()}


def freeze(path, document, evaluate_only=False):
    if path.exists():
        saved = json.loads(path.read_text())
        if 'trials_per_case_agent' not in saved:
            raise ValueError('The frozen oracle predates planned trial counts; use its original evaluator for historical evidence or a new output folder for a new experiment')
        if 'case_image' not in saved:
            raise ValueError('The frozen oracle predates preparation mode provenance; use its original evaluator for historical evidence or a new output folder for a new experiment')
        if saved != document:
            raise ValueError('The frozen oracle differs from the requested cases, expectations or trial count; use a new output folder')
    elif evaluate_only:
        raise ValueError('Repeated-trial evaluation requires the oracle saved before the trials')
    else:
        path.parent.mkdir(parents=True, exist_ok=True)
        with path.open('x') as stream:
            stream.write(json.dumps(document, indent=2) + '\n')


def score(records):
    groups = {}
    for record in records:
        key = (record.get('catalog_id', record['id']), record['agent'])
        group = groups.setdefault(key, {'case': key[0], 'agent': key[1], 'trials': 0, 'expected_passes': 0,
                                        'complete_locally': 0, 'trajectory_passes': 0, 'rigorous_passes': 0})
        group['trials'] += 1
        matched = record['outcome'] == 'pass'
        trajectory = record.get('trajectory', {}).get('status') == 'pass'
        group['expected_passes'] += matched
        group['complete_locally'] += record.get('actual', {}).get('verdict') == 'COMPLETE LOCALLY'
        group['trajectory_passes'] += trajectory
        group['rigorous_passes'] += matched and trajectory
    values = list(groups.values())
    for group in values:
        for metric in ('expected_passes', 'complete_locally', 'trajectory_passes', 'rigorous_passes'):
            group[metric + '_rate'] = group[metric] / group['trials']
    count = sum(group['trials'] for group in values)
    return {'trials': count, 'expected_pass_rate': sum(group['expected_passes'] for group in values) / count if count else None,
            'rigorous_pass_rate': sum(group['rigorous_passes'] for group in values) / count if count else None, 'cases': values}


def compare(current, previous):
    if current.get('oracle_sha256') != previous.get('oracle_sha256') or not current.get('oracle_sha256'):
        raise ValueError('Scores require the same frozen oracle, cases, agents and setup to compare')
    old, new = previous['score'], current['score']
    if {(x['case'], x['agent']) for x in old['cases']} != {(x['case'], x['agent']) for x in new['cases']}:
        raise ValueError('Scores cover different case and agent sets')
    if not old['trials'] or not new['trials']:
        raise ValueError('Scores with no trials cannot be compared')
    return {'previous_ddtest': previous.get('ddtest_label'), 'current_ddtest': current.get('ddtest_label'),
            'expected_pass_rate_delta': new['expected_pass_rate'] - old['expected_pass_rate'],
            'rigorous_pass_rate_delta': new['rigorous_pass_rate'] - old['rigorous_pass_rate']}
