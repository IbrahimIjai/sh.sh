#!/usr/bin/env python3
"""Barrage load tests for shortened.cv: `run` (HTTP + Postgres probe) and `chaos` (Postgres faults via Toxiproxy)."""
import argparse
import hashlib
import json
import os
import platform
import shutil
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from urllib.parse import urlsplit, urlunsplit

HERE = Path(__file__).resolve().parent
SERVER = HERE.parent.parent
CACHE = Path(os.environ.get('XDG_CACHE_HOME', Path.home() / '.cache')) / 'shortened-bench'
BARRAGE_VERSION = 'v0.7.0'
TOXIPROXY_VERSION = 'v2.12.0'
TOXIPROXY_API = '127.0.0.1:8474'
PROXY_LISTEN = '127.0.0.1:26000'
BROWSER_UA = 'Mozilla/5.0 (barrage load test)'


def log(message):
    print(f'[barrage-test] {message}', flush=True)


def fail(message):
    log(message)
    sys.exit(1)


def read_env(path):
    env = {}
    if not path.exists():
        return env
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith('#') or '=' not in line:
            continue
        key, value = line.split('=', 1)
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in '"\'':
            value = value[1:-1]
        env[key.strip()] = value
    return env


def decode(raw):
    if not raw:
        return None
    try:
        return json.loads(raw)
    except ValueError:
        return raw.decode(errors='replace')


def request(method, url, body=None, headers=None, timeout=10):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method,
                                 headers={'Content-Type': 'application/json', **(headers or {})})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, decode(resp.read())
    except urllib.error.HTTPError as err:
        return err.code, decode(err.read())


def wait_ready(url, what, timeout=30):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            if request('GET', url, timeout=2)[0] < 300:
                return
        except OSError:
            pass
        time.sleep(0.3)
    fail(f'{what} did not become ready at {url}')


def port_free(port):
    with socket.socket() as s:
        return s.connect_ex(('127.0.0.1', port)) != 0


def barrage_bin():
    found = os.environ.get('BARRAGE') or shutil.which('barrage')
    if found:
        return found
    target = CACHE / f'barrage-{BARRAGE_VERSION}'
    if not target.exists():
        log(f'building barrage {BARRAGE_VERSION} from source (first run only) ...')
        gobin = CACHE / 'gobin'
        subprocess.run(['go', 'install', f'github.com/codetesla51/barrage/cmd/barrage@{BARRAGE_VERSION}'],
                       env={**os.environ, 'GOBIN': str(gobin)}, check=True)
        (gobin / 'barrage').rename(target)
    return str(target)


def toxiproxy_bin():
    found = shutil.which('toxiproxy-server')
    if found:
        return found
    arch = {'x86_64': 'amd64', 'amd64': 'amd64', 'aarch64': 'arm64', 'arm64': 'arm64'}.get(platform.machine().lower())
    asset = f'toxiproxy-server-{platform.system().lower()}-{arch}'
    target = CACHE / f'{asset}-{TOXIPROXY_VERSION}'
    if target.exists():
        return str(target)
    base = f'https://github.com/Shopify/toxiproxy/releases/download/{TOXIPROXY_VERSION}'
    log(f'downloading {asset} {TOXIPROXY_VERSION} (first run only) ...')
    sums = urllib.request.urlopen(f'{base}/checksums.txt', timeout=30).read().decode()
    expected = next((line.split()[0] for line in sums.splitlines() if line.split()[-1:] == [asset]), None)
    if not expected:
        fail(f'no {asset} in the toxiproxy {TOXIPROXY_VERSION} release')
    data = urllib.request.urlopen(f'{base}/{asset}', timeout=180).read()
    if hashlib.sha256(data).hexdigest() != expected:
        fail('toxiproxy download failed its checksum')
    CACHE.mkdir(parents=True, exist_ok=True)
    target.write_bytes(data)
    target.chmod(0o755)
    return str(target)


