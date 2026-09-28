#!/usr/bin/env python3
"""Article workload benchmark: one isolated API + current cache + disposable PostgreSQL.

No development credentials, database or running containers are used. Profiles are local HTTP
experiments, not claims of daily capacity. Redis is explicitly absent until the API integrates it.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
import uuid

sys.dont_write_bytecode = True
from run import HERE, SERVER, run
from article_functional import check_functional


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def summarize(summary, stage):
    metrics = summary['metrics']
    seconds = summary['state']['testRunDurationMs'] / 1000
    def count(name):
        return metrics.get(name, {}).get('values', {}).get('count', 0)
    def values(name):
        return metrics.get(name, {}).get('values', {})
    result = {'stage': stage, 'measured_seconds': seconds,
              'completed_duration': seconds >= stage['seconds'],
              'http_requests': count('http_reqs'),
              'http_error_rate': values('http_req_failed').get('rate'),
              'dropped_iterations': count('dropped_iterations'),
              'dropped_iterations_by_scenario': {name: count('dropped_iterations{scenario:'+name+'}')
                                                  for name in ['redirect', 'create', 'report']
                                                  if 'dropped_iterations{scenario:'+name+'}' in metrics},
              'incorrect_responses': count('incorrect_responses')}
    for kind, metric, latency in [('redirects', 'successful_redirects', 'redirect_latency'),
                                  ('creates', 'successful_creates', 'create_latency'),
                                  ('reports', 'successful_reports', 'report_latency')]:
        result[kind] = count(metric)
        result[kind + '_per_second'] = count(metric) / seconds if seconds else 0
        result[kind + '_latency_ms'] = values(latency)
    result['humans'] = count('human_redirects')
    result['bots'] = count('bot_redirects')
    return result


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--profiles', default='smoke', help='Comma-separated profiles in article-profiles.json')
    p.add_argument('--profile-file', type=Path, default=HERE/'article-profiles.json')
    p.add_argument('--links', type=int, default=1000)
    p.add_argument('--vus', type=int, default=400)
    p.add_argument('--create-vus', type=int, default=100)
    p.add_argument('--stats-vus', type=int, default=50)
    p.add_argument('--integration-tests', action='store_true')
    p.add_argument('--fault-checks', action='store_true', help='Also prove API-crash loss and graceful drain in isolated stack')
    p.add_argument('--skip-functional', action='store_true')
    p.add_argument('--output', type=Path, required=True)
    args = p.parse_args()
    if min(args.links, args.vus, args.create_vus, args.stats_vus) < 1:
        p.error('links and worker counts must be positive')
    profiles = json.loads(args.profile_file.read_text())
    try:
        stages = [dict(s) for name in args.profiles.split(',') for s in profiles[name]]
    except KeyError as e:
        p.error(f'unknown profile {e}')
    names = [s['name'] for s in stages]
    if len(set(names)) != len(names) or any(not re.fullmatch(r'[a-z0-9-]+', n) for n in names):
        p.error('stage names must be unique lowercase alphanumeric/hyphen strings')
    for s in stages:
        if min(s[k] for k in ['redirects', 'creates', 'reports']) < 0 or s['seconds'] <= 0 or not any(s[k] for k in ['redirects', 'creates', 'reports']):
            p.error('stages require nonnegative rates, a positive duration and at least one workload')
        if s.get('db_outage_seconds', 0) and s['seconds'] < 20:
            p.error('outage stage must last at least 20 seconds')
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        p.error('output must be empty; prior evidence is never overwritten')
    project = 'sh-article-' + uuid.uuid4().hex[:10]
    docker = ['docker', '--context', 'default']
    snapshot = out/'source'
    # Build a frozen copy, including uncommitted Claude changes, without .env or benchmark data.
    sources = [SERVER/'Dockerfile', SERVER/'go.mod', SERVER/'go.sum']
    sources += sorted((SERVER/'cmd').rglob('*.go')) + sorted((SERVER/'internal').rglob('*.go'))
    sources += sorted((SERVER/'db').rglob('*.sql'))
    hashes = {}
    for src in sources:
        rel = src.relative_to(SERVER)
        target = snapshot/rel
        target.parent.mkdir(parents=True, exist_ok=True)
        data = src.read_bytes()
        target.write_bytes(data)
        hashes[str(rel)] = hashlib.sha256(data).hexdigest()
    scripts = out/'tested-scripts'
    scripts.mkdir()
    for src in HERE.glob('*'):
        if src.is_file() and src.suffix in ('.py', '.js', '.json', '.yml'):
            shutil.copy2(src, scripts/src.name)
    override = out/'compose.override.json'
    override.write_text(json.dumps({'services': {
        'api': {'build': {'context': str(snapshot)}, 'ports': ['127.0.0.1::8080']},
        'db': {'ports': ['127.0.0.1::5432']},
    }}))
    compose = docker + ['compose', '-p', project, '-f', str(scripts/'compose.yml'), '-f', str(override)]
    stop = threading.Event()
    monitor_thread = None
    current = {'stage': 'setup'}
    base = ''
    results = []
    meta = {'started_at': datetime.now(timezone.utc).isoformat(), 'project': project,
            'commit': run(['git', 'rev-parse', 'HEAD'], cwd=SERVER, capture_output=True).stdout.strip(),
            'git_status': run(['git', 'status', '--short'], cwd=SERVER, capture_output=True).stdout,
            'source_sha256': hashes, 'host': platform.platform(), 'logical_cpus': os.cpu_count(),
            'cpu': run(['lscpu'], capture_output=True).stdout,
            'command': sys.argv, 'stages': stages, 'links': args.links,
            'topology': {'api_instances': 1, 'lru': True, 'redis': 'NOT IMPLEMENTED', 'postgres_instances': 1},
            'scope': 'Local Docker HTTP; no Traefik/TLS, remote network, email or Web Risk. Authenticated creates each followed by one preview read. Same host generator. No 20-year retention target.',
            'thresholds': 'Local redirect median<5ms/p99<50ms and report p95<500ms are proposal targets, not article-specified SLOs. Abort above 1% HTTP errors or 2s redirect p99 after 15s.'}

    def sql(statement):
        return run(compose + ['exec', '-T', 'db', 'psql', '-U', 'postgres', '-d', 'benchmark', '-v', 'ON_ERROR_STOP=1', '-tA'],
                   input=statement, capture_output=True).stdout.strip()

    def api(path, method='GET', body=None, user=1, browser=False, timeout=6):
        headers = {'User-Agent': 'Mozilla/5.0 Firefox/127.0' if browser else 'curl/8.0'}
        if user:
            headers['Cookie'] = f'limen_session=article-session-{user}'
        if body is not None:
            headers['Content-Type'] = 'application/json'
        req = urllib.request.Request(base + path, method=method, headers=headers,
                                     data=json.dumps(body).encode() if body is not None else None)
        try:
            response = urllib.request.build_opener(NoRedirect()).open(req, timeout=timeout)
        except urllib.error.HTTPError as e:
            response = e
        with response:
            raw = response.read().decode()
            try:
                parsed = json.loads(raw)
            except ValueError:
                parsed = raw
            return response.code, parsed, dict(response.headers)

    def wait_ready():
        nonlocal base
        # Docker may assign a new ephemeral host port after a stopped container starts.
        base = 'http://' + run(compose + ['port', 'api', '8080'], capture_output=True).stdout.strip()
        for _ in range(60):
            try:
                if api('/health/ready', timeout=2)[0] == 200:
                    return
            except OSError:
                pass
            time.sleep(.5)
        raise RuntimeError('isolated API did not become ready')

    def monitor():
        with (out/'resources.jsonl').open('w') as f, (out/'database-resources.jsonl').open('w') as db_resources:
            while not stop.is_set():
                sample = subprocess.run(docker + ['stats', '--no-stream', '--format', '{{json .}}'], capture_output=True, text=True)
                for line in sample.stdout.splitlines():
                    row = json.loads(line)
                    if row.get('Name', '').startswith(project):
                        f.write(json.dumps({'at': datetime.now(timezone.utc).isoformat(), **current, **row})+'\n')
                f.flush()
                try:
                    raw = sql("SET statement_timeout='2s'; SELECT json_build_object("
                              "'clients',count(*),'active',count(*) FILTER (WHERE state='active'),"
                              "'waiting',count(*) FILTER (WHERE wait_event_type IS NOT NULL)) "
                              "FROM pg_stat_activity WHERE datname=current_database() AND backend_type='client backend' AND pid<>pg_backend_pid();")
                    activity = json.loads(raw.splitlines()[-1])
                except (subprocess.CalledProcessError, ValueError, IndexError) as error:
                    activity = {'sampling_error': str(error)}
                db_resources.write(json.dumps({'at': datetime.now(timezone.utc).isoformat(), **current, **activity})+'\n')
                db_resources.flush()
                stop.wait(2)

    def counts(label):
        return [int(x) for x in sql(f"SELECT count(*),count(*) FILTER (WHERE NOT is_bot),count(*) FILTER (WHERE is_bot) FROM clicks WHERE utm_campaign='{label}';").split('|')]

    def fault_checks():
        evidence = {}
        api('/bench-1+')
        run(compose + ['stop', '-t', '5', 'db'], capture_output=True)
        try:
            evidence['liveness_during_db_outage'] = api('/health')[0]
            evidence['readiness_during_db_outage'] = api('/health/ready')[0]
            evidence['uncached_during_db_outage'] = api('/never-cached-during-outage')[0]
            delivered = sum(api('/bench-1?utm_campaign=crash-proof')[0] == 302 for _ in range(100))
            run(compose + ['kill', '-s', 'SIGKILL', 'api'], capture_output=True)
        finally:
            run(compose + ['start', 'db'], capture_output=True)
            run(compose + ['up', '-d', '--wait', 'db'], capture_output=True)
        run(compose + ['start', 'api'], capture_output=True)
        wait_ready()
        evidence['api_crash'] = {'redirects_acknowledged': delivered, 'stored_events': counts('crash-proof')[0],
                                 'note': 'Database stopped before requests; API killed before database recovery. Intentional volatile-queue loss probe.'}
        delivered = sum(api('/bench-1?utm_campaign=graceful-proof')[0] == 302 for _ in range(100))
        run(compose + ['stop', '-t', '20', 'api'], capture_output=True)
        evidence['graceful_shutdown'] = {'redirects_acknowledged': delivered, 'stored_events': counts('graceful-proof')[0]}
        began = time.monotonic()
        run(compose + ['start', 'api'], capture_output=True)
        wait_ready()
        evidence['api_start_to_ready_seconds'] = time.monotonic() - began
        (out/'fault-checks.json').write_text(json.dumps(evidence, indent=2))

    try:
        print(f'Building frozen source in isolated project {project}', flush=True)
        with (out/'build.log').open('w') as f:
            run(compose + ['build', 'api'], stdout=f, stderr=subprocess.STDOUT)
        run(compose + ['up', '-d', '--wait', 'db'], capture_output=True)
        for migration in sorted((snapshot/'db/migrations').glob('*.sql')):
            sql(migration.read_text().split('-- +goose Down')[0])
        sql("INSERT INTO users (id,email,email_verified_at,updated_at) VALUES (1,'article1@example.com',now(),now()),(2,'article2@example.com',now(),now());"
            "SELECT setval(pg_get_serial_sequence('users','id'),2);"
            "INSERT INTO sessions (token,user_id,created_at,expires_at,last_access) SELECT 'article-session-'||i,i,now(),now()+interval '1 day',now() FROM generate_series(1,2) i;"
            f"INSERT INTO links (user_id,slug,target_url) SELECT 1,'bench-'||i,'https://example.com/benchmark/'||i FROM generate_series(1,{args.links}) i;")
        if args.integration_tests:
            port = run(compose + ['port', 'db', '5432'], capture_output=True).stdout.strip().rsplit(':', 1)[1]
            with (out/'go-tests.log').open('w') as f:
                run(['go', 'test', '-race', '-count=1', '-v', './...'], cwd=snapshot,
                    env={**os.environ, 'TEST_DATABASE_URL': f'postgres://postgres:local-benchmark-only@127.0.0.1:{port}/benchmark?sslmode=disable'},
                    stdout=f, stderr=subprocess.STDOUT)
        run(compose + ['up', '-d', '--no-deps', 'api'], capture_output=True)
        address = run(compose + ['port', 'api', '8080'], capture_output=True).stdout.strip()
        base = 'http://' + address
        wait_ready()
        ids = run(compose + ['ps', '-q'], capture_output=True).stdout.split()
        meta['containers'] = json.loads(run(docker + ['inspect'] + ids, capture_output=True).stdout)
        (out/'metadata.json').write_text(json.dumps(meta, indent=2))
        if not args.skip_functional:
            checks = check_functional(api, sql)
            (out/'functional.json').write_text(json.dumps(checks, indent=2))
            if not all(c['passed'] for c in checks):
                raise RuntimeError('functional probes failed; see functional.json')
            print(f'Functional checks: {len(checks)} passed', flush=True)
        if args.fault_checks:
            fault_checks()
            print('Crash-loss and graceful-drain probes complete', flush=True)
        time.sleep(2)
        monitor_thread = threading.Thread(target=monitor, daemon=True)
        monitor_thread.start()
        for stage in stages:
            label = stage['name']
            current['stage'] = label
            before = int(sql('SELECT coalesce(sum(click_count),0) FROM links;'))
            api('/bench-1+')
            print(f'Stage {label}: {stage}', flush=True)
            command = docker + ['run', '--rm', '--name', project+'-k6', '--network', project+'_default',
                '--user', f'{os.getuid()}:{os.getgid()}', '-v', str(scripts)+':/scripts:ro', '-v', str(out)+':/results']
            settings = {'RUN_ID': label, 'DURATION': stage['seconds'], 'LINKS': args.links,
                        'REDIRECT_RATE': stage['redirects'], 'CREATE_RATE': stage['creates'], 'STATS_RATE': stage['reports'],
                        'SHAPE': stage.get('shape', 'uniform'), 'VUS': args.vus, 'CREATE_VUS': args.create_vus, 'STATS_VUS': args.stats_vus}
            for key, value in settings.items():
                command += ['-e', f'{key}={value}']
            command += ['grafana/k6:2.3.0', 'run', '--quiet', '/scripts/article.js']
            fault = []
            with (out/f'{label}.log').open('w') as f:
                proc = subprocess.Popen(command, stdout=f, stderr=subprocess.STDOUT)
                try:
                    if stage.get('db_outage_seconds'):
                        time.sleep(8)
                        run(compose + ['stop', '-t', '5', 'db'], capture_output=True)
                        fault.append({'event': 'db stopped', 'at': datetime.now(timezone.utc).isoformat()})
                        time.sleep(stage['db_outage_seconds'])
                        run(compose + ['start', 'db'], capture_output=True)
                        fault.append({'event': 'db start requested', 'at': datetime.now(timezone.utc).isoformat()})
                    code = proc.wait(timeout=stage['seconds']+90)
                finally:
                    if proc.poll() is None:
                        subprocess.run(docker + ['stop', '-t', '10', project+'-k6'], capture_output=True)
                        proc.wait(timeout=30)
            raw = out/f'{label}.json'
            if not raw.exists():
                raise RuntimeError(f'k6 summary absent: {label}.log')
            result = summarize(json.loads(raw.read_text()), stage)
            settle = time.monotonic()
            expected = [result['redirects'], result['humans'], result['bots']]
            # Wait the full bounded drain window when counts differ; a stalled batch can recover.
            while True:
                actual = counts(label)
                if actual == expected or time.monotonic() - settle >= 45:
                    break
                time.sleep(1)
            after = int(sql('SELECT coalesce(sum(click_count),0) FROM links;'))
            created = [int(x) for x in sql(f"SELECT count(*),count(DISTINCT slug),count(DISTINCT target_url) FROM links WHERE target_url LIKE 'https://example.com/article/{label}/%';").split('|')]
            stats_checks = []
            for link_id in sorted({1, min(2, args.links), args.links}):
                status, response, _ = api(f'/api/v1/links/{link_id}/stats?days=30')
                expected_stats = [int(x) for x in sql(f"SELECT count(*) FILTER (WHERE NOT is_bot),count(DISTINCT visitor_hash) FILTER (WHERE NOT is_bot),count(*) FILTER (WHERE is_bot) FROM clicks WHERE link_id={link_id} AND created_at>=(date_trunc('day',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')-interval '29 days';").split('|')]
                returned = [response.get(k) for k in ['clicks', 'unique_visitors', 'bot_clicks']] if isinstance(response, dict) else []
                stats_checks.append({'link_id': link_id, 'status': status, 'api': returned, 'database': expected_stats, 'match': returned == expected_stats})
            result.update({'stored_events_human_bot': actual, 'analytics_match': actual == expected and after-before == result['humans'],
                           'counter_delta': after-before, 'created_rows_unique_slugs_targets': created,
                           'creation_match': created == [result['creates']]*3, 'stats_checks': stats_checks,
                           'settle_seconds': time.monotonic()-settle, 'k6_exit_code': code, 'fault_timeline': fault})
            result['passed'] = (code == 0 and result['completed_duration'] and result['dropped_iterations'] == 0
                                and result['incorrect_responses'] == 0 and result['analytics_match'] and result['creation_match']
                                and all(c['match'] for c in stats_checks))
            results.append(result)
            (out/'results.json').write_text(json.dumps(results, indent=2))
            print(json.dumps(result), flush=True)
            time.sleep(3)
        (out/'database.json').write_text(sql("SELECT json_build_object('bytes',pg_database_size(current_database()),'links',(SELECT count(*) FROM links),'clicks',(SELECT count(*) FROM clicks),'receipts',(SELECT count(*) FROM click_batches));"))
    finally:
        stop.set()
        if monitor_thread:
            monitor_thread.join(timeout=10)
        with (out/'api.log').open('w') as f:
            subprocess.run(compose + ['logs', '--no-color', 'api'], stdout=f, stderr=subprocess.STDOUT)
        with (out/'db.log').open('w') as f:
            subprocess.run(compose + ['logs', '--no-color', 'db'], stdout=f, stderr=subprocess.STDOUT)
        # Only the UUID-named benchmark project is removed, never the development stack.
        run(compose + ['down', '--volumes', '--remove-orphans'], capture_output=True)
    if any(not result['passed'] for result in results):
        raise SystemExit(1)


if __name__ == '__main__':
    main()
