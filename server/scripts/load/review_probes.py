"""Run review regressions in a separate source copy, preserving both failures and source."""
import json
from pathlib import Path
import shutil
import subprocess


def run_probes(snapshot, output, env, fixtures=None):
    source = output/'review-source'
    shutil.copytree(snapshot, source)
    fixtures = fixtures or Path(__file__).resolve().parent/'review_tests'
    for path in fixtures.glob('*.go.txt'):
        package = path.name.split('.')[0]
        shutil.copy2(path, source/'internal'/package/'review_regression_test.go')
    command = ['go', 'test', '-race', '-count=1', '-json', '-run', '^TestReview',
               './internal/rediscache', './internal/service', './internal/maintenance']
    result = subprocess.run(command, cwd=source, env=env, text=True,
                            stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    (output/'review-probes.jsonl').write_text(result.stdout)
    tests = []
    for line in result.stdout.splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if event.get('Test') and event.get('Action') in ('pass', 'fail', 'skip'):
            tests.append({k: event[k] for k in ('Package', 'Test', 'Action')})
    summary = {'command': command, 'exit_code': result.returncode, 'tests': tests,
               'note': 'These assert desired behavior. Failures are review findings, not passing checks.'}
    (output/'review-probes.json').write_text(json.dumps(summary, indent=2))
    print('Review probes: '+json.dumps(summary), flush=True)
    if len(tests) != 4 or any(t['Action'] == 'skip' for t in tests):
        raise RuntimeError('Review probes did not all run; inspect review-probes.jsonl')