def go_build(package, name):
    target = CACHE / name
    CACHE.mkdir(parents=True, exist_ok=True)
    subprocess.run(['go', 'build', '-o', str(target), package], cwd=SERVER, check=True)
    return str(target)


class Processes:
    def __init__(self):
        self.running = []

    def start(self, name, cmd, log_path, **kwargs):
        out = open(log_path, 'w')
        self.running.append((name, subprocess.Popen(cmd, stdout=out, stderr=subprocess.STDOUT, **kwargs), out))

    def stop(self):
        for name, proc, out in reversed(self.running):
            if proc.poll() is None:
                proc.terminate()
                try:
                    proc.wait(5)
                except subprocess.TimeoutExpired:
                    proc.kill()
            out.close()
            log(f'stopped {name}')


def via_proxy(url):
    parts = urlsplit(url)
    userinfo = parts.netloc.rsplit('@', 1)[0] + '@' if '@' in parts.netloc else ''
    return urlunsplit(parts._replace(netloc=userinfo + PROXY_LISTEN))


def host_port(url):
    parts = urlsplit(url)
    return f'{parts.hostname}:{parts.port or 5432}'


def create_link(api, sink_port):
    status, body = request('POST', f'{api}/api/v1/links', {'target_url': f'http://localhost:{sink_port}/barrage'})
    if status == 429:
        fail('anonymous link limit reached (20/hour per IP); wait, then rerun')
    if status != 201:
        fail(f'could not create a test link: {status} {body}')
    request('GET', f'{api}/{body["slug"]}', headers={'User-Agent': BROWSER_UA})
    log(f'test link {body["short_url"]} -> {body["target_url"]} (id {body["id"]}, warmed)')
    return body


def recorded_clicks(api, claim_token):
    status, body = request('POST', f'{api}/api/v1/links/lookup', {'claim_tokens': [claim_token]})
    if status != 200 or not body:
        fail(f'lookup failed: {status} {body}')
    return body[0]['click_count']


def config_yaml(args, url, db_url, slug, chaos_upstream=None):
    lines = [
        f'duration: {args.duration}', 'bucket_width: 1s', f'ramp: {args.ramp}', f'concurrency: {args.concurrency}', '',
        'http:', f'  rate: {args.rate}', '  target:', '    method: GET', f'    url: {url}',
        '    header:', f'      user-agent: [{BROWSER_UA}]', '',
        'db:', f'  rate: {args.db_rate}', '  target:', '    driver: postgres', f'    conn: {db_url}',
    ]
    if chaos_upstream:
        lines.append(f'    chaos_conn: {via_proxy(db_url)}')
    lines += ['    max_open_conns: 5', '    queries:',
              f"      - query: SELECT id, target_url FROM links WHERE slug = '{slug}'", '        weight: 1', '        type: read']
    if chaos_upstream:
        lines += ['', 'chaos:', f'  api: {TOXIPROXY_API}', '  proxies:', '    - name: db-proxy',
                  f'      listen: {PROXY_LISTEN}', f'      upstream: {chaos_upstream}', '  faults:',
                  f'    - at: {args.slow_at}', f'      duration: {args.slow_for}', '      proxy: db-proxy',
                  '      type: latency', '      attrs:', f'        latency: {args.slow_ms}',
                  f'    - at: {args.down_at}', f'      duration: {args.down_for}', '      proxy: db-proxy', '      type: down']
    return '\n'.join(lines) + '\n'


