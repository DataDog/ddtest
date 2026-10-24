#!/usr/bin/python3
"""Model-free CLI fixture for manual QA and container integration tests."""
import json
import os
from pathlib import Path
import subprocess
import sys
import time

agent=Path(sys.argv[0]).name
if '--version' in sys.argv:
    print('codex-cli 0.163.0' if agent=='codex' else '2.1.220 (Claude Code)')
    sys.exit(0)
if '--fixture-mode' in sys.argv:
    mode=sys.argv[sys.argv.index('--fixture-mode')+1]
    if mode=='environment':
        (Path(os.environ['HOME'])/'setup-env.json').write_text(json.dumps(dict(os.environ)))
    else:
        modes={'transport':{'plain_error':'API Error: 503 service unavailable'},'permission':{'plain_error':'Permission denied (publickey)'},
               'detached':{'detach':True},'hang':{'detach':True,'wait':True}}
        Path('fake-options.json').write_text(json.dumps(modes[mode]))
        with Path('.git/info/exclude').open('a') as stream:stream.write('\nfake-options.json\n')
    sys.exit(0)
observed={'fake_agent':True,'argv':sys.argv[1:],'prompt':sys.stdin.read(),'environment':dict(os.environ)}
Path('observed.json').write_text(json.dumps(observed))
options=Path('fake-options.json')
options=json.loads(options.read_text()) if options.exists() else {}
if options.get('detach'):
    sentinel=Path(os.environ['HOME']).parent/'orphan-survived'
    child=f"import time,pathlib; time.sleep(4); pathlib.Path({str(sentinel)!r}).write_text('orphan'); time.sleep(300)"
    # The wrapper exits immediately. The descendant is reparented before any scan.
    subprocess.Popen([sys.executable,'-c',child],start_new_session=True)
    print('detached',flush=True)
    if options.get('wait'):
        print('Fake agent waiting; safe to terminate the controller.',flush=True)
        time.sleep(300)
    sys.exit(0)
if options.get('plain_error'):
    print(options['plain_error'],file=sys.stderr)
    sys.exit(1)
if agent=='codex':
    events=[{'type':'item.completed','item':{'type':'file_change','changes':[{'path':'src/example.js','kind':'update'}]}},
            {'type':'item.completed','item':{'type':'mcp_tool_call','server':'fixture','tool':'query','arguments':{'url':'https://example.com'}}},
            {'type':'item.completed','item':{'type':'reasoning','text':'PRIVATE_REASONING'}},
            {'type':'item.completed','item':{'type':'agent_message','text':'Fake agent completed; token=fake-secret-value.'}},
            {'type':'turn.completed'}]
else:
    events=[{'type':'assistant','message':{'content':[{'type':'tool_use','name':'Write','input':{'file_path':'/outside/fixture','content':'fixture'}},
             {'type':'thinking','thinking':'PRIVATE_REASONING'}]}},
            {'type':'result','result':'Fake agent completed; token=fake-secret-value.'}]
for event in events:print(json.dumps(event),flush=True)
