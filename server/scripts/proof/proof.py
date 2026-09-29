#!/usr/bin/env python3
"""Article-scale proof: API instances, one PostgreSQL, one Redis and k6 generators on real machines.

Hosts come from an inventory (inventory.example.json). A host whose "ssh" is "local" runs on this
machine; local.json puts every role on one laptop for a rehearsal. Everything created is named
sh-proof-* and removed at the end unless --keep. Results: results.json and results.md in --output.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import re
import secrets
import shlex
import subprocess
import sys
import threading
import time
import uuid
from pathlib import Path

HERE = Path(__file__).resolve().parent
SERVER = HERE.parent.parent
PREFIX = 'sh-proof'
SSH = ['ssh', '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=accept-new', '-o', 'ControlMaster=auto',
       '-o', 'ControlPath=~/.ssh/sh-proof-%C', '-o', 'ControlPersist=15m', '-o', 'ServerAliveInterval=15']
DEFAULTS = {
    'ssh_user': 'root', 'links': 1_000_000, 'sessions': 1000, 'max_vus': 3000,
    'ports': {'db': 15432, 'redis': 16379, 'api': 18080, 'metrics': 19090},
    'postgres_image': 'postgres:17-alpine', 'redis_image': 'redis:7.2-alpine', 'k6_image': 'grafana/k6:2.3.0',
    'postgres_args': [], 'redis_maxmemory': '1gb', 'api_env': {}, 'limits': {},
}
KEEP_METRICS = ('click_', 'clicks_', 'link_cache_', 'http_requests_total', 'process_cpu_seconds_total',
                'process_resident_memory_bytes', 'go_goroutines', 'go_sql_')
# Proposal targets from docs/010; the article itself sets no latency numbers.
GOALS = {'delivered': 0.999, 'unsent': 0.001, 'redirect_p99_ms': 50, 'report_p95_ms': 500,
         'fault_availability': 0.99, 'generator_cpu': 0.85}
SEED_CHUNK = 1_000_000


def utcnow():
    return datetime.now(timezone.utc).isoformat()


class Host:
    def __init__(self, name, spec, user):
        self.name = name
        self.local = spec['ssh'] == 'local'
        self.target = spec['ssh'] if self.local or '@' in spec['ssh'] else f"{user}@{spec['ssh']}"
        self.address = spec.get('private') or ('127.0.0.1' if self.local else spec['ssh'].split('@')[-1])
        self.roles = set(spec['roles'])
        self.api_instances = int(spec.get('api_instances', 1)) if 'api' in self.roles else 0
        self.facts = {}

    def argv(self, command):
        return ['bash', '-c', command] if self.local else SSH + [self.target, command]

    def call(self, command, input=None, timeout=600):
        return subprocess.run(self.argv(command), input=input, text=True, capture_output=True, timeout=timeout)

    def run(self, command, input=None, timeout=600):
        p = self.call(command, input=input, timeout=timeout)
        if p.returncode:
            raise RuntimeError(f'{self.name}: `{command[:200]}` failed: {p.stderr.strip()[-1500:]}')
        return p.stdout

    def ok(self, command, timeout=60):
        try:
            return self.call(command, timeout=timeout).returncode == 0
        except subprocess.TimeoutExpired:
            return False

    def docker(self, *args, **kwargs):
        return self.run(shlex.join(['docker', *args]), **kwargs)

    def put(self, path, content, mode='600'):
        q = shlex.quote(path)
        self.run(f'umask 077; cat > {q} && chmod {mode} {q}', input=content)


def load_inventory(path, overrides):
    raw = json.loads(Path(path).read_text())
    inv = {**DEFAULTS, **raw, 'ports': {**DEFAULTS['ports'], **raw.get('ports', {})}}
    inv['api_env'] = {**inv['api_env'], **overrides}
    hosts = [Host(name, spec, inv['ssh_user']) for name, spec in inv['hosts'].items()]
    by_role = {role: [h for h in hosts if role in h.roles] for role in ['db', 'redis', 'api', 'gen']}
    problems = [f'exactly one host needs the {role} role' for role in ['db', 'redis'] if len(by_role[role]) != 1]
    problems += [f'at least one host needs the {role} role' for role in ['api', 'gen'] if not by_role[role]]
    unknown = {r for h in hosts for r in h.roles} - set(by_role)
    if unknown:
        problems.append(f'unknown roles {sorted(unknown)}')
    if problems:
        raise SystemExit('inventory: ' + '; '.join(problems))
    return inv, hosts, by_role


def parse_metrics(text):
    values = {}
    for line in text.splitlines():
        if not line or line[0] == '#' or not line.startswith(KEEP_METRICS):
            continue
        key, _, value = line.rpartition(' ')
        try:
            values[key] = float(value)
        except ValueError:
            pass
    return values


def cpu(value):
    try:
        return float(str(value).rstrip('%'))
    except ValueError:
        return 0.0


class Proof:
    def __init__(self, args, inv, hosts, by_role, stages):
        self.args, self.inv, self.hosts, self.stages = args, inv, hosts, stages
        self.db, self.redis = by_role['db'][0], by_role['redis'][0]
        self.gens = by_role['gen']
        self.run_id = datetime.now(timezone.utc).strftime('%Y%m%d%H%M%S') + '-' + uuid.uuid4().hex[:6]
        self.image = f'{PREFIX}-api:{self.run_id}'
        self.workdir = f'/tmp/{PREFIX}-{self.run_id}'
        self.out = args.output.resolve()
        self.secrets = {'db': secrets.token_hex(16), 'redis': secrets.token_hex(16),
                        'limen': secrets.token_hex(16), 'admin': secrets.token_hex(32)}
        ports = inv['ports']
        self.apis = []
        for host in by_role['api']:
            for _ in range(host.api_instances):
                i = len(self.apis)
                port, metrics_port = ports['api'] + i, ports['metrics'] + i
                self.apis.append({'index': i, 'host': host, 'name': f'{PREFIX}-api-{i}', 'port': port,
                                  'addr': f'{host.address}:{port}', 'url': f'http://{host.address}:{port}',
                                  'metrics': f'{host.address}:{metrics_port}'})
        self.current = {'stage': 'setup'}
        self.stop = threading.Event()
        self.samples = []
        self.lock = threading.Lock()
        self.seed_max_id = 0
        self.touched = False
        self.results = {'run': self.run_id, 'started_at': utcnow(), 'command': sys.argv, 'goals': GOALS,
                        'stages': [], 'notes': []}

    # ---- plumbing -----------------------------------------------------------------------------

    def limits(self, role):
        limit = self.inv['limits'].get(role, {})
        args = []
        if 'cpus' in limit:
            args += ['--cpus', str(limit['cpus'])]
        if 'memory' in limit:
            args += ['--memory', str(limit['memory'])]
        return args

    def sql(self, statement, timeout=3600):
        port = self.inv['ports']['db']
        return self.db.run(f'docker exec -i {PREFIX}-db psql -p {port} -U postgres -d proof -v ON_ERROR_STOP=1 -qtA',
                           input=statement, timeout=timeout).strip()

    def counter_total(self):
        return int(self.sql('SELECT coalesce(sum(human_clicks), 0) FROM link_counters;') or 0)

    def scrape(self, api):
        p = api['host'].call(f"docker exec {api['name']} wget -qO- -T 5 http://{api['metrics']}/metrics", timeout=30)
        return parse_metrics(p.stdout) if p.returncode == 0 else {}

    def wait_ready(self, role, name=None, limit=180):
        deadline = time.monotonic() + limit
        ports = self.inv['ports']
        if role == 'db':
            host, probe = self.db, f'docker exec {PREFIX}-db pg_isready -h {self.db.address} -p {ports["db"]} -U postgres'
        elif role == 'redis':
            host = self.redis
            probe = (f'docker exec {PREFIX}-redis redis-cli -h {self.redis.address} -p {ports["redis"]} '
                     f'-a {self.secrets["redis"]} --no-auth-warning ping | grep -q PONG')
        else:
            api = next(a for a in self.apis if a['name'] == name)
            host, probe = api['host'], f"docker exec {api['name']} wget -qO- -T 3 http://{api['addr']}/health/ready"
        while time.monotonic() < deadline:
            if host.ok(probe, timeout=30):
                return
            time.sleep(1)
        raise RuntimeError(f'{name or role} was not ready after {limit}s')

    def log(self, message):
        print(f'[{datetime.now().strftime("%H:%M:%S")}] {message}', flush=True)

    # ---- setup --------------------------------------------------------------------------------

    def preflight(self):
        local_arch = subprocess.run(['docker', 'version', '--format', '{{.Server.Arch}}'], capture_output=True,
                                    text=True, check=True).stdout.strip()
        for host in self.hosts:
            facts = host.run("docker version --format '{{.Server.Version}} {{.Server.Arch}}'; nproc; "
                             "awk '/MemTotal/ {print $2}' /proc/meminfo; uname -r; "
                             f"docker ps -aq --filter name={PREFIX}- | wc -l").split()
            host.facts = {'docker': facts[0], 'arch': facts[1], 'cpus': int(facts[2]),
                          'memory_gib': round(int(facts[3]) / 1024 / 1024, 1), 'kernel': facts[4]}
            if int(facts[5]):
                raise SystemExit(f'{host.name} already has {PREFIX}-* containers; run with --teardown first')
            if 'api' in host.roles and host.facts['arch'] != local_arch:
                raise SystemExit(f'{host.name} is {host.facts["arch"]} but this machine builds {local_arch} images')
            self.log(f'{host.name}: {sorted(host.roles)}, {host.facts["cpus"]} CPUs, {host.facts["memory_gib"]} GiB')

    def build(self):
        snapshot = self.out / 'source'
        sources = [SERVER / 'Dockerfile', SERVER / 'go.mod', SERVER / 'go.sum']
        sources += sorted((SERVER / 'cmd').rglob('*.go')) + sorted((SERVER / 'internal').rglob('*.go'))
        sources += sorted((SERVER / 'db').rglob('*.sql'))
        hashes = {}
        for src in sources:
            rel = src.relative_to(SERVER)
            (snapshot / rel).parent.mkdir(parents=True, exist_ok=True)
            data = src.read_bytes()
            (snapshot / rel).write_bytes(data)
            hashes[str(rel)] = hashlib.sha256(data).hexdigest()
        self.results['source_sha256'] = hashes
        self.results['commit'] = subprocess.run(['git', 'rev-parse', 'HEAD'], cwd=SERVER, capture_output=True,
                                                text=True).stdout.strip()
        self.results['git_status'] = subprocess.run(['git', 'status', '--short'], cwd=SERVER, capture_output=True,
                                                    text=True).stdout
        self.log(f'building {self.image}')
        with (self.out / 'build.log').open('w') as f:
            subprocess.run(['docker', 'build', '-t', self.image, str(snapshot)], stdout=f, stderr=subprocess.STDOUT,
                           check=True)
        for host in {a['host'] for a in self.apis if not a['host'].local}:
            self.log(f'shipping the image to {host.name}')
            ssh = shlex.join(SSH + [host.target, 'gunzip | docker load'])
            subprocess.run(f'docker save {shlex.quote(self.image)} | gzip -1 | {ssh}', shell=True, check=True,
                           capture_output=True)

    def prepare_hosts(self):
        script = (HERE / 'proof.js').read_text()
        for host in self.hosts:
            host.run(f'mkdir -p {self.workdir}/env {self.workdir}/k6 && chmod 700 {self.workdir}/env '
                     f'&& chmod 755 {self.workdir} {self.workdir}/k6')
            if 'gen' in host.roles:
                host.put(f'{self.workdir}/k6/proof.js', script, mode='644')

    def start_data(self):
        ports, inv = self.inv['ports'], self.inv
        self.db.put(f'{self.workdir}/env/db.env', f'POSTGRES_PASSWORD={self.secrets["db"]}\nPOSTGRES_DB=proof\n')
        self.db.docker('volume', 'create', f'{PREFIX}-pgdata')
        self.db.docker('run', '-d', '--name', f'{PREFIX}-db', '--network', 'host', '--shm-size', '1g',
                       '-v', f'{PREFIX}-pgdata:/var/lib/postgresql/data', '--env-file', f'{self.workdir}/env/db.env',
                       *self.limits('db'), inv['postgres_image'],
                       '-c', f'listen_addresses={self.db.address}', '-c', f'port={ports["db"]}', *inv['postgres_args'])
        self.redis.docker('run', '-d', '--name', f'{PREFIX}-redis', '--network', 'host', *self.limits('redis'),
                          inv['redis_image'], 'redis-server', '--bind', self.redis.address, '--port', str(ports['redis']),
                          '--requirepass', self.secrets['redis'], '--maxmemory', inv['redis_maxmemory'],
                          '--maxmemory-policy', 'volatile-lru', '--appendonly', 'yes', '--appendfsync', 'everysec',
                          '--save', '')
        self.wait_ready('db')
        self.wait_ready('redis')
        self.log('PostgreSQL and Redis are up')

    def migrate_and_seed(self):
        for migration in sorted((self.out / 'source/db/migrations').glob('*.sql')):
            self.sql(migration.read_text().split('-- +goose Down')[0])
        links, sessions = self.inv['links'], self.inv['sessions']
        began = time.monotonic()
        self.sql(f"INSERT INTO users (id, email, email_verified_at, updated_at) "
                 f"SELECT i, 'proof' || i || '@example.com', now(), now() FROM generate_series(1, {sessions}) i;"
                 f"SELECT setval(pg_get_serial_sequence('users', 'id'), {sessions});"
                 f"INSERT INTO sessions (token, user_id, created_at, expires_at, last_access) "
                 f"SELECT 'proof-session-' || i, i, now(), now() + interval '3 days', now() FROM generate_series(1, {sessions}) i;")
        for start in range(1, links + 1, SEED_CHUNK):
            end = min(links, start + SEED_CHUNK - 1)
            self.sql(f"INSERT INTO links (user_id, slug, target_url) SELECT ((i - 1) % {sessions}) + 1, 'p-' || i, "
                     f"'https://example.com/p/' || i FROM generate_series({start}, {end}) i;")
            self.log(f'seeded {end:,}/{links:,} links')
        self.sql("INSERT INTO links (user_id, slug, target_url) VALUES (1, 'proof-takedown', 'https://example.com/takedown');")
        misplaced = int(self.sql(f"SELECT count(*) FROM links WHERE id <= {links} AND slug <> 'p-' || id;"))
        if misplaced:
            raise RuntimeError(f'{misplaced} seeded links have an ID that does not match their slug')
        self.seed_max_id = int(self.sql('SELECT max(id) FROM links;'))
        self.sql('VACUUM ANALYZE;')
        self.results['seed'] = {'links': links, 'sessions': sessions, 'seconds': round(time.monotonic() - began, 1),
                                'database_bytes': int(self.sql('SELECT pg_database_size(current_database());'))}
        self.log(f'seeded {links:,} links and {sessions:,} sessions in {self.results["seed"]["seconds"]}s')

    def api_env(self, api):
        ports = self.inv['ports']
        max_connections = 100
        for arg in self.inv['postgres_args']:
            m = re.fullmatch(r'max_connections=(\d+)', arg)
            if m:
                max_connections = int(m.group(1))
        pool = max(10, min(200, (max_connections - 20) // len(self.apis)))
        env = {
            'APP_ENV': 'development', 'PORT': str(api['port']), 'BASE_URL': api['url'],
            'DATABASE_URL': f'postgres://postgres:{self.secrets["db"]}@{self.db.address}:{ports["db"]}/proof?sslmode=disable',
            'REDIS_URL': f'redis://:{self.secrets["redis"]}@{self.redis.address}:{ports["redis"]}/0',
            'LIMEN_SECRET': self.secrets['limen'], 'EMAIL_PROVIDER': 'resend', 'ADMIN_TOKEN': self.secrets['admin'],
            'METRICS_ADDR': api['metrics'], 'REQUEST_LOG': 'errors', 'LINK_CACHE_SIZE': '1000000',
            'DB_MAX_OPEN_CONNS': str(pool), 'DB_MAX_IDLE_CONNS': str(pool),
        }
        env.update({k: str(v) for k, v in self.inv['api_env'].items()})
        return env

    def start_apis(self):
        for api in self.apis:
            env = self.api_env(api)
            path = f'{self.workdir}/env/{api["name"]}.env'
            api['host'].put(path, ''.join(f'{k}={v}\n' for k, v in env.items()))
            api['host'].docker('run', '-d', '--name', api['name'], '--network', 'host',
                               '--ulimit', 'nofile=1048576:1048576', '--env-file', path, *self.limits('api'), self.image)
        for api in self.apis:
            self.wait_ready('api', api['name'])
        public = {k: v for k, v in self.api_env(self.apis[0]).items()
                  if k not in ('DATABASE_URL', 'REDIS_URL', 'LIMEN_SECRET', 'ADMIN_TOKEN', 'PORT', 'BASE_URL', 'METRICS_ADDR')}
        self.results['api_env'] = public
        self.log(f'{len(self.apis)} API instances ready: ' + ', '.join(a['url'] for a in self.apis))

    # ---- sampling -----------------------------------------------------------------------------

    def sampler(self, host):
        apis = [a for a in self.apis if a['host'] is host]
        script = f"docker stats --no-stream --format '{{{{json .}}}}' $(docker ps -q --filter name={PREFIX}-) 2>/dev/null"
        for api in apis:
            script += (f"; echo '### {api['name']}'; docker exec {api['name']} wget -qO- -T 5 "
                       f"http://{api['metrics']}/metrics 2>/dev/null")
        with (self.out / 'samples' / f'{host.name}.jsonl').open('w') as f:
            while not self.stop.is_set():
                try:
                    text = host.call(script, timeout=60).stdout
                except subprocess.TimeoutExpired:
                    text = ''
                sample = {'at': utcnow(), 'mono': time.monotonic(), 'stage': self.current['stage'], 'host': host.name,
                          'containers': {}, 'metrics': {}}
                section = None
                buffer = []
                for line in text.splitlines() + ['### end']:
                    if line.startswith('### '):
                        if section:
                            sample['metrics'][section] = parse_metrics('\n'.join(buffer))
                        section, buffer = line[4:].strip(), []
                    elif section:
                        buffer.append(line)
                    elif line.startswith('{'):
                        row = json.loads(line)
                        if row.get('Name', '').startswith(PREFIX):
                            sample['containers'][row['Name']] = {'cpu': cpu(row.get('CPUPerc')),
                                                                 'memory': row.get('MemUsage', '').split('/')[0].strip()}
                sample['metrics'].pop('end', None)
                with self.lock:
                    self.samples.append(sample)
                f.write(json.dumps(sample) + '\n')
                f.flush()
                self.stop.wait(5)

    def resources(self, name, began, ended):
        rows = {}
        with self.lock:
            window = [s for s in self.samples if began <= s['mono'] <= ended]
        for s in window:
            for container, v in s['containers'].items():
                rows.setdefault(container, {'host': s['host'], 'cpu': []})['cpu'].append(v['cpu'])
        return {c: {'host': v['host'], 'avg_cores': round(sum(v['cpu']) / len(v['cpu']) / 100, 2),
                    'max_cores': round(max(v['cpu']) / 100, 2), 'samples': len(v['cpu'])} for c, v in rows.items()}

    # ---- stages -------------------------------------------------------------------------------

    def fault(self, fault, started, events):
        time.sleep(max(0.0, started + fault['at'] - time.monotonic()))
        role, action = fault['role'], fault['action']
        if role == 'api':
            api = self.apis[fault.get('index', 0)]
            host, name = api['host'], api['name']
        else:
            host, name = (self.db, f'{PREFIX}-db') if role == 'db' else (self.redis, f'{PREFIX}-redis')

        def event(what):
            events.append({'second': round(time.monotonic() - started, 1), 'event': f'{name} {what}'})
            self.log(f'fault: {name} {what}')
        try:
            if action == 'restart':
                event('restarting')
                host.docker('restart', '-t', '10', name)
            else:
                host.docker(*(['kill', name] if action == 'kill' else ['stop', '-t', '5', name]))
                event('killed' if action == 'kill' else 'stopped')
                time.sleep(fault.get('for', 30))
                host.docker('start', name)
                event('started')
            self.wait_ready(role, name)
            event('ready')
        except Exception as error:
            event(f'fault injection failed: {error}')

    def drain(self, limit=300):
        began, last, stable = time.monotonic(), None, 0
        while time.monotonic() - began < limit:
            pending = 0.0
            for api in self.apis:
                m = self.scrape(api)
                pending += m.get('click_queue_depth', 0) + m.get('click_stream_backlog_entries', 0)
            total = self.counter_total()
            stable = stable + 1 if pending == 0 and total == last else 0
            if stable >= 2:
                break
            last = total
            time.sleep(2)
        return {'seconds': round(time.monotonic() - began, 1), 'drained': stable >= 2}

    def metric_deltas(self, before, after, seconds):
        totals = {}
        statuses = {}
        for name, end in after.items():
            start = before.get(name, {})
            for key, value in end.items():
                # A restarted instance starts its counters again from zero.
                delta = value - start.get(key, 0) if value >= start.get(key, 0) else value
                if key.startswith('http_requests_total'):
                    status = re.search(r'status="(\d+)"', key)
                    route = re.search(r'route="([^"]*)"', key)
                    label = f'{route.group(1) if route else "?"} {status.group(1) if status else "?"}'
                    statuses[label] = statuses.get(label, 0) + delta
                elif key.endswith('_total') and '{' not in key:
                    totals[key] = totals.get(key, 0) + delta
        cpu_seconds = totals.pop('process_cpu_seconds_total', 0)
        return {'api_cores': round(cpu_seconds / seconds, 2) if seconds else 0,
                'counters': {k: round(v) for k, v in sorted(totals.items())},
                'responses': {k: round(v) for k, v in sorted(statuses.items())}}

    def k6(self, stage, gen):
        env = {
            'RUN_ID': stage['name'], 'GENERATOR': gen.name, 'TARGETS': ','.join(a['url'] for a in self.apis),
            'LINKS': self.inv['links'], 'SESSIONS': self.inv['sessions'], 'SHARE': 1 / len(self.gens),
            'DURATION': stage['seconds'], 'REDIRECT_RATE': stage['redirects'], 'CREATE_RATE': stage['creates'],
            'REPORT_RATE': stage['reports'], 'REPORT_SHAPE': stage.get('report_shape', 'uniform'),
            'WINDOW': stage.get('window', 0), 'MAX_VUS': self.inv['max_vus'],
        }
        command = ['docker', 'run', '--rm', '--name', f'{PREFIX}-k6-{stage["name"]}', '--network', 'host',
                   '--ulimit', 'nofile=1048576:1048576', '-v', f'{self.workdir}/k6:/scripts:ro']
        for key, value in env.items():
            command += ['-e', f'{key}={value}']
        return command + [self.inv['k6_image'], 'run', '--quiet', '/scripts/proof.js']

    def run_stage(self, stage):
        name = stage['name']
        self.current['stage'] = name
        self.log(f'stage {name}: {stage["redirects"]} redirects, {stage["creates"]} creates, '
                 f'{stage["reports"]} reports per second for {stage["seconds"]}s')
        before = {a['name']: self.scrape(a) for a in self.apis}
        counter_before = self.counter_total()
        (self.out / 'k6').mkdir(exist_ok=True)
        procs = []
        for gen in self.gens:
            stdout = (self.out / 'k6' / f'{name}-{gen.name}.json').open('w')
            stderr = (self.out / 'k6' / f'{name}-{gen.name}.log').open('w')
            proc = subprocess.Popen(gen.argv(shlex.join(self.k6(stage, gen))), stdout=stdout, stderr=stderr, text=True)
            procs.append((gen, proc, stdout, stderr))
        started = time.monotonic()
        events = []
        faults = [threading.Thread(target=self.fault, args=(f, started, events)) for f in stage.get('faults', [])]
        for t in faults:
            t.start()
        codes = {}
        for gen, proc, stdout, stderr in procs:
            try:
                codes[gen.name] = proc.wait(timeout=stage['seconds'] + 180)
            except subprocess.TimeoutExpired:
                gen.ok(f'docker stop -t 10 {PREFIX}-k6-{name}')
                codes[gen.name] = proc.wait(timeout=60)
            stdout.close()
            stderr.close()
        ended = time.monotonic()
        for t in faults:
            t.join()
        drain = self.drain()
        after = {a['name']: self.scrape(a) for a in self.apis}
        counter_after = self.counter_total()

        summaries = []
        for gen in self.gens:
            lines = [l for l in (self.out / 'k6' / f'{name}-{gen.name}.json').read_text().splitlines() if l.startswith('{')]
            if not lines:
                raise RuntimeError(f'k6 on {gen.name} produced no summary; see k6/{name}-{gen.name}.log')
            summaries.append(json.loads(lines[-1]))
        result = self.combine(stage, summaries)
        result.update({
            'stage': stage, 'k6_exit_codes': codes, 'fault_events': events, 'drain': drain,
            'counter_delta': counter_after - counter_before,
            'api': self.metric_deltas(before, after, ended - started),
            'resources': self.resources(name, started, ended),
        })
        result['generator_bound'] = self.generator_bound(result['resources'])
        self.results['stages'].append(result)
        self.save()
        self.log(f'stage {name}: {result["redirects_per_second"]:.0f} redirects/s, {result["creates_per_second"]:.0f} '
                 f'creates/s, {result["reports_per_second"]:.1f} reports/s, {result["incorrect_responses"]} incorrect, '
                 f'{result["dropped_iterations"]} unsent')
        time.sleep(3)

    def combine(self, stage, summaries):
        def values(s, name):
            return s['metrics'].get(name, {}).get('values', {})

        def total(name):
            return sum(values(s, name).get('count', 0) for s in summaries)

        seconds = max(s['state']['testRunDurationMs'] for s in summaries) / 1000
        r = {'measured_seconds': round(seconds, 1), 'generators': [s['generator'] for s in summaries]}
        for key in ['successful_redirects', 'human_redirects', 'bot_redirects', 'successful_creates',
                    'successful_reports', 'incorrect_responses', 'failovers', 'dropped_iterations', 'http_reqs']:
            r[key] = total(key)
        r['dropped_by_scenario'] = {n: total(f'dropped_iterations{{scenario:{n}}}') for n in ['redirect', 'create', 'report']}
        for kind, rate in [('redirects', 'successful_redirects'), ('creates', 'successful_creates'),
                           ('reports', 'successful_reports')]:
            r[f'{kind}_per_second'] = r[rate] / seconds if seconds else 0
        # Percentiles can't be merged across generators; the worst generator's value is reported.
        for kind in ['redirect', 'create', 'report']:
            r[f'{kind}_latency_ms'] = {stat: round(max(values(s, f'{kind}_latency').get(stat, 0) for s in summaries), 2)
                                       for stat in ['med', 'p(95)', 'p(99)', 'max']}
        passes = sum(values(s, 'new_link_visible_elsewhere').get('passes', 0) for s in summaries)
        fails = sum(values(s, 'new_link_visible_elsewhere').get('fails', 0) for s in summaries)
        r['visibility_checks'] = {'visible': passes, 'not_visible': fails}
        window = stage.get('window', 0)
        if window:
            rows = []
            for w in range(int((stage['seconds'] + 15) // window) + 1):
                ok, bad, moved = (total(f'{m}{{win:{w}}}') for m in ['successful_redirects', 'incorrect_responses', 'failovers'])
                p99 = max(values(s, f'redirect_latency{{win:{w}}}').get('p(99)', 0) for s in summaries)
                if ok or bad or moved:
                    rows.append({'from_second': w * window, 'redirects_per_second': round(ok / window),
                                 'incorrect': bad, 'failovers': moved, 'redirect_p99_ms': round(p99, 1)})
            r['windows'] = rows
        return r

    def generator_bound(self, resources):
        bound = []
        for gen in self.gens:
            k6 = [v for c, v in resources.items() if c.startswith(f'{PREFIX}-k6-') and v['host'] == gen.name]
            if k6 and k6[0]['avg_cores'] > GOALS['generator_cpu'] * gen.facts.get('cpus', 1):
                bound.append(gen.name)
        return bound

    def takedown_probe(self):
        slug, token = 'proof-takedown', self.secrets['admin']
        for api in self.apis:
            if not api['host'].ok(f"docker exec {api['name']} wget -qO /dev/null -T 3 http://{api['addr']}/{slug}+"):
                return {'error': f'{api["name"]} did not serve the link before the takedown'}
        first = self.apis[0]
        body = json.dumps({'slugs': [slug]})
        began = time.monotonic()
        first['host'].run(f"docker exec {first['name']} wget -qO- -T 5 --header 'Authorization: Bearer {token}' "
                          f"--header 'Content-Type: application/json' --post-data {shlex.quote(body)} "
                          f"http://{first['addr']}/api/v1/admin/takedown")
        answered = time.monotonic() - began
        gone = {}

        def watch(api):
            while time.monotonic() - began < 120:
                if not api['host'].ok(f"docker exec {api['name']} wget -qO /dev/null -T 3 http://{api['addr']}/{slug}+"):
                    gone[api['name']] = round(time.monotonic() - began, 2)
                    return
                time.sleep(0.05)
        watchers = [threading.Thread(target=watch, args=(a,)) for a in self.apis]
        for t in watchers:
            t.start()
        for t in watchers:
            t.join()
        result = {'admin_request_seconds': round(answered, 2), 'gone_after_seconds': gone,
                  'all_gone': len(gone) == len(self.apis),
                  'note': 'Each check is a docker exec over SSH, so times are upper bounds with ~0.1-0.3 s resolution.'}
        self.log(f'takedown: gone on {len(gone)}/{len(self.apis)} instances, slowest {max(gone.values(), default=None)}s')
        return result

    # ---- results ------------------------------------------------------------------------------

    def reconcile(self):
        names = ','.join(f"'{s['name']}'" for s in self.stages)
        clicks = json.loads(self.sql(
            "SET statement_timeout = '60min';"
            "SELECT coalesce(json_object_agg(stage, json_build_object('humans', humans, 'bots', bots, 'rows', rows, "
            "'events', events)), '{}') FROM (SELECT utm_campaign AS stage, count(*) FILTER (WHERE NOT is_bot) AS humans, "
            "count(*) FILTER (WHERE is_bot) AS bots, count(*) AS rows, count(DISTINCT event_id) AS events "
            f"FROM clicks WHERE utm_campaign IN ({names}) GROUP BY 1) r;").splitlines()[-1])
        created = json.loads(self.sql(
            "SELECT coalesce(json_object_agg(stage, json_build_object('rows', rows, 'slugs', slugs)), '{}') FROM "
            "(SELECT split_part(target_url, '/', 5) AS stage, count(*) AS rows, count(DISTINCT slug) AS slugs "
            f"FROM links WHERE id > {self.seed_max_id} GROUP BY 1) r;").splitlines()[-1])
        for result in self.results['stages']:
            name = result['stage']['name']
            stored = clicks.get(name, {'humans': 0, 'bots': 0, 'rows': 0, 'events': 0})
            made = created.get(name, {'rows': 0, 'slugs': 0})
            result['clicks'] = {'acknowledged_humans': result['human_redirects'], 'acknowledged_bots': result['bot_redirects'],
                                **stored, 'lost': result['successful_redirects'] - stored['rows'],
                                'duplicated': stored['rows'] - stored['events']}
            result['created_links'] = {'acknowledged': result['successful_creates'], **made}
            self.judge(result)
        self.results['database'] = json.loads(self.sql(
            "SELECT json_build_object('bytes', pg_database_size(current_database()), 'links', (SELECT count(*) FROM links), "
            "'clicks', (SELECT count(*) FROM clicks));"))

    def judge(self, r):
        stage = r['stage']
        checks = {}
        for kind, key in [('redirects', 'successful_redirects'), ('creates', 'successful_creates'),
                          ('reports', 'successful_reports')]:
            if stage[kind]:
                checks[f'{kind} delivered >= {GOALS["delivered"]:.1%}'] = r[key] >= GOALS['delivered'] * stage[kind] * stage['seconds']
        scheduled = sum(stage[k] for k in ['redirects', 'creates', 'reports']) * stage['seconds']
        checks[f'unsent <= {GOALS["unsent"]:.1%}'] = r['dropped_iterations'] <= GOALS['unsent'] * scheduled
        if stage.get('faults'):
            answered = r['successful_redirects'] + r['incorrect_responses']
            checks[f'redirect availability >= {GOALS["fault_availability"]:.0%}'] = (
                answered == 0 or r['successful_redirects'] / answered >= GOALS['fault_availability'])
        else:
            checks['no incorrect responses'] = r['incorrect_responses'] == 0
            checks['every acknowledged click stored once'] = r['clicks']['lost'] == 0 and r['clicks']['duplicated'] == 0
            if stage['redirects']:
                checks[f'redirect p99 < {GOALS["redirect_p99_ms"]} ms'] = r['redirect_latency_ms']['p(99)'] < GOALS['redirect_p99_ms']
            if stage['reports']:
                checks[f'report p95 < {GOALS["report_p95_ms"]} ms'] = r['report_latency_ms']['p(95)'] < GOALS['report_p95_ms']
        if stage['creates']:
            checks['created links visible on another instance'] = r['visibility_checks']['not_visible'] == 0
            checks['every acknowledged create stored'] = r['created_links']['slugs'] >= r['created_links']['acknowledged']
        r['checks'] = checks
        r['passed'] = all(checks.values()) and not r['generator_bound']

    def save(self):
        (self.out / 'results.json').write_text(json.dumps(self.results, indent=2, default=str) + '\n')

    def markdown(self):
        res = self.results
        lines = [f'# Proof run {res["run"]}', '',
                 f'Started {res["started_at"]}, commit `{res.get("commit", "?")[:12]}`'
                 + (' with uncommitted changes' if res.get('git_status') else '') + '.', '',
                 '## Topology', '', '| Host | Roles | CPUs | Memory |', '|---|---|---:|---:|']
        for h in res['hosts']:
            lines.append(f'| {h["name"]} | {", ".join(h["roles"])} | {h["cpus"]} | {h["memory_gib"]} GiB |')
        lines += ['', f'{len(self.apis)} API instances; API settings: '
                  + ', '.join(f'`{k}={v}`' for k, v in sorted(res.get('api_env', {}).items())) + '.', '']
        if 'seed' in res:
            s = res['seed']
            lines += [f'Seeded {s["links"]:,} links and {s["sessions"]:,} signed-in sessions in {s["seconds"]} s.', '']
        lines += ['## Stages', '',
                  '| Stage | Offered r/c/s per sec | Delivered r/c/s per sec | Unsent | Incorrect | Failovers | '
                  'Redirect p50 / p99 | Create p99 | Report p95 | Clicks lost / duplicated | API / DB / Redis cores | Result |',
                  '|---|---|---|---:|---:|---:|---|---:|---:|---|---|---|']
        for r in res['stages']:
            st = r['stage']
            cores = {}
            for c, v in r['resources'].items():
                kind = 'api' if '-api-' in c else 'db' if c.endswith('-db') else 'redis' if c.endswith('-redis') else None
                if kind:
                    cores[kind] = cores.get(kind, 0) + v['avg_cores']
            clicks = r.get('clicks', {})
            verdict = 'Pass' if r.get('passed') else 'Fail: ' + ', '.join(k for k, ok in r.get('checks', {}).items() if not ok)
            if r['generator_bound']:
                verdict += f' (generator-bound: {", ".join(r["generator_bound"])})'
            lines.append(
                f'| {st["name"]} | {st["redirects"]:,} / {st["creates"]:,} / {st["reports"]:,} | '
                f'{r["redirects_per_second"]:,.0f} / {r["creates_per_second"]:,.0f} / {r["reports_per_second"]:,.1f} | '
                f'{r["dropped_iterations"]:,} | {r["incorrect_responses"]:,} | {r["failovers"]:,} | '
                f'{r["redirect_latency_ms"]["med"]} / {r["redirect_latency_ms"]["p(99)"]} ms | '
                f'{r["create_latency_ms"]["p(99)"]} ms | {r["report_latency_ms"]["p(95)"]} ms | '
                f'{clicks.get("lost", "?")} / {clicks.get("duplicated", "?")} | '
                f'{cores.get("api", 0):.1f} / {cores.get("db", 0):.1f} / {cores.get("redis", 0):.1f} | {verdict} |')
        for r in res['stages']:
            if not r.get('windows'):
                continue
            lines += ['', f'### {r["stage"]["name"]}: timeline', '']
            lines += [f'- {e["second"]} s: {e["event"]}' for e in r['fault_events']]
            lines += ['', '| From second | Redirects/sec | Incorrect | Failovers | Redirect p99 |', '|---:|---:|---:|---:|---:|']
            lines += [f'| {w["from_second"]} | {w["redirects_per_second"]:,} | {w["incorrect"]:,} | {w["failovers"]:,} | '
                      f'{w["redirect_p99_ms"]} ms |' for w in r['windows']]
        if 'takedown' in res:
            t = res['takedown']
            lines += ['', '## Takedown', '']
            if 'error' in t:
                lines.append(f'Not measured: {t["error"]}')
            else:
                lines.append(f'Admin request answered in {t["admin_request_seconds"]} s; the link stopped resolving on '
                             f'{len(t["gone_after_seconds"])}/{len(self.apis)} instances, slowest after '
                             f'{max(t["gone_after_seconds"].values(), default="?")} s. {t["note"]}')
        lines += ['', '## Scope', '',
                  '- Direct HTTP on a private network: no TLS, CDN or load balancer. Generators pick instances at random '
                  'and retry an unreachable one on another, as a load balancer would.',
                  '- Latency percentiles are the worst generator\'s, not merged across generators.',
                  '- A stage is only a capacity result if no generator was CPU-bound.',
                  '- Goals are the proposal targets in docs/010; the article sets throughput, not latency.', '']
        (self.out / 'results.md').write_text('\n'.join(lines))

    # ---- teardown -----------------------------------------------------------------------------

    def collect_logs(self):
        logs = self.out / 'logs'
        logs.mkdir(exist_ok=True)
        for api in self.apis:
            p = api['host'].call(f"docker logs --tail 20000 {api['name']}", timeout=120)
            (logs / f'{api["name"]}.log').write_text(p.stdout + p.stderr)
        for host, name in [(self.db, f'{PREFIX}-db'), (self.redis, f'{PREFIX}-redis')]:
            p = host.call(f'docker logs --tail 5000 {name}', timeout=120)
            (logs / f'{name}.log').write_text(p.stdout + p.stderr)


def teardown(hosts):
    for host in hosts:
        host.ok(f'ids=$(docker ps -aq --filter name={PREFIX}-); [ -z "$ids" ] || docker rm -f $ids', timeout=300)
        host.ok(f'docker volume rm -f {PREFIX}-pgdata', timeout=120)
        host.ok(f'rm -rf /tmp/{PREFIX}-*', timeout=120)
        if not host.local:
            host.ok(f'imgs=$(docker images -q {PREFIX}-api); [ -z "$imgs" ] || docker rmi -f $imgs', timeout=300)
    subprocess.run(f'imgs=$(docker images -q {PREFIX}-api); [ -z "$imgs" ] || docker rmi -f $imgs', shell=True,
                   capture_output=True)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--inventory', type=Path, required=True, help='Hosts and settings, e.g. local.json')
    p.add_argument('--profile', default='rehearsal', help='Stage list in profiles.json')
    p.add_argument('--profile-file', type=Path, default=HERE / 'profiles.json')
    p.add_argument('--output', type=Path, help='Empty directory for results')
    p.add_argument('--links', type=int, help='Override the inventory link count')
    p.add_argument('--click-queue', choices=['redis', 'memory'], help='Override CLICK_QUEUE on the API')
    p.add_argument('--no-takedown', action='store_true', help='Skip the takedown timing probe')
    p.add_argument('--keep', action='store_true', help='Leave everything running afterwards')
    p.add_argument('--teardown', action='store_true', help=f'Only remove {PREFIX}-* containers, volumes and images')
    p.add_argument('--dry-run', action='store_true', help='Print the plan without touching any host')
    args = p.parse_args()

    overrides = {'CLICK_QUEUE': args.click_queue} if args.click_queue else {}
    inv, hosts, by_role = load_inventory(args.inventory, overrides)
    if args.links:
        inv['links'] = args.links
    if args.teardown:
        teardown(hosts)
        print(f'removed {PREFIX}-* from {len(hosts)} hosts')
        return
    stages = json.loads(args.profile_file.read_text()).get(args.profile)
    if not stages:
        p.error(f'no profile {args.profile!r} in {args.profile_file}')
    names = [s['name'] for s in stages]
    if len(set(names)) != len(names) or any(not re.fullmatch(r'[a-z0-9-]+', n) for n in names):
        p.error('stage names must be unique lowercase letters, digits and hyphens')
    apis = sum(h.api_instances for h in by_role['api'])
    if args.dry_run:
        print(f'{len(hosts)} hosts, {apis} API instances, {len(by_role["gen"])} generators, {inv["links"]:,} links')
        for s in stages:
            per_gen = {k: round(s[k] / len(by_role['gen'])) for k in ['redirects', 'creates', 'reports']}
            print(f'  {s["name"]}: {s["seconds"]}s; per generator {per_gen}; faults {s.get("faults", [])}')
        return
    if not args.output:
        p.error('--output is required')
    args.output.mkdir(parents=True, exist_ok=True)
    if any(args.output.iterdir()):
        p.error('--output must be empty; earlier evidence is never overwritten')
    (args.output / 'samples').mkdir()

    proof = Proof(args, inv, hosts, by_role, stages)
    samplers = []
    try:
        proof.preflight()
        proof.touched = True
        proof.results['hosts'] = [{'name': h.name, 'roles': sorted(h.roles), **h.facts} for h in hosts]
        proof.results['profile'] = {'name': args.profile, 'stages': stages}
        proof.results['inventory'] = {k: v for k, v in inv.items() if k != 'hosts'}
        proof.build()
        proof.prepare_hosts()
        proof.start_data()
        proof.migrate_and_seed()
        proof.start_apis()
        samplers = [threading.Thread(target=proof.sampler, args=(h,), daemon=True) for h in hosts]
        for t in samplers:
            t.start()
        for stage in stages:
            proof.run_stage(stage)
            if stage['name'] == 'steady' and not args.no_takedown:
                proof.results['takedown'] = proof.takedown_probe()
        if 'takedown' not in proof.results and not args.no_takedown:
            proof.results['takedown'] = proof.takedown_probe()
        proof.current['stage'] = 'reconcile'
        proof.log('reconciling clicks and created links')
        proof.reconcile()
    finally:
        proof.stop.set()
        for t in samplers:
            t.join(timeout=30)
        proof.results['finished_at'] = utcnow()
        proof.save()
        if proof.results['stages']:
            proof.markdown()
        if proof.touched:
            try:
                proof.collect_logs()
            except Exception as error:
                print(f'could not collect logs: {error}', file=sys.stderr)
            if args.keep:
                print(f'left running; remove with: {sys.argv[0]} --inventory {args.inventory} --teardown')
            else:
                teardown(hosts)
    failed = [r['stage']['name'] for r in proof.results['stages'] if not r.get('passed')]
    print(f'results: {args.output / "results.md"}' + (f'; not passing: {", ".join(failed)}' if failed else '; all stages pass'))
    if failed:
        raise SystemExit(1)


if __name__ == '__main__':
    main()