def run_barrage(out, config, binary):
    (out / 'config.yaml').write_text(config)
    cmd = [binary, 'run', '-c', 'config.yaml', '--report', 'report.html', '--json', 'results.json', '--no-progress']
    if shutil.which('systemd-inhibit'):
        cmd = ['systemd-inhibit', '--what=idle:sleep', '--who=barrage', '--why=shortened.cv load test'] + cmd
    with (out / 'barrage.log').open('w') as logfile:
        proc = subprocess.Popen(cmd, cwd=out, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        for line in proc.stdout:
            sys.stdout.write(line)
            logfile.write(line)
        proc.wait()
    results = out / 'results.json'
    if not results.exists():
        fail(f'barrage wrote no results; see {out / "barrage.log"}')
    return json.loads(results.read_text()), proc.returncode


def reconcile(api, claim_token, results):
    http_runner = next(r for r in results['runners'] if r['name'] == 'HTTP')
    redirected = sum(n for code, n in http_runner.get('status_codes', {}).items() if code.startswith('2'))
    expected = redirected + 1
    last, stable = None, 0
    for _ in range(60):
        count = recorded_clicks(api, claim_token)
        stable = stable + 1 if count == last else 0
        last = count
        if count >= expected or stable >= 8:
            break
        time.sleep(1)
    return {'expected': expected, 'recorded': last, 'match': last == expected}


def recorder_events(api_log):
    if not api_log.exists():
        return None
    text = api_log.read_text()
    return {'failed_attempts': text.count('click recorder: write of'),
            'recovered_batches': text.count('click recorder: recovered batch'),
            'dropped_lines': text.count('buffer full, dropped')}


def summarize(args, out, results, exit_code, clicks, recorder):
    runners = {r['name']: r for r in results['runners']}
    spikes = results.get('spikes') or []
    summary = {
        'mode': args.mode, 'finished_at': datetime.now(timezone.utc).isoformat(),
        'commit': subprocess.run(['git', 'rev-parse', '--short', 'HEAD'], cwd=SERVER, capture_output=True, text=True).stdout.strip(),
        'settings': {k: str(v) for k, v in vars(args).items()},
        'runners': runners, 'spikes': len(spikes), 'masked_spikes': sum(1 for s in spikes if s.get('masked')),
        'clicks': clicks, 'recorder': recorder, 'barrage_exit_code': exit_code,
    }
    (out / 'summary.json').write_text(json.dumps(summary, indent=2))
    http_runner, db_runner = runners.get('HTTP', {}), runners.get('DB', {})
    print()
    log(f'HTTP     {http_runner.get("requests", 0):,} requests · {http_runner.get("success_percent", 0):.1f}% ok · p99 {http_runner.get("p99_ms")} ms')
    log(f'Postgres {db_runner.get("requests", 0):,} probes · {db_runner.get("success_percent", 0):.1f}% ok · p99 {db_runner.get("p99_ms")} ms')
    masked = f' ({summary["masked_spikes"]} masked: storage slow, redirects unaffected)' if spikes else ''
    log(f'spikes   {len(spikes)}{masked}')
    if recorder:
        log(f'recorder {recorder["failed_attempts"]} failed writes · {recorder["recovered_batches"]} recovered batches · {recorder["dropped_lines"]} drop warnings')
    verdict = 'ALL CLICKS RECORDED' if clicks['match'] else 'CLICK MISMATCH'
    log(f'clicks   {clicks["recorded"]:,} recorded / {clicks["expected"]:,} expected -> {verdict}')
    log(f'report   {out / "report.html"}')
    return 0 if clicks['match'] else 1


def run_mode(args, out, procs, env):
    db_url = args.database_url or env.get('DATABASE_URL')
    if not db_url:
        fail('no DATABASE_URL in server/.env; pass --database-url')
    wait_ready(f'{args.api}/health/ready', 'the API')
    procs.start('sink', [go_build('./scripts/barrage/sink', 'sink'), f'127.0.0.1:{args.sink_port}'], out / 'sink.log')
    wait_ready(f'http://127.0.0.1:{args.sink_port}/', 'the sink')
    binary = barrage_bin()
    link = create_link(args.api, args.sink_port)
    results, code = run_barrage(out, config_yaml(args, f'{args.api}/{link["slug"]}', db_url, link['slug']), binary)
    return summarize(args, out, results, code, reconcile(args.api, link['claim_token'], results), None)


def chaos_mode(args, out, procs, env):
    db_url = args.database_url or env.get('DATABASE_URL')
    if not db_url:
        fail('no DATABASE_URL in server/.env; pass --database-url')
    upstream = host_port(db_url)
    toxi = f'http://{TOXIPROXY_API}'
    try:
        request('GET', f'{toxi}/version', timeout=1)
        log('using the toxiproxy server already running')
    except OSError:
        host, port = TOXIPROXY_API.split(':')
        procs.start('toxiproxy', [toxiproxy_bin(), '-host', host, '-port', port], out / 'toxiproxy.log')
        wait_ready(f'{toxi}/version', 'toxiproxy')
    request('DELETE', f'{toxi}/proxies/db-proxy')
    status, body = request('POST', f'{toxi}/proxies', {'name': 'db-proxy', 'listen': PROXY_LISTEN, 'upstream': upstream, 'enabled': True})
    if status not in (200, 201):
        fail(f'could not create the Postgres proxy: {status} {body}')
    log(f'proxy {PROXY_LISTEN} -> Postgres {upstream}')

    api = f'http://localhost:{args.port}'
    api_env = {**os.environ, **env, 'DATABASE_URL': via_proxy(db_url), 'PORT': str(args.port), 'BASE_URL': api}
    procs.start('api', [go_build('./cmd/api', 'api')], out / 'api.log', env=api_env, cwd=out)
    wait_ready(f'{api}/health/ready', 'the API behind the proxy')
    procs.start('sink', [go_build('./scripts/barrage/sink', 'sink'), f'127.0.0.1:{args.sink_port}'], out / 'sink.log')
    wait_ready(f'http://127.0.0.1:{args.sink_port}/', 'the sink')
    binary = barrage_bin()
    link = create_link(api, args.sink_port)
    results, code = run_barrage(out, config_yaml(args, f'{api}/{link["slug"]}', db_url, link['slug'], chaos_upstream=upstream), binary)
    clicks = reconcile(api, link['claim_token'], results)
    return summarize(args, out, results, code, clicks, recorder_events(out / 'api.log'))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    modes = parser.add_subparsers(dest='mode', required=True)
    for name, help_text, duration in (
        ('run', 'HTTP load on a short link plus a read-only Postgres probe, against a running API', '20s'),
        ('chaos', 'the same through a fresh API whose Postgres is slowed, then cut off, via Toxiproxy', '35s'),
    ):
        mode = modes.add_parser(name, help=help_text)
        mode.add_argument('--output', type=Path, required=True)
        mode.add_argument('--rate', type=int, default=500, help='redirects per second')
        mode.add_argument('--duration', default=duration)
        mode.add_argument('--ramp', default='2s')
        mode.add_argument('--concurrency', type=int, default=100)
        mode.add_argument('--db-rate', type=int, default=10, help='Postgres probe queries per second')
        mode.add_argument('--sink-port', type=int, default=9999)
        mode.add_argument('--database-url', help='default: DATABASE_URL from server/.env')
    modes.choices['run'].add_argument('--api', default='http://localhost:8080')
    chaos = modes.choices['chaos']
    chaos.add_argument('--port', type=int, default=18080, help='port for the API behind the proxy')
    chaos.add_argument('--slow-at', default='8s')
    chaos.add_argument('--slow-for', default='8s')
    chaos.add_argument('--slow-ms', type=int, default=500)
    chaos.add_argument('--down-at', default='22s')
    chaos.add_argument('--down-for', default='6s')
    args = parser.parse_args()

    busy = [p for p in [args.sink_port] + ([args.port, int(PROXY_LISTEN.split(':')[1])] if args.mode == 'chaos' else []) if not port_free(p)]
    if busy:
        fail(f'ports already in use: {busy}')
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=True)
    env = read_env(SERVER / '.env')
    procs = Processes()
    try:
        code = (run_mode if args.mode == 'run' else chaos_mode)(args, out, procs, env)
    finally:
        procs.stop()
    sys.exit(code)


if __name__ == '__main__':
    main()
