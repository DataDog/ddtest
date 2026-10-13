"""Loopback intake for runtime measurements; decode and compare after timing."""
from collections import Counter
from email import policy
from email.parser import BytesParser
import gzip
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import socket
import subprocess
import threading


class Receiver:
    def __init__(self, output, coverage):
        self.output = Path(output)
        self.output.mkdir(parents=True)
        self.coverage = coverage
        self.lock = threading.Lock()
        self.requests = []
        self.bodies = {}
        self.errors = []
        owner = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = 'HTTP/1.1'

            def setup(self):
                super().setup()
                self.connection.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)

            def log_message(self, *args):
                pass

            def body(self):
                if self.headers.get('Transfer-Encoding', '').lower() != 'chunked':
                    length = int(self.headers.get('Content-Length', '0'))
                    if length > 32 << 20:
                        raise ValueError('request exceeds the receiver limit')
                    return self.rfile.read(length)
                parts, size = [], 0
                while True:
                    length = int(self.rfile.readline().split(b';', 1)[0], 16)
                    if not length:
                        while self.rfile.readline().strip():
                            pass
                        return b''.join(parts)
                    size += length
                    if size > 32 << 20:
                        raise ValueError('chunked request exceeds the receiver limit')
                    data = self.rfile.read(length)
                    if len(data) != length or self.rfile.read(2) != b'\r\n':
                        raise ValueError('truncated chunked request')
                    parts.append(data)

            def reply(self, code, body=b'', content_type='application/json'):
                self.send_response(code)
                self.send_header('Content-Type', content_type)
                self.send_header('Content-Length', str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def do_GET(self):
                if self.path != '/info':
                    owner.errors.append('unexpected GET ' + self.path)
                    self.reply(404)
                    return
                self.reply(200, json.dumps({'version': '7.70.0',
                    'endpoints': ['/evp_proxy/v2', '/telemetry/proxy/api/v2/apmtelemetry'],
                    'client_drop_p0s': True}).encode())

            def do_POST(self):
                try:
                    raw = self.body()
                    with owner.lock:
                        index = len(owner.requests)
                        filename = f'request-{index:04d}.bin'
                        # Persist after the test process clock has stopped.
                        owner.bodies[filename] = raw
                        owner.requests.append({'path': self.path, 'file': filename,
                            'wire_bytes': len(raw),
                            'encoding': self.headers.get('Content-Encoding', ''),
                            'content_type': self.headers.get('Content-Type', '')})
                    if self.path.endswith('/setting'):
                        attrs = {'itr_enabled': False, 'tests_skipping': False,
                            'require_git': False, 'code_coverage': owner.coverage,
                            'coverage_report_upload_enabled': False,
                            'known_tests_enabled': False, 'impacted_tests_enabled': False,
                            'flaky_test_retries_enabled': False,
                            'early_flake_detection': {'enabled': False},
                            'test_management': {'enabled': False}}
                        value = {'data': {'id': 'benchmark',
                            'type': 'ci_app_test_service_libraries_settings',
                            'attributes': attrs}}
                        self.reply(200, json.dumps(value).encode())
                    elif (self.path.endswith(('/citestcycle', '/citestcov', '/logs', '/cicovreprt'))
                          or 'telemetry' in self.path or self.path.endswith('/traces')
                          or self.path.endswith('/stats')):
                        self.reply(202)
                    else:
                        owner.errors.append('unexpected POST ' + self.path)
                        self.reply(404)
                except Exception as error:
                    owner.errors.append(str(error))
                    self.reply(400)

        self.server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.server.daemon_threads = True
        self.server.block_on_close = False
        self.url = 'http://127.0.0.1:' + str(self.server.server_address[1])
        self.thread = threading.Thread(target=self.server.serve_forever,
                                       kwargs={'poll_interval': .02}, daemon=True)
        self.thread.start()

    def close(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)
        if self.thread.is_alive():
            raise RuntimeError('receiver did not stop')
        for filename, body in self.bodies.items():
            (self.output / filename).write_bytes(body)
        self.bodies.clear()
        (self.output / 'requests.json').write_text(json.dumps(self.requests, indent=2) + '\n')
        if self.errors:
            raise RuntimeError('receiver errors: ' + repr(self.errors))

    def decoded(self, decoder):
        events, coverage = [], []
        traffic = Counter()
        paths = Counter()
        for request in self.requests:
            path = request['path']
            paths[path] += 1
            raw = (self.output / request['file']).read_bytes()
            traffic['wire_bytes'] += len(raw)
            body = gzip.decompress(raw) if request['encoding'] == 'gzip' else raw
            traffic['uncompressed_bytes'] += len(body)
            if path.endswith('/citestcycle'):
                envelope = decode(decoder, body)
                if envelope['version'] != 1:
                    raise ValueError('invalid CI event envelope')
                metadata = envelope.get('metadata', {})
                for event in envelope['events']:
                    effective = dict(metadata.get('*', {}))
                    effective.update(metadata.get(event['type'], {}))
                    effective.update(event['content'].get('meta', {}))
                    events.append({'type': event['type'], 'meta': effective,
                                   'content': event['content']})
                traffic['event_payloads'] += 1
                traffic['event_wire_bytes'] += len(raw)
                traffic['event_uncompressed_bytes'] += len(body)
            elif path.endswith('/citestcov'):
                message = BytesParser(policy=policy.default).parsebytes(
                    ('Content-Type: ' + request['content_type'] + '\r\n\r\n').encode() + body)
                if not message.is_multipart():
                    raise ValueError('coverage is not multipart')
                for part in message.iter_parts():
                    if part.get_param('name', header='content-disposition') == 'coveragex':
                        envelope = decode(decoder, part.get_payload(decode=True))
                        if envelope['version'] != 2:
                            raise ValueError('invalid coverage envelope')
                        coverage += envelope['coverages']
        kinds = Counter(event['type'] for event in events)
        tests = Counter((e['meta'].get('test.module', ''), e['meta'].get('test.suite', ''),
                         e['meta'].get('test.name', ''), e['meta'].get('test.status', ''))
                        for e in events if e['type'] == 'test')
        return {'event_counts': dict(kinds), 'tests': [list(k) + [v] for k, v in sorted(tests.items())],
                'coverage_items': len(coverage), 'traffic': dict(traffic), 'requests': dict(paths)}


def decode(decoder, data):
    result = subprocess.run([str(decoder)], input=data, capture_output=True,
                            check=True, timeout=30)
    return json.loads(result.stdout)
