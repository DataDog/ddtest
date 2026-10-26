import base64
"""No model calls: real Docker boundary, fake CLIs, and a local signed Git tree."""

import contextlib
import io
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

import isolation
import run_eval


IMAGE=os.environ.get('DDTEST_EVAL_TEST_IMAGE')
SOURCE=str(Path(__file__).resolve().parents[3])


class FixtureContainer(isolation.Container):
    """Seed fixture data only inside the storage production mounts."""
    def start(self):
        subprocess.run(['git','clone','--quiet','--no-local','--depth=1','--',SOURCE,str(self.trial/'source')],check=True,capture_output=True)
        super().start()
        (self.trial.parent/'active.json').write_text(json.dumps({'trial':str(self.trial),'container':self.name}))
    def execute(self,argv,*args):
        if argv[:3]==['git','clone','--']:
            argv=[*argv[:3],str(self.trial/'source'),argv[-1]]
        return super().execute(argv,*args)


class EnvironmentForwardingTests(unittest.TestCase):
    def test_multiline_model_headers_do_not_enter_argv_or_env_file(self):
        with tempfile.TemporaryDirectory() as temporary:
            trial = Path(temporary)
            container = isolation.Container(trial, "sha256:" + "0" * 64)
            headers = "source: fixture\norganization: private-canary"
            seen = []
            def executor(argv, cwd, env, *args):
                seen.append(argv)
                self.assertNotIn(headers, " ".join(argv))
                self.assertIn("ANTHROPIC_CUSTOM_HEADERS", " ".join(argv))
                path = Path(argv[argv.index("--env-file") + 1])
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
                entries = dict(line.split("=", 1) for line in path.read_text().splitlines())
                self.assertEqual(entries["CANARY"], "ordinary-value")
                self.assertNotIn(headers, path.read_text())
                self.assertEqual(base64.b64decode(entries["DDTEST_EVAL_B64_ANTHROPIC_CUSTOM_HEADERS"]).decode(), headers)
                return 0
            self.assertEqual(container.execute(["claude", "--version"], trial,
                {"CANARY": "ordinary-value", "ANTHROPIC_CUSTOM_HEADERS": headers},
                10, lambda _: None, None, executor), 0)
            self.assertFalse(Path(seen[0][seen[0].index("--env-file") + 1]).exists())

    def test_invalid_environment_cannot_reach_docker(self):
        container = isolation.Container(Path("/tmp"), "sha256:" + "0" * 64)
        for env in ({"INVALID=KEY": "value"}, {"HEADER": "value\0invalid"}):
            with self.assertRaisesRegex(ValueError, "cannot be forwarded"):
                container.execute(["claude"], Path("/tmp"), env, 10, None, None, None)


