#!/usr/bin/env python3
"""Benchmark independent dashboards and skewed click histories in disposable services."""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import random
import subprocess
import sys
import threading
import time

sys.dont_write_bytecode = True
from run import HERE, SERVER, run


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--users', type=int, default=20000)
    p.add_argument('--links-per-user', type=int, default=5)
    p.add_argument('--history', type=int, default=3000000)
    p.add_argument('--levels', default='1000,5000,10000,20000', help='Active dashboards per stage')
    p.add_argument('--refresh-seconds', type=int, default=30)
    p.add_argument('--redirect-rate', type=int, default=1000)
    p.add_argument('--duration', type=int, default=60)
    p.add_argument('--confirm-duration', type=int, default=120, help='Repeat highest passing level; zero skips')
    p.add_argument('--burst-requests', type=int, default=2000, help='Extra 90-day reports in two seconds; zero skips')
    p.add_argument('--stats-vus', type=int, default=400)
    p.add_argument('--redirect-vus', type=int, default=250)
    p.add_argument('--burst-vus', type=int, default=600)
    p.add_argument('--output', type=Path, required=True)
    args = p.parse_args()
    try:
        levels = [int(x) for x in args.levels.split(',')]
    except ValueError:
        p.error('levels must be comma-separated integers')
    if min(levels + [args.users, args.links_per_user, args.history, args.refresh_seconds,
                     args.redirect_rate, args.duration, args.stats_vus, args.redirect_vus, args.burst_vus]) < 1:
        p.error('counts, rates, durations, and VUs must be positive')
    if args.users < 2 or max(levels) > args.users or args.history < 3 or min(args.confirm_duration, args.burst_requests) < 0:
        p.error('need >=2 users, >=3 history rows, levels <= users, and nonnegative confirmation/burst settings')
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=True)
    if (out/'metadata.json').exists():
        p.error('output already contains a run; choose a new directory')
    links = args.users * args.links_per_user
    popular = min(100, args.users)
    project = f'sh-dashboard-{int(time.time())}'
    docker = ['docker', '--context', 'default']
    compose = docker + ['compose', '-p', project, '-f', str(HERE/'compose.yml')]
    results = []
    stop = threading.Event()
    monitor_thread = None
    stage = {'name': 'setup'}

    def sql(statement):
        return run(compose + ['exec', '-T', 'db', 'psql', '-U', 'postgres', '-d', 'benchmark',
                             '-v', 'ON_ERROR_STOP=1', '-tA'], input=statement, capture_output=True).stdout.strip()

    def api(path, user=1, status=False, timeout=30):
        # Run curl in a small utility container on the private benchmark network.
        command = docker + ['run', '--rm', '--network', project+'_default', 'curlimages/curl:8.12.1',
                            '--silent', '--show-error', '--max-time', str(timeout),
                            '--header', f'Cookie: limen_session=dashboard-session-{user}']
        if status:
            command += ['--output', '/dev/null', '--write-out', '%{http_code}']
        else:
            command += ['--fail']
        return run(command + ['http://api:8080'+path], capture_output=True).stdout.strip()

    def monitor():
        with (out/'resources.jsonl').open('w') as resources, (out/'database-resources.jsonl').open('w') as database:
            while not stop.is_set():
                stamp = datetime.now(timezone.utc).isoformat()
                current_stage = stage['name']
                sampled = subprocess.run(docker + ['stats', '--no-stream', '--format', '{{json .}}'], capture_output=True, text=True)
                for line in sampled.stdout.splitlines():
                    sample = json.loads(line)
                    if sample.get('Name', '').startswith(project):
                        resources.write(json.dumps({'at':stamp, 'stage':current_stage, **sample})+'\n')
                resources.flush()
                try:
                    sample = json.loads(sql("SET statement_timeout='2s'; SELECT json_build_object("
                        "'clients',count(*),'active',count(*) FILTER (WHERE state='active'),"
                        "'waiting',count(*) FILTER (WHERE wait_event_type IS NOT NULL)) "
                        "FROM pg_stat_activity WHERE datname=current_database() AND backend_type='client backend' AND pid<>pg_backend_pid();").splitlines()[-1])
                    database.write(json.dumps({'at':stamp, 'stage':current_stage, **sample})+'\n')
                    database.flush()
                except (subprocess.CalledProcessError, ValueError) as error:
                    database.write(json.dumps({'at':stamp, 'stage':current_stage, 'sampling_error':str(error)})+'\n')
                stop.wait(3)

    def reconcile_stats():
        checked = []
        for user in sorted({1, 2, popular, args.users}):
            link_id = 1 + (user-1)*args.links_per_user
            for days in [30, 90]:
                body = json.loads(api(f'/api/v1/links/{link_id}/stats?days={days}', user=user))
                expected = [int(x) for x in sql(f"SELECT count(*) FILTER (WHERE NOT is_bot),"
                    f"count(DISTINCT visitor_hash) FILTER (WHERE NOT is_bot), count(*) FILTER (WHERE is_bot) "
                    f"FROM clicks WHERE link_id={link_id} AND created_at>='{body['since']}'::date;").split('|')]
                actual = [body['clicks'], body['unique_visitors'], body['bot_clicks']]
                checked.append({'user':user, 'link':link_id, 'days':days, 'api':actual, 'database':expected, 'match':actual==expected})
        return checked

    def execute(label, active, duration, burst=0):
        stage['name'] = label
        baseline_counter = int(sql('SELECT coalesce(sum(click_count),0) FROM links;'))
        print(f'{label}: {active} dashboards / {args.refresh_seconds}s = {active/args.refresh_seconds:.2f} stats/s; '
              f'{args.redirect_rate} redirects/s; {duration}s; burst={burst}', flush=True)
        values = {'ACTIVE_DASHBOARDS':active, 'REFRESH_SECONDS':args.refresh_seconds, 'DURATION':f'{duration}s',
                  'RUN_ID':label, 'LINKS':links, 'LINKS_PER_USER':args.links_per_user, 'POPULAR_LINKS':popular,
                  'REDIRECT_RATE':args.redirect_rate, 'STATS_VUS':args.stats_vus, 'REDIRECT_VUS':args.redirect_vus,
                  'BURST_REQUESTS':burst, 'BURST_VUS':args.burst_vus}
        command = docker + ['run', '--rm', '--name', project+'-k6', '--network', project+'_default',
                            '--user', f'{os.getuid()}:{os.getgid()}', '-v', f'{HERE}:/scripts:ro', '-v', f'{out}:/results']
        for name, value in values.items():
            command += ['-e', f'{name}={value}']
        command += ['grafana/k6:2.3.0', 'run', '--quiet', '/scripts/dashboards.js']
        with (out/(label+'.log')).open('w') as log:
            process = subprocess.run(command, stdout=log, stderr=subprocess.STDOUT)
        summary = json.loads((out/(label+'.json')).read_text())
        metrics = summary['metrics']
        values = lambda name: metrics.get(name, {}).get('values', {})
        count = lambda name: values(name).get('count', 0)
        elapsed = summary['state']['testRunDurationMs']/1000
        stage['name'] = label+'-settle'
        # Wait through bounded retries, rather than treating a temporarily stable count as a drain.
        expected = [count('successful_redirects'), count('human_redirects'), count('bot_redirects')]
        actual = None
        for attempt in range(30):
            actual = [int(x) for x in sql(f"SELECT count(*),count(*) FILTER (WHERE NOT is_bot),count(*) FILTER (WHERE is_bot) "
                        f"FROM clicks WHERE utm_campaign='{label}';").split('|')]
            if actual[0] >= expected[0]:
                break
            time.sleep(1)
        counter = int(sql('SELECT coalesce(sum(click_count),0) FROM links;'))-baseline_counter
        # Full ownership and large-history checks run before and after the load stages.
        row = {
            'stage':label, 'active_dashboards':active, 'target_stats_rps':active/args.refresh_seconds,
            'target_redirect_rps':args.redirect_rate, 'duration_s':duration, 'measured_seconds':elapsed,
            'completed_duration':elapsed>=duration, 'burst_target':burst, 'http_requests':count('http_reqs'),
            'http_error_rate':values('http_req_failed').get('rate', 0), 'dropped_iterations':count('dropped_iterations'),
            'dropped_redirects':count('dropped_iterations{scenario:redirects}'),
            'dropped_stats':count('dropped_iterations{scenario:dashboards}'),
            'dropped_burst':count('dropped_iterations{scenario:report_burst}'),
            'redirect_attempts':count('redirect_attempts'), 'redirects':expected[0], 'redirect_rps':expected[0]/elapsed,
            'stats_attempts':count('stats_attempts'), 'successful_stats':count('successful_stats'),
            'stats_rps':count('successful_stats')/elapsed,
            'distinct_dashboard_accounts_attempted':min(active,count('stats_attempts')),
            'burst_attempts':count('burst_attempts'), 'successful_burst':count('successful_burst'),
            'latency_ms':values('redirect_latency'), 'stats_latency_ms':values('stats_latency'),
            'large_link_stats_latency_ms':values('large_link_stats_latency'),
            'popular_link_stats_latency_ms':values('popular_link_stats_latency'),
            'standard_link_stats_latency_ms':values('standard_link_stats_latency'),
            'burst_latency_ms':values('burst_latency'),
            'expected_clicks':expected, 'stored_clicks':actual, 'link_counter_delta':counter,
            'analytics_match':actual==expected and counter==expected[1],
            'k6_exit_code':process.returncode, 'thresholds_passed':process.returncode==0,
        }
        row['passed'] = row['thresholds_passed'] and row['completed_duration'] and row['analytics_match']
        results.append(row)
        (out/'results.json').write_text(json.dumps(results,indent=2)+'\n')
        print(json.dumps(row), flush=True)
        time.sleep(3)
        return row

    meta = {'started_at':datetime.now(timezone.utc).isoformat(),
            'base_commit':run(['git','rev-parse','HEAD'],cwd=SERVER,capture_output=True).stdout.strip(),
            'workload':vars(args)|{'output':str(out)}, 'k6_image':'grafana/k6:2.3.0',
            'cpu':run(['lscpu'],capture_output=True).stdout,
            'history_days':90, 'popular_links':popular,
            'notes':'Synthetic independent account polling, not full browsers. All services share one host. No outbound email or redirect-target traffic. CPU/memory unchanged. Database activity sampling adds one short-lived diagnostic connection.'}
    meta['source_sha256'] = {str(path.relative_to(SERVER)):hashlib.sha256(path.read_bytes()).hexdigest()
                            for path in sorted([*SERVER.rglob('*.go'), *SERVER.glob('db/**/*.sql'), SERVER/'go.mod', SERVER/'go.sum', HERE/'dashboards.py', HERE/'dashboards.js', HERE/'compose.yml'])}
    (out/'metadata.json').write_text(json.dumps(meta,indent=2)+'\n')
    try:
        print('Building isolated API...', flush=True)
        with (out/'build.log').open('w') as log:
            run(compose+['build','api'],stdout=log,stderr=subprocess.STDOUT)
        # Pull before measuring anything so network/image startup cannot distort a load stage.
        run(docker+['pull','curlimages/curl:8.12.1'],stdout=subprocess.DEVNULL)
        run(compose+['up','-d','--wait','db'])
        for migration in sorted((SERVER/'db/migrations').glob('*.sql')):
            sql(migration.read_text().split('-- +goose Down')[0])
        print(f'Seeding {args.users:,} accounts, {links:,} links, {args.history:,} clicks...',flush=True)
        sql(f"INSERT INTO users (id,email,email_verified_at,updated_at) SELECT i,'dashboard-'||i||'@example.com',now(),now() FROM generate_series(1,{args.users}) i;"
            f"INSERT INTO sessions (token,user_id,created_at,expires_at,last_access) SELECT 'dashboard-session-'||i,i,now(),now()+interval '1 day',now() FROM generate_series(1,{args.users}) i;"
            f"INSERT INTO links (id,user_id,slug,target_url) SELECT i,1+(i-1)/{args.links_per_user},'dashboard-link-'||i,'https://example.com/dashboard/'||i FROM generate_series(1,{links}) i;"
            "SELECT setval(pg_get_serial_sequence('users','id'),(SELECT max(id) FROM users));"
            "SELECT setval(pg_get_serial_sequence('links','id'),(SELECT max(id) FROM links));")
        fixtures = [{'user_id':i,'link_id':1+(i-1)*args.links_per_user,'token':f'dashboard-session-{i}',
                     'tier':'large' if i==1 else 'popular' if i<=popular else 'standard'} for i in range(1,args.users+1)]
        random.Random(20260922).shuffle(fixtures)
        (out/'dashboards.json').write_text(json.dumps(fixtures))
        third = args.history//3
        for start in range(1,args.history+1,100000):
            end = min(start+99999,args.history)
            sql(f"""INSERT INTO clicks (link_id,created_at,visitor_hash,user_agent,referer_host,source,utm_source,
                        utm_medium,utm_campaign,device_type,browser,os,country,city,is_bot)
                SELECT CASE WHEN i<={third} THEN 1 WHEN i<={2*third} THEN 1+(1+(i%{popular-1}))*{args.links_per_user}
                            ELSE 1+(i%{args.users})*{args.links_per_user} END,
                    date_trunc('day',now())-((i%90)*interval '1 day')+((i%3600)*interval '1 second'),
                    md5((i%10000)::text||':'||(i%90)::text)||md5((i%10000)::text||':'||(i%90)::text),
                    'Synthetic historical browser', 'example.com',
                    (ARRAY['Direct','X (Twitter)','Facebook','Google'])[1+(i%4)],
                    (ARRAY['newsletter','social','search'])[1+(i%3)], 'synthetic', 'seed-history',
                    (ARRAY['desktop','mobile'])[1+(i%2)], 'Firefox', 'Linux',
                    (ARRAY['NG','US','GB','DE'])[1+(i%4)], 'Synthetic city', (i/7)%10=0
                FROM generate_series({start},{end}) i;""")
            if end%500000==0 or end==args.history:
                print(f'Historical clicks seeded: {end:,}/{args.history:,}',flush=True)
        sql("UPDATE links SET click_count=t.n FROM (SELECT link_id,count(*) AS n FROM clicks WHERE NOT is_bot GROUP BY link_id) t WHERE links.id=t.link_id;")
        sql('VACUUM ANALYZE;')
        (out/'seed.json').write_text(sql("SELECT json_build_object('users',(SELECT count(*) FROM users),'sessions',(SELECT count(*) FROM sessions),"
            "'links',(SELECT count(*) FROM links),'clicks',(SELECT count(*) FROM clicks),'largest_link_clicks',(SELECT count(*) FROM clicks WHERE link_id=1),'bytes',pg_database_size(current_database()));")+'\n')
        run(compose+['up','-d','--no-deps','api'])
        for _ in range(30):
            probe = subprocess.run(compose+['exec','-T','api','wget','-qO-','http://localhost:8080/health'],capture_output=True)
            if probe.returncode==0:
                break
            time.sleep(1)
        else:
            raise RuntimeError('API never became healthy')
        ids = run(compose+['ps','-q'],capture_output=True).stdout.split()
        containers = json.loads(run(docker+['inspect']+ids,capture_output=True).stdout)
        meta['containers'] = [{'name':c['Name'],'image':c['Image'],'cpu_limit':c['HostConfig']['NanoCpus']/1e9,
                               'memory_bytes':c['HostConfig']['Memory']} for c in containers]
        (out/'metadata.json').write_text(json.dumps(meta,indent=2)+'\n')
        print('Checking seeded analytics and cross-account access...',flush=True)
        assert api('/api/v1/links/1/stats',user=2,status=True)=='404', 'cross-account stats access was allowed'
        initial = reconcile_stats()
        (out/'preflight.json').write_text(json.dumps(initial,indent=2)+'\n')
        assert all(row['match'] for row in initial), 'seeded stats do not match SQL'
        monitor_thread = threading.Thread(target=monitor,daemon=True)
        monitor_thread.start()
        passed = []
        for i, active in enumerate(levels):
            result = execute(f'ramp-{i+1}-{active}dash',active,args.duration)
            if result['passed']:
                passed.append(active)
        if args.confirm_duration and passed:
            execute(f'confirm-{max(passed)}dash',max(passed),args.confirm_duration)
        if args.burst_requests:
            execute(f'burst-{max(levels)}dash',max(levels),max(30,args.duration),args.burst_requests)
        stage['name']='final-reconciliation'
        final = reconcile_stats()
        (out/'reconciliation.json').write_text(json.dumps(final,indent=2)+'\n')
        assert all(row['match'] for row in final), 'final stats do not match SQL'
        (out/'database.json').write_text(sql("SELECT json_build_object('bytes',pg_database_size(current_database()),'clicks',(SELECT count(*) FROM clicks),"
            "'batch_receipts',(SELECT count(*) FROM click_batches),'temp_bytes',temp_bytes,'deadlocks',deadlocks) FROM pg_stat_database WHERE datname=current_database();")+'\n')
        print('Completed. All sampled final analytics matched PostgreSQL.',flush=True)
    finally:
        stop.set()
        if monitor_thread:
            monitor_thread.join(timeout=10)
        with (out/'api.log').open('w') as log:
            subprocess.run(compose+['logs','--no-color','api'],stdout=log,stderr=subprocess.STDOUT)
        with (out/'db.log').open('w') as log:
            subprocess.run(compose+['logs','--no-color','db'],stdout=log,stderr=subprocess.STDOUT)
        run(compose+['down','--volumes','--remove-orphans'])


if __name__=='__main__':
    main()
