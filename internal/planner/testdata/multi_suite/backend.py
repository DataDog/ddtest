"""Local-only manual QA backend. Edit scenario.json between planning runs."""
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        self.rfile.read(int(self.headers.get("Content-Length", 0)))
        scenario = json.loads(Path(__file__).with_name("scenario.json").read_text())
        data = {}
        if self.path.endswith("/setting"):
            data = {"data": {"id": "qa", "type": "ci_app_tracers_test_service_settings", "attributes": {
                "itr_enabled": True, "tests_skipping": True, "require_git": False,
            }}}
        elif self.path.endswith("/skippable"):
            data = {"data": [{"id": name, "type": "suite", "attributes": {
                "suite": f"{name} at ./spec/multiple_spec.rb", "configurations": {"test.bundle": "rspec"},
            }} for name in scenario["skippable"]]}
        elif self.path.endswith("/test_suite_durations"):
            suites = {"A at ./spec/multiple_spec.rb": {
                "source_file": "spec/multiple_spec.rb", "duration": {"p50": "10000000000"},
            }}
            if scenario["duration_b"] is not None:
                suites["B at ./spec/multiple_spec.rb"] = {
                    "source_file": "spec/multiple_spec.rb", "duration": {"p50": scenario["duration_b"]},
                }
            data = {"data": {"attributes": {"test_suites": {"rspec": suites}}}}
        elif self.path.endswith("/search_commits"):
            data = {"data": []}
        payload = json.dumps(data).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, *args):
        pass


if __name__ == "__main__":
    server = ThreadingHTTPServer(("127.0.0.1", 8765), Handler)
    print("QA backend: http://127.0.0.1:8765 (Ctrl-C to stop)", flush=True)
    server.serve_forever()
