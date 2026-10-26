"""Read current product JSON instead of letting the evaluator schema drift."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

import run_eval


class ProductContractTests(unittest.TestCase):
    def test_actual_go_reports_supply_every_consumed_field(self):
        root=Path(__file__).resolve().parents[3]
        with tempfile.TemporaryDirectory() as temporary:
            output=Path(temporary)/'reports.json'
            command = subprocess.run(['go','test','./internal/testdrive','-run','^TestExportEvaluatorReportContract$','-count=1'],
                           cwd=root,env=dict(os.environ,DDTEST_EVAL_CONTRACT_OUTPUT=str(output)),
                           check=False,capture_output=True,text=True,timeout=180)
            self.assertEqual(command.returncode,0,command.stdout+command.stderr)
            reports=json.loads(output.read_text())
        frameworks = {"jest", "vitest", "mocha", "playwright", "cypress", "cucumber", "webdriverio",
                      "junit4", "junit5", "testng", "spock", "cucumber-java", "karate", "scalatest", "munit"}
        self.assertEqual({report["framework"] for report in reports}, frameworks)
        self.assertEqual(len(reports), 2 * len(frameworks))
        for report in reports:
            with self.subTest(framework=report['framework'],check_only=report['check_only']):
                self.assertIsInstance(report['schema_version'],int)
                self.assertIsInstance(report['working_directory'],str)
                self.assertIsInstance(report['html_report_current'],bool)
                summary=report['summary']
                for field in ('status','local_validation','final_response','local_node'):
                    self.assertIsInstance(summary[field],str)
                self.assertIsInstance(summary['blocking_checks'],list)
                self.assertIsInstance(summary['selected_validation']['status'],str)
                executed=summary['executed_validation']
                for field in ('status','passed','configurations'):self.assertIn(field,executed)
                coverage=summary['configuration_coverage']
                for field in ('validated','discovered','executed','paired','blocked','not_exercised','incomplete'):
                    self.assertIsInstance(coverage[field],int)
                for field in ('test_executions','source_test_identities'):self.assertIn(field,report['reporting_counts'])
                self.assertIsInstance(report['last_execution']['current'],bool)
                self.assertIsInstance(report['configuration_check'],dict)
                self.assertIsInstance(report['preflight']['node'],str)
                self.assertEqual(report['configurations'][0]['preflight']['node'],'24.19.0')
                if not report['check_only']:
                    self.assertEqual(report['runs'][0]['framework_node'],'22.0.0')
                for configuration in report['configurations']:
                    if configuration.get('preflight'):
                        self.assertIsInstance(configuration['preflight']['node'],str)
                for run in report.get('runs',[]):
                    if 'framework_node' in run:self.assertIsInstance(run['framework_node'],str)
                result=run_eval.reporting_verdict(summary['final_response'],report)
                self.assertEqual(result['status'],'pass',result)
                runtime=run_eval.report_runtime(report,'24.19.0')
                self.assertEqual(runtime['status'],'matched',runtime)
                self.assertEqual(run_eval.reporting_verdict(summary['final_response']+'\nOnboarding is complete.',report)['status'],'fail')
                for claim in ('Blockers: none','Onboarding successfully completed.','Status: COMPLETE LOCALLY'):
                    self.assertEqual(run_eval.reporting_verdict(summary['final_response']+'\n'+claim,report)['status'],'fail',claim)
                self.assertEqual(run_eval.reporting_verdict(summary['final_response']+'\nI have fully onboarded this repository.',report)['status'],'review')
                # On a stale report a count stated as current is a contradiction, not just unsupported.
                expected='review' if report['last_execution']['current'] else 'fail'
                self.assertEqual(run_eval.reporting_verdict(summary['final_response']+'\nThere are 32 unique tests.',report)['status'],expected)
                if not report['last_execution']['current']:
                    claim='\nCurrent run: 96 test executions; 32 source-test identities.'
                    self.assertEqual(run_eval.reporting_verdict(summary['final_response']+claim,report)['status'],'fail')