@unittest.skipUnless(IMAGE,'Set DDTEST_EVAL_TEST_IMAGE to the Dockerfile offline target image ID')
class ContainerTests(unittest.TestCase):
    def setUp(self):
        # On macOS/Colima choose a test root the VM shares. CI uses /tmp.
        base=Path(os.environ.get('DDTEST_EVAL_TEST_TMPDIR',tempfile.gettempdir()))
        self.storage=tempfile.TemporaryDirectory(dir=base)
        self.addCleanup(self.storage.cleanup)
        self.root=Path(self.storage.name).resolve()
        self.binary=self.root/'ddtest';self.binary.write_text('#!/bin/sh\nexit 0\n');self.binary.chmod(0o755)
        self.args=type('Args',(),dict(repo='fixture-source',ref='HEAD',ddtest=self.binary,agent='codex',
            image=IMAGE,setup_command=[],output=self.root/'evidence',model=None,timeout=20))()
        self.addCleanup(self.remove_trials)

    def remove_trials(self):
        for trial in self.root.glob('dde-*'):
            shutil.rmtree(trial,ignore_errors=True)

    def trial(self):
        with patch.object(run_eval,'Container',FixtureContainer),patch.object(run_eval,'trial_root',return_value=self.root),contextlib.redirect_stdout(io.StringIO()),contextlib.redirect_stderr(io.StringIO()):
            code=run_eval.run(self.args)
        manifest=json.loads((self.args.output/'run.json').read_text())
        return code,manifest

    def test_agents_and_setup_cannot_read_host_home_or_credentials(self):
        canary=self.root/'outside-credentials';canary.write_text('HOST-CREDENTIAL-CANARY')
        for agent in ('codex','claude'):
            self.args.agent=agent;self.args.output=self.root/(agent+'-evidence')
            # This file is outside the only mounted trial directory.
            script=f"import os,pathlib,json; assert not pathlib.Path({str(canary)!r}).exists(); pathlib.Path(os.environ['HOME'],'setup.json').write_text(json.dumps(dict(os.environ)))"
            import shlex
            self.args.setup_command=[shlex.join(['/usr/bin/python3','-c',script])]
            with patch.dict(os.environ,{'AWS_ACCESS_KEY_ID':'dummy-cloud-key','CLAUDE_CODE_SESSION_ID':'parent-session','GH_TOKEN':'dummy-github-token'}):
                code,manifest=self.trial()
            self.assertEqual(code,0,str(manifest.get('error'))+json.dumps(manifest['commands'][-1:]))
            self.assertEqual(manifest['isolation']['cleanup'],'removed')
            observed=json.loads((Path(manifest['checkout'])/'observed.json').read_text())
            self.assertTrue(observed['fake_agent'])
            for env in (observed['environment'],json.loads((Path(manifest['trial_directory'])/'home/setup.json').read_text())):
                for key in ('AWS_ACCESS_KEY_ID','GH_TOKEN','CLAUDE_CODE_SESSION_ID','SSH_AUTH_SOCK','NODE_OPTIONS'):
                    self.assertNotIn(key,env)
            activity=(self.args.output/'activity.jsonl').read_text()
            self.assertNotIn('PRIVATE_REASONING',activity)
            self.assertNotIn('fake-secret-value',activity)
            self.assertIn('file_change' if agent=='codex' else 'Write',activity)
        self.assertEqual(canary.read_text(),'HOST-CREDENTIAL-CANARY')

    def test_pnpm_configuration_uses_container_paths(self):
        import shlex
        script="import subprocess; p=subprocess.check_output(['pnpm','config','get','store-dir'],text=True).strip(); assert p=='/trial/caches/pnpm-store',p"
        self.args.setup_command=[shlex.join(['/usr/bin/python3','-c',script])]
        code,manifest=self.trial()
        self.assertEqual(code,0,manifest.get('error'))
        self.assertEqual(manifest['isolation']['cleanup'],'removed')

    def test_selected_fake_login_preflight_keeps_credentials_out_of_setup(self):
        import shlex
        for agent,key in (('codex','tokens'),('claude','claudeAiOauth')):
            with self.subTest(agent=agent):
                self.args.agent=agent
                self.args.output=self.root/(agent+'-login-evidence')
                login=self.root/(agent+'-login.json')
                payload={key:{'accessToken':'fake-model-only-token'}}
                login.write_text(json.dumps(payload))
                self.args.login_json=login
                script="import pathlib,os; h=pathlib.Path(os.environ['HOME']); assert not (h/'.codex/auth.json').exists(); assert not (h/'.claude/.credentials.json').exists()"
                self.args.setup_command=[shlex.join(['/usr/bin/python3','-c',script])]
                code,manifest=self.trial()
                self.assertEqual(code,0,manifest.get('error'))
                self.assertEqual(manifest['authentication']['status'],'passed')
                self.assertEqual(manifest['login'],'removed')
                self.assertEqual(json.loads(login.read_text()),payload)
                home=Path(manifest['trial_directory'])/'home'
                self.assertFalse((home/('.codex/auth.json' if agent=='codex' else '.claude/.credentials.json')).exists())

    def options(self,**values):
        import shlex
        script="import pathlib,json; pathlib.Path('fake-options.json').write_text("+repr(json.dumps(values))+"); pathlib.Path('.git/info/exclude').write_text('fake-options.json\\n')"
        self.args.setup_command=[shlex.join(['/usr/bin/python3','-c',script])]

    def test_detached_reparented_stdout_holder_is_killed_on_timeout(self):
        self.options(detach=True)
        self.args.timeout=3
        started=time.monotonic();code,manifest=self.trial()
        self.assertEqual(code,1)
        self.assertIn(manifest.get('failure_kind'),(None,'infrastructure'))
        # Multiple setup commands each have their own deadline; agent pipe itself
        # must not remain held open until the 300-second descendant exits.
        self.assertLess(time.monotonic()-started,30)
        self.assertEqual(manifest['isolation']['cleanup'],'removed')
        time.sleep(4)
        self.assertFalse((Path(manifest['trial_directory'])/'orphan-survived').exists())
        result=subprocess.run(['docker','inspect',manifest['isolation']['name']],capture_output=True)
        self.assertNotEqual(result.returncode,0)

    def test_deadline_removes_detached_descendants_without_killing_unrelated_process(self):
        trial=self.root/'deadline-trial';trial.mkdir()
        container=isolation.Container(trial,IMAGE)
        canary=subprocess.Popen([sys.executable,'-c','import time; time.sleep(30)'])
        sentinel=trial/'orphan-survived'
        child=f"import pathlib,time; time.sleep(3); pathlib.Path({str(sentinel)!r}).touch(); time.sleep(300)"
        wrapper=f"import subprocess,sys,time; subprocess.Popen([sys.executable,'-c',{child!r}],start_new_session=True); time.sleep(300)"
        env=isolation.environment(trial);env['PATH']=container.path
        try:
            container.start()
            with self.assertRaises(TimeoutError):
                container.execute(['/usr/bin/python3','-c',wrapper],trial,env,1,lambda _:None,None,run_eval.execute)
        finally:
            container.close()
            self.assertIsNone(canary.poll())
            canary.kill();canary.wait(timeout=5)
        time.sleep(3)
        self.assertFalse(sentinel.exists())

    def test_forwarded_environment_is_exact_and_absent_from_docker_argv(self):
        trial=self.root/'env-trial';trial.mkdir()
        container=isolation.Container(trial,IMAGE)
        env=isolation.environment(trial);env['PATH']=container.path;env['CANARY']='forwarded-value'
        lines,seen=[],[]
        def executor(arguments,*rest):
            seen.append(arguments)
            return run_eval.execute(arguments,*rest)
        try:
            container.start()
            self.assertEqual(container.execute(['/usr/bin/env'],trial,env,10,lines.append,None,executor),0)
        finally:
            container.close()
        expected={key:container.inside(value) for key,value in env.items()}
        self.assertEqual(dict(line.split('=',1) for line in lines if line),expected)
        self.assertNotIn('forwarded-value',' '.join(seen[0]))

    def test_multiline_values_are_restored_exactly_inside_the_container(self):
        trial=self.root/'multiline-trial';trial.mkdir()
        container=isolation.Container(trial,IMAGE)
        env=isolation.environment(trial);env['PATH']=container.path;env['HEADERS']='source: a\norg-id: b'
        lines=[]
        try:
            container.start()
            self.assertEqual(container.execute(['/bin/sh','-c','printf "%s|" "$HEADERS"; env | grep -c DDTEST_EVAL_B64_ || true'],
                                               trial,env,10,lines.append,None,run_eval.execute),0)
        finally:
            container.close()
        self.assertEqual(lines,['source: a','org-id: b|0'])

    def test_stopping_processes_ends_a_detached_agent_and_keeps_the_container(self):
        trial=self.root/'stop-trial';trial.mkdir()
        container=isolation.Container(trial,IMAGE)
        sentinel=trial/'still-writing'
        child=f"import pathlib,time\nwhile True:\n pathlib.Path({str(sentinel)!r}).touch();time.sleep(.1)"
        wrapper=f"import subprocess,sys,time; subprocess.Popen([sys.executable,'-c',{child!r}],start_new_session=True); time.sleep(300)"
        env=isolation.environment(trial);env['PATH']=container.path
        try:
            container.start()
            # A timed-out agent: its docker exec client dies, the process does not.
            with self.assertRaises(TimeoutError):
                container.execute(['/usr/bin/python3','-c',wrapper],trial,env,2,lambda _:None,None,run_eval.execute)
            self.assertTrue(sentinel.exists())
            container.stop_processes()
            sentinel.unlink(missing_ok=True);time.sleep(1)
            self.assertFalse(sentinel.exists(),'the agent kept writing after it was stopped')
            listing=container.control('exec',container.name,'/bin/sh','-c',isolation.LIST_PROCESSES).stdout.split()
            self.assertEqual(sorted(listing),sorted(container.keepalive))
            self.assertEqual(container.execute(['true'],trial,env,10,lambda _:None,None,run_eval.execute),0)
        finally:
            container.close()

    def test_controller_pipe_eof_stops_the_pid_namespace(self):
        trial=self.root/'pipe-eof-trial';trial.mkdir()
        container=isolation.Container(trial,IMAGE)
        try:
            container.start()
            container.connection.stdin.close()
            # --rm: the stopped container is also removed, so nothing is left after SIGKILL.
            deadline=time.monotonic()+10
            while time.monotonic()<deadline:
                result=container.control('inspect','--format','{{.State.Running}}',container.name,check=False)
                state='removed' if isolation.missing(result) else result.stdout.strip()
                if state=='removed':break
                time.sleep(.1)
            self.assertEqual(state,'removed')
        finally:
            container.close()

    def test_terminal_signals_write_evidence_and_remove_the_container(self):
        self.options(detach=True,wait=True)
        for number in (signal.SIGTERM,signal.SIGHUP):
            with self.subTest(signal=number):
                output=self.root/('signal-'+str(number));self.args.output=output
                arguments={**vars(self.args.__class__),**vars(self.args)};arguments={k:v for k,v in arguments.items() if not k.startswith('__')}
                arguments.update(ddtest=str(self.binary),output=str(output))
                worker=self.root/'worker.py'
                worker.write_text("import json,sys\nfrom pathlib import Path\nfrom unittest.mock import patch\nimport run_eval\nfrom test_container import FixtureContainer\nfrom isolation import interrupted\n"+
                    "args=type('Args',(),json.loads("+repr(json.dumps(arguments))+"))()\nargs.ddtest=Path(args.ddtest);args.output=Path(args.output)\n"+
                    "with patch.object(run_eval,'Container',FixtureContainer),patch.object(run_eval,'trial_root',return_value=Path("+repr(str(self.root))+")),interrupted():\n sys.exit(run_eval.run(args))\n")
                env=dict(os.environ,PYTHONPATH=str(Path(__file__).parent.resolve()))
                (self.root/'active.json').unlink(missing_ok=True)
                process=subprocess.Popen([sys.executable,str(worker)],stdout=subprocess.DEVNULL,stderr=subprocess.PIPE,env=env)
                try:
                    deadline=time.monotonic()+30
                    while time.monotonic()<deadline:
                        marker=self.root/'active.json'
                        if marker.exists():
                            trial=Path(json.loads(marker.read_text())['trial'])
                            if (trial/'repo/observed.json').exists():break
                        if process.poll() is not None:
                            detail=json.loads((output/'run.json').read_text()) if (output/'run.json').exists() else {}
                            self.fail(process.stderr.read().decode()+json.dumps(detail.get('commands',[])[-2:]))
                        time.sleep(.05)
                    else:self.fail('Fake agent did not start')
                    process.send_signal(number);_,stderr=process.communicate(timeout=20)
                    self.assertEqual(process.returncode,1,stderr.decode())
                    manifest=json.loads((output/'run.json').read_text())
                    self.assertEqual(manifest['status'],'interrupted')
                    self.assertEqual(manifest['isolation']['cleanup'],'removed')
                    time.sleep(4)
                    self.assertFalse((trial/'orphan-survived').exists())
                finally:
                    if process.poll() is None:process.kill();process.communicate(timeout=10)
                    process.stderr.close()
