#!/usr/bin/env python3
"""Run a k6 benchmark in disposable Docker services, then reconcile click records."""
import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import platform
import subprocess
import threading
import time

HERE = Path(__file__).resolve().parent
SERVER = HERE.parent.parent


def run(command, **kwargs):
    return subprocess.run(command, check=True, text=True, **kwargs)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--rates', default='100,500,1000,2000,3200')
    parser.add_argument('--duration', type=int, default=30, help='Seconds at each rate')
    parser.add_argument('--links', type=int, default=1000)
    parser.add_argument('--stats-rate', type=int, default=0)
    parser.add_argument('--vus', type=int, default=200)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    rates = [int(x) for x in args.rates.split(',')]
    if min(rates + [args.duration, args.links, args.vus]) < 1 or args.stats_rate < 0:
        parser.error('Rates, duration, links and VUs must be positive; stats-rate must be nonnegative')
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=True)
    project = 'sh-load-' + str(int(time.time()))
    docker = ['docker', '--context', 'default']
    compose = docker + ['compose', '-p', project, '-f', str(HERE / 'compose.yml')]

    def sql(statement):
        return run(compose + ['exec', '-T', 'db', 'psql', '-U', 'postgres', '-d', 'benchmark', '-v', 'ON_ERROR_STOP=1', '-tA'], input=statement, capture_output=True).stdout.strip()

    meta = {
        'started_at': datetime.now(timezone.utc).isoformat(),
        'commit': run(['git', 'rev-parse', 'HEAD'], cwd=SERVER, capture_output=True).stdout.strip(),
        'host': platform.platform(), 'logical_cpus': os.cpu_count(),
        'cpu': run(['lscpu'], capture_output=True).stdout,
        'api_cpu_limit': 2, 'api_memory': '512MiB', 'db_cpu_limit': 2, 'db_memory': '1GiB',
        'k6_image': 'grafana/k6:2.3.0',
        'workload': vars(args) | {'output': str(out)},
        'notes': 'API, database, and generator share one host. Internal Docker HTTP, no TLS/CDN. Analytics and Gin request logging enabled. Separate fresh database. No outbound emails. Redirects are not followed.',
    }
    results = []
    stop = threading.Event()

    def monitor(ids):
        with (out / 'resources.jsonl').open('w') as f:
            while not stop.is_set():
                p = subprocess.run(docker + ['stats', '--no-stream', '--format', '{{json .}}'], text=True, capture_output=True)
                for line in p.stdout.splitlines():
                    try:
                        sample = json.loads(line)
                        if sample.get('Name', '').startswith(project):
                            f.write(json.dumps({'at': datetime.now(timezone.utc).isoformat(), **sample}) + '\n')
                    except json.JSONDecodeError:
                        pass
                f.flush()
                stop.wait(3)

    try:
        print('Building isolated API and starting benchmark database...', flush=True)
        run(compose + ['build', 'api'], stdout=(out / 'build.log').open('w'), stderr=subprocess.STDOUT)
        run(compose + ['up', '-d', '--wait', 'db', 'redis'])
        for migration in sorted((SERVER / 'db/migrations').glob('*.sql')):
            sql(migration.read_text().split('-- +goose Down')[0])
        sql("INSERT INTO users (id,email,email_verified_at,updated_at) VALUES (1,'loadtest@example.com',now(),now());"
            "INSERT INTO sessions (token,user_id,created_at,expires_at,last_access) VALUES ('local-load-test-session',1,now(),now()+interval '1 day',now());"
            f"INSERT INTO links (user_id,slug,target_url) SELECT 1,'bench-'||i,'https://example.com/benchmark/'||i FROM generate_series(1,{args.links}) i;")
        run(compose + ['up', '-d', '--no-deps', 'api'])
        for _ in range(30):
            p = subprocess.run(compose + ['exec', '-T', 'api', 'wget', '-qO-', 'http://localhost:8080/health'], capture_output=True, text=True)
            if p.returncode == 0:
                break
            time.sleep(1)
        else:
            raise RuntimeError('Benchmark API did not become healthy')
        ids = run(compose + ['ps', '-q'], capture_output=True).stdout.split()
        meta['containers'] = json.loads(run(docker + ['inspect'] + ids, capture_output=True).stdout)
        # Container environments contain only public benchmark fixtures, never server/.env.
        (out / 'metadata.json').write_text(json.dumps(meta, indent=2))
        thread = threading.Thread(target=monitor, args=(ids,), daemon=True)
        thread.start()

        for index, rate in enumerate(rates):
            label = f'run-{index + 1}-{rate}rps'
            before = int(sql('SELECT coalesce(sum(human_clicks),0) FROM link_counters;'))
            print(f'Starting {label}: {args.duration}s; {args.stats_rate} stats reads/s', flush=True)
            cmd = docker + ['run', '--rm', '--name', project + '-k6', '--network', project + '_default',
                '--user', str(os.getuid()) + ':' + str(os.getgid()),
                '-v', str(HERE) + ':/scripts:ro', '-v', str(out) + ':/results',
                '-e', 'RATE=' + str(rate), '-e', 'DURATION=' + str(args.duration) + 's',
                '-e', 'RUN_ID=' + label, '-e', 'LINKS=' + str(args.links),
                '-e', 'STATS_RATE=' + str(args.stats_rate), '-e', 'VUS=' + str(args.vus),
                'grafana/k6:2.3.0', 'run', '--quiet', '/scripts/redirects.js']
            started = time.monotonic()
            with (out / (label + '.log')).open('w') as log:
                process = subprocess.run(cmd, stdout=log, stderr=subprocess.STDOUT)
            elapsed = time.monotonic() - started
            summary_path = out / (label + '.json')
            if not summary_path.exists():
                raise RuntimeError('k6 produced no summary; inspect ' + str(out / (label + '.log')))
            summary = json.loads(summary_path.read_text())
            metrics = summary['metrics']
            measured_seconds = summary['state']['testRunDurationMs'] / 1000

            def count(name):
                return metrics.get(name, {}).get('values', {}).get('count', 0)

            expected = count('successful_redirects')
            settle_start = time.monotonic()
            last = None
            stable = 0
            for _ in range(30):
                row = sql(f"SELECT count(*),count(*) FILTER (WHERE NOT is_bot),count(*) FILTER (WHERE is_bot) FROM clicks WHERE utm_campaign='{label}';")
                actual = [int(x) for x in row.split('|')]
                stable = stable + 1 if actual == last else 0
                last = actual
                if actual[0] >= expected or stable >= 5:
                    break
                time.sleep(1)
            after = int(sql('SELECT coalesce(sum(human_clicks),0) FROM link_counters;'))
            stats_reconciliation = []
            for link_id in sorted({1, min(2, args.links), args.links}):
                response = run(compose + ['exec', '-T', 'api', 'wget', '-qO-',
                    '--header=Cookie: limen_session=local-load-test-session',
                    f'http://localhost:8080/api/v1/links/{link_id}/stats?days=7'], capture_output=True)
                actual_stats = json.loads(response.stdout)
                expected_stats = [int(x) for x in sql(f'SELECT count(*) FILTER (WHERE NOT is_bot),count(DISTINCT visitor_hash) FILTER (WHERE NOT is_bot),count(*) FILTER (WHERE is_bot) FROM clicks WHERE link_id={link_id};').split('|')]
                returned = [actual_stats['clicks'], actual_stats['unique_visitors'], actual_stats['bot_clicks']]
                stats_reconciliation.append({'link_id': link_id, 'api': returned, 'database': expected_stats, 'match': returned == expected_stats})
            result = {
                'run': label, 'target_rps': rate, 'duration_s': args.duration,
                'stats_rps': args.stats_rate, 'wall_seconds_including_container': elapsed,
                'http_requests': count('http_reqs'), 'redirects': expected,
                'measured_seconds': measured_seconds,
                'completed_duration': measured_seconds >= args.duration,
                'redirect_rps': expected / measured_seconds,
                'http_error_rate': metrics['http_req_failed']['values']['rate'],
                'dropped_iterations': count('dropped_iterations'),
                'latency_ms': metrics.get('redirect_latency', {}).get('values', {}),
                'stats_latency_ms': metrics.get('stats_latency', {}).get('values', {}),
                'human_redirects': count('human_redirects'), 'bot_redirects': count('bot_redirects'),
                'stored_clicks': actual[0], 'stored_human_clicks': actual[1], 'stored_bot_clicks': actual[2],
                'link_counter_delta': after - before,
                'analytics_match': actual == [expected, count('human_redirects'), count('bot_redirects')] and after - before == count('human_redirects'),
                'stats_reconciliation': stats_reconciliation,
                'settle_wait_s': time.monotonic() - settle_start,
                'k6_exit_code': process.returncode,
                'thresholds_passed': process.returncode == 0,
            }
            results.append(result)
            (out / 'results.json').write_text(json.dumps(results, indent=2))
            print(json.dumps(result), flush=True)
            time.sleep(2)
        run(compose + ['logs', '--no-color', 'api'], stdout=(out / 'api.log').open('w'), stderr=subprocess.STDOUT)
        (out / 'database.json').write_text(sql("SELECT json_build_object('bytes',pg_database_size(current_database()),'clicks',(SELECT count(*) FROM clicks));"))
    finally:
        stop.set()
        with (out / 'api.log').open('w') as log:
            subprocess.run(compose + ['logs', '--no-color', 'api'], stdout=log, stderr=subprocess.STDOUT)
        run(compose + ['down', '--volumes', '--remove-orphans'])


if __name__ == '__main__':
    main()
