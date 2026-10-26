"""Offline regressions for the evaluator's isolation and evidence contract."""

import json
import os
from pathlib import Path
import shlex
import signal
import subprocess
import sys
import unittest
from unittest.mock import patch

import isolation
import run_eval
from isolation import cleanup, environment, interrupted, check_interrupted
import test_run_eval as fixtures


class ReviewTests(unittest.TestCase):
    def setUp(self):
        self.case = fixtures.RunnerTests()
        self.case.setUp()
        self.addCleanup(self.case.doCleanups)

    def test_agent_and_setup_receive_no_host_credentials_or_parent_session(self):
        host = self.case.root/'host-home'
        host.mkdir()
        (host/'.aws').mkdir()
        (host/'.aws/credentials').write_text('host-credential-canary')
        unwanted = {key:'dummy-credential-value' for key in (
            'AWS_ACCESS_KEY_ID','AWS_SECRET_ACCESS_KEY','GOOGLE_APPLICATION_CREDENTIALS',
            'KUBECONFIG','NPM_TOKEN','GITHUB_TOKEN','GH_TOKEN','OPENAI_API_KEY','ANTHROPIC_API_KEY',
            'SSH_AUTH_SOCK','CLAUDECODE','CLAUDE_CODE_SESSION_ID','CLAUDE_CODE_CHILD_SESSION',
            'CLAUDE_CODE_MESSAGING_SOCKET','CLAUDE_CODE_MESSAGING_TOKEN','ANTHROPIC_CUSTOM_HEADERS',
            'NODE_OPTIONS','NODE_PATH')}
        unwanted['HOME'] = str(host)
        source = "import json,os,pathlib; pathlib.Path(os.environ['HOME'],'setup-env.json').write_text(json.dumps(dict(os.environ)))"
        self.case.args.setup_command = [shlex.join([sys.executable,'-c',source])]
        code, manifest = self.case.trial(unwanted)
        self.assertEqual(code,0)
        trial = Path(manifest['trial_directory'])
        observed = json.loads((Path(manifest['checkout'])/'observed.json').read_text())
        for env in (observed['environment'],json.loads((trial/'home/setup-env.json').read_text())):
            for key in unwanted.keys()-{'HOME'}:
                self.assertNotIn(key,env)
            self.assertEqual(env['HOME'],str(trial/'home'))
        self.assertFalse((trial/'home/.aws').exists())
        self.assertEqual((host/'.aws/credentials').read_text(),'host-credential-canary')

    def test_only_selected_login_is_copied_after_setup(self):
        for agent,key in (('codex','tokens'),('claude','claudeAiOauth')):
            with self.subTest(agent=agent):
                self.case.args.agent = agent
                self.case.args.output = self.case.root/(agent+'-login-evidence')
                login = self.case.root/(agent+'-login.json')
                login.write_text(json.dumps({key:{'accessToken':'throwaway-token'},'mcpServers':{'datadog':{}},'hooks':{'bad':'command'}}))
                self.case.args.login_json = login
                self.case.args.setup_command = [shlex.join([sys.executable,'-c',"import pathlib,os; assert not pathlib.Path(os.environ['HOME'],'.codex/auth.json').exists(); assert not pathlib.Path(os.environ['HOME'],'.claude/.credentials.json').exists()"])]
                code,manifest = self.case.trial({'CLAUDE_CODE_SESSION_ID':'parent-session'})
                self.assertEqual(code,0)
                home=Path(manifest['trial_directory'])/'home'
                file=home/('.codex/auth.json' if agent=='codex' else '.claude/.credentials.json')
                observed=json.loads((Path(manifest['checkout'])/'observed.json').read_text())
                data=observed['login'][str(file)]
                self.assertEqual(set(data),{key,'auth_mode'} if agent=='codex' else {key})
                self.assertEqual(data[key],{'accessToken':'throwaway-token'})
                # The agent saw it; the retained trial must not keep it.
                self.assertFalse(file.exists())
                self.assertEqual(manifest['login'],'removed')
                self.assertTrue(Path(observed['environment']['CODEX_HOME']).is_relative_to(home))
                if agent=='claude':
                    argv=observed['argv']
                    self.assertEqual(argv[argv.index('--setting-sources')+1],'')
                    self.assertIn('--strict-mcp-config',argv)
                    self.assertEqual(json.loads(argv[argv.index('--mcp-config')+1]),{'mcpServers':{}})
                else:
                    self.assertIn('mcp_servers={}',observed['argv'])

    def login(self):
        login=self.case.root/'codex-login.json'
        login.write_text(json.dumps({'tokens':{'accessToken':'throwaway-token'}}))
        self.case.args.login_json=login
        outside=self.case.root/'outside';outside.mkdir()
        return outside

    def test_setup_cannot_redirect_the_login_outside_the_trial(self):
        for replaced in ('home/.codex','home'):
            with self.subTest(replaced=replaced):
                outside=self.login()
                self.case.args.output=self.case.root/('evidence-'+replaced.replace('/','-'))
                # Setup is repository-controlled and runs before the login is written.
                script=("import os,pathlib,shutil; trial=pathlib.Path(os.environ['HOME']).parent; "
                        f"target=trial/{replaced!r}; shutil.rmtree(target); os.symlink({str(outside)!r},target)")
                self.case.args.setup_command=[shlex.join([sys.executable,'-c',script])]
                code,manifest=self.case.trial()
                self.assertEqual(code,1)
                self.assertEqual(manifest['status'],'failed')
                self.assertIn('is not a plain directory in the trial',manifest['error'])
                self.assertEqual(list(outside.iterdir()),[],'nothing may be written outside the trial')
                self.assertNotIn('login',manifest)
                (Path(manifest['trial_directory'])/replaced).unlink()
                outside.rmdir()

    def test_setup_cannot_redirect_the_login_file_outside_the_trial(self):
        outside=self.login()
        canary=outside/'auth.json';canary.write_text('outside-file')
        script=("import os,pathlib; home=pathlib.Path(os.environ['HOME']); "
                f"os.symlink({str(canary)!r},home/'.codex/auth.json')")
        self.case.args.setup_command=[shlex.join([sys.executable,'-c',script])]
        code,manifest=self.case.trial()
        self.assertEqual(code,0,manifest.get('error'))
        self.assertEqual(canary.read_text(),'outside-file')
        observed=json.loads((Path(manifest['checkout'])/'observed.json').read_text())
        file=Path(manifest['trial_directory'])/'home/.codex/auth.json'
        self.assertEqual(observed['login'][str(file)]['tokens'],{'accessToken':'throwaway-token'})
        self.assertFalse(file.exists() or file.is_symlink())
        self.assertEqual(manifest['login'],'removed')

    def test_agent_is_stopped_before_evidence_is_read(self):
        calls=[]
        class Recording(fixtures.FakeContainer):
            def stop_processes(self):
                calls.append('stop')
            def execute(self,argv,*rest):
                calls.append(argv[:2])
                return super().execute(argv,*rest)
        with patch.object(fixtures,'FakeContainer',Recording):
            code,manifest=self.case.trial()
        self.assertEqual(code,0)
        self.assertTrue(manifest['isolation']['processes_stopped'])
        stop=calls.index('stop')
        self.assertTrue(all(call!=['git','diff'] for call in calls[:stop]))
        self.assertIn(['git','diff'],calls[stop:])

    def test_forwarded_values_never_reach_the_docker_command_line(self):
        trial=self.case.root/'values-trial';trial.mkdir()
        container=isolation.Container(trial,'sha256:'+'0'*64)
        container.container_environment=['PATH','NODE_VERSION','HOSTNAME']
        seen={}
        def executor(arguments,cwd,env,timeout,on_line,prompt):
            env_file=Path(arguments[arguments.index('--env-file')+1])
            seen.update(arguments=arguments,mode=env_file.stat().st_mode&0o777,content=env_file.read_text(),file=env_file)
            self.assertNotIn('secret-value',' '.join(arguments))
            return 0
        container.execute(['node','--version'],trial,{'PATH':'/usr/bin','TOKEN':'secret-value'},5,lambda _:None,None,executor)
        self.assertEqual(seen['mode'],0o600)
        self.assertEqual(seen['content'],'PATH=/usr/bin\nTOKEN=secret-value\n')
        self.assertFalse(seen['file'].exists())
        arguments=seen['arguments']
        self.assertEqual(arguments[arguments.index('/usr/bin/env')+1:],['-u','NODE_VERSION','-u','HOSTNAME','node','--version'])
        # Multi-line values (managed model headers) travel base64-encoded in the
        # env file and are restored by a shell wrapper inside the container.
        container.execute(['true'],trial,{'HEADERS':'a: 1\nb: 2'},5,lambda _:None,None,executor)
        self.assertEqual(seen['content'],'DDTEST_EVAL_B64_HEADERS=YTogMQpiOiAy\n')
        arguments=seen['arguments']
        wrapped=arguments[arguments.index('/bin/sh'):]
        self.assertEqual(wrapped[1],'-c')
        self.assertEqual(wrapped[3:],['sh','true'])
        self.assertNotIn('a: 1',' '.join(arguments))
        with self.assertRaises(ValueError):
            container.execute(['true'],trial,{'BAD':'nul\0byte'},5,lambda _:None,None,executor)

    def test_low_storage_stops_before_clone_or_model(self):
        from types import SimpleNamespace
        with patch.object(run_eval.shutil,'disk_usage',return_value=SimpleNamespace(free=0)):
            code,manifest=self.case.trial()
        self.assertEqual(code,1)
        self.assertEqual(manifest['failure_kind'],'evaluator_environment')
        self.assertEqual(manifest['failure_stage'],'storage_readiness')
        self.assertEqual(manifest['commands'],[])
        self.assertEqual(manifest['isolation']['cleanup'],'removed')

    def test_ref_is_checked_at_programmatic_entry_and_explicitly_fetched(self):
        self.case.args.ref='--upload-pack=bad'
        with self.assertRaises(ValueError):
            run_eval.run(self.case.args)
        self.assertFalse(self.case.args.output.exists())
        self.case.args.ref='HEAD'
        code,manifest=self.case.trial()
        self.assertEqual(code,0)
        self.assertIn(['git','fetch','--update-shallow','--no-tags','origin','HEAD'],[c['argv'] for c in manifest['commands']])
        self.assertEqual(manifest['submodules'],'not requested')

    def test_malformed_nested_report_still_finalizes_evidence_and_cleanup(self):
        code, manifest = self.case.trial({'FAKE_REPORT_JSON': json.dumps({'summary': []})})
        self.assertEqual(code, 1)
        self.assertEqual(manifest['status'], 'failed')
        self.assertEqual(manifest['isolation']['cleanup'], 'removed')
        self.assertEqual(manifest['agent_reporting']['status'], 'review')
        self.assertIn('Invalid report fields', manifest['collection_errors'][0])

    def test_ref_outside_branches_and_tags_is_fetched(self):
        source = self.case.root / 'source.git'
        def git(*args):
            return subprocess.check_output(['git', *args], text=True, stderr=subprocess.PIPE).strip()
        git('clone', '--bare', '--depth=1', '--single-branch', '--no-tags', '--',
            Path(self.case.source).as_uri(), str(source))
        expected = git('-C', str(source), 'rev-parse', 'HEAD')
        branch = git('-C', str(source), 'symbolic-ref', 'HEAD')
        git('-C', str(source), 'update-ref', 'refs/pull/1/head', expected)
        # CI has a depth-one checkout. Remove branch reachability without
        # requiring a parent commit or creating an unsigned fixture commit.
        git('-C', str(source), 'update-ref', '-d', branch)
        self.case.args.repo = str(source)
        self.case.args.ref = 'refs/pull/1/head'
        code, manifest = self.case.trial()
        self.assertEqual(code, 0, manifest)
        self.assertEqual(manifest['commit'], expected)

    def test_cleanup_requires_bound_marker_and_preserves_evidence_and_other_roots(self):
        code,manifest=self.case.trial()
        self.assertEqual(code,0)
        trial=Path(manifest['trial_directory'])
        canary=self.case.root/'keep';canary.write_text('original')
        marker=trial/'.ddtest-eval-owner.json'
        original=marker.read_text();marker.write_text('{}')
        with self.assertRaises(ValueError):cleanup(self.case.args.output)
        self.assertTrue(trial.exists())
        marker.write_text(original)
        runfile=self.case.args.output/'run.json'
        runoriginal=runfile.read_text()
        runfile.write_text(json.dumps({**manifest,'trial_directory':str(self.case.root)}))
        with self.assertRaises(ValueError):cleanup(self.case.args.output)
        self.assertEqual(canary.read_text(),'original')
        runfile.write_text(runoriginal)
        self.assertEqual(cleanup(self.case.args.output),trial)
        self.assertFalse(trial.exists())
        self.assertTrue(runfile.exists())
        self.assertEqual(canary.read_text(),'original')

    def test_cleanup_rejects_symlinks(self):
        _,manifest=self.case.trial()
        root=Path(manifest['trial_directory'])
        rootmarker=root/'.ddtest-eval-owner.json'
        saved=rootmarker.read_text();rootmarker.unlink()
        target=self.case.root/'target';target.write_text(saved)
        rootmarker.symlink_to(target)
        with self.assertRaises(ValueError):cleanup(self.case.args.output)
        self.assertTrue(target.exists())

    def test_signal_handler_restores_handlers_and_requests_durable_teardown(self):
        for number in (signal.SIGTERM,signal.SIGHUP):
            previous=signal.getsignal(number)
            with self.assertRaises(KeyboardInterrupt):
                with interrupted():
                    os.kill(os.getpid(),number)
                    check_interrupted()
            self.assertEqual(signal.getsignal(number),previous)


class EvidenceTests(unittest.TestCase):
    def test_records_all_tools_with_targets_but_never_private_reasoning(self):
        events=[]
        for kind,fields in (
            ('file_change',{'changes':[{'path':'src/test.js','kind':'update'}]}),
            ('mcp_tool_call',{'server':'datadog','tool':'query','arguments':{'url':'https://example.com'}}),
            ('web_search',{'query':'framework docs'})):
            events.extend(run_eval.public_events({'type':'item.completed','item':{'type':kind,**fields}},'codex'))
        for name,args in (('Write',{'file_path':'/outside/file'}),('Edit',{'file_path':'src/a.js'}),('WebFetch',{'url':'https://example.com'}),('mcp__datadog__query',{'query':'test'})):
            events.extend(run_eval.public_events({'type':'assistant','message':{'content':[{'type':'tool_use','name':name,'input':args}]}},'claude'))
        events.extend(run_eval.public_events({'type':'assistant','message':{'content':[
            {'type':'server_tool_use','name':'web_search','input':{'query':'public docs'}}]}},'claude'))
        text=json.dumps(events)
        for target in ('src/test.js','datadog','https://example.com','/outside/file','src/a.js'):
            self.assertIn(target,text)
        self.assertIn('public docs',text)
        for agent,event in (
            ('codex',{'type':'item.completed','item':{'type':'reasoning','text':'PRIVATE'}}),
            ('claude',{'type':'assistant','message':{'content':[{'type':'thinking','thinking':'PRIVATE'}]}})):
            self.assertNotIn('PRIVATE',json.dumps(list(run_eval.public_events(event,agent))))

    def test_redacts_before_excerpting_credentials_from_every_evidence_channel(self):
        secret='a-known-dummy-credential'
        with patch.dict(os.environ,{'AWS_SECRET_ACCESS_KEY':secret}):
            value={'api_key':'not-in-env','message':f'AWS_SECRET_ACCESS_KEY={secret}; token=unknown-value; https://example.com',
                   'pem':'-----BEGIN PRIVATE KEY-----\nVERYSECRET\n-----END PRIVATE KEY-----',
                   'tool_result':'sk-1234567890abcdefghijk'}
            text=json.dumps(run_eval.redact(value))
        for denied in (secret,'not-in-env','unknown-value','VERYSECRET','sk-1234567890abcdefghijk'):
            self.assertNotIn(denied,text)
        self.assertIn('https://example.com',text)

    def test_setup_private_keys_remain_redacted_across_lines_and_excerpt_boundaries(self):
        clean = run_eval.LineRedactor()
        lines = ["-----BEGIN PRIVATE KEY-----", "PRIVATEBODY"*1000, "-----END PRIVATE KEY-----", "safe diagnostic"]
        text = "\n".join(clean(line) for line in lines)
        self.assertNotIn("PRIVATEBODY",text)
        self.assertIn("safe diagnostic",text)
        self.assertIn("REDACTED PRIVATE KEY",text)

    def test_incomplete_reports_reject_success_claims_and_stronger_counts(self):
        fixture=fixtures.VerdictTests();report=fixture.compact_report()
        block=fixture.compact_answer()+'Blockers: 1. Full blockers and commands are in Results JSON.\n'
        report['summary'].update(final_response=block,blocking_checks=['unavailable Firefox'])
        self.assertEqual(run_eval.reporting_verdict(block,report)['status'],'pass')
        for claim,expected in (('Blockers: none','fail'),
                               ('Onboarding successfully completed.','fail'),
                               ('Status: COMPLETE LOCALLY','fail'),
                               ('I have fully onboarded this repository.','review'),
                               ('There are 32 unique tests.','review'),
                               ('There are 32 distinct tests.','review'),
                               ('The repository is not fully onboarded yet.','pass'),
                               ('Onboarding is not complete.','pass')):
            with self.subTest(claim=claim):
                result=run_eval.reporting_verdict(block+claim+'\n',report)
                self.assertEqual(result['status'],expected,result)
        report['summary']['status']='COMPLETE LOCALLY'
        complete=block.replace('Onboarding validation: INCOMPLETE','Onboarding validation: COMPLETE LOCALLY')
        report['summary'].update(final_response=complete,blocking_checks=[])
        self.assertEqual(run_eval.reporting_verdict(complete,report)['status'],'pass')
        self.assertEqual(run_eval.reporting_verdict(complete+'I have fully onboarded this repository.\n',report)['status'],'pass')

    def test_historical_counts_cannot_be_restated_as_current(self):
        fixture=fixtures.VerdictTests();report=fixture.compact_report()
        report['last_execution']={'current':False}
        canonical=fixture.compact_answer().replace('Reporting:','Historical reporting (not revalidated):')
        report['summary']['final_response']=canonical
        self.assertEqual(run_eval.reporting_verdict(canonical,report)['status'],'pass')
        for claim in ('Current run: 96 test executions; 32 source-test identities.',
                      'This run executed 96 test executions.',
                      'Reporting: 96 test executions; 32 source-test identities.'):
            with self.subTest(claim=claim):
                self.assertEqual(run_eval.reporting_verdict(canonical+claim+'\n',report)['status'],'fail')
        self.assertEqual(run_eval.reporting_verdict(canonical+'The earlier run had 96 test executions.\n',report)['status'],'pass')

    def test_missing_blockers_historical_counts_appendix_contradictions_and_conditions(self):
        fixture=fixtures.VerdictTests();report=fixture.compact_report()
        block=fixture.compact_answer()+'Blocking checks: unavailable Firefox.\n'
        report['summary'].update(final_response=block,blocking_checks=['unavailable Firefox'])
        self.assertEqual(run_eval.reporting_verdict(fixture.compact_answer(),report)['status'],'review')
        self.assertEqual(run_eval.reporting_verdict(block+'There are 96 unique tests.',report)['status'],'fail')
        self.assertEqual(run_eval.reporting_verdict(block+'There are no blockers.',report)['status'],'fail')
        self.assertEqual(run_eval.reporting_verdict(block+'Onboarding is complete only after all blocked configurations pass.',report)['status'],'pass')
        for current in (False,True):
            report['last_execution']={'current':current}
            canonical=block if current else block.replace('Reporting:','Historical reporting (not revalidated):')
            report['summary']['final_response']=canonical
            self.assertEqual(run_eval.reporting_verdict(canonical,report)['status'],'pass')
            if not current:
                self.assertEqual(run_eval.reporting_verdict(canonical+'\nReporting: 96 test executions; 32 source-test identities.',report)['status'],'fail')
