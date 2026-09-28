#!/usr/bin/env python3
"""Reproduce cache invalidation ordering without changing application code.

The Go probe intentionally fails while an in-flight load can undo Forget.
Its exit code and output are evidence, not a passing application test.
"""
import argparse
import json
from pathlib import Path
import shutil
import subprocess

PROBE = '''package cache

import (
    "context"
    "testing"
    "time"
)

func TestForgetMustPreventInFlightStaleFill(t *testing.T) {
    c := New[string](10, time.Minute, time.Second)
    loaded, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
    go func() {
        defer close(done)
        c.Get(context.Background(), "removed", func(context.Context, string) (string, bool, error) {
            close(loaded) // database read saw the active link before deletion
            <-release
            return "old destination", true, nil
        })
    }()
    <-loaded
    c.Forget("removed") // deletion committed, then invalidated the key
    close(release)
    <-done
    value, found, err := c.Get(context.Background(), "removed", func(context.Context, string) (string, bool, error) {
        return "", false, nil // database now says the link is removed
    })
    if err != nil { t.Fatal(err) }
    if found { t.Fatalf("stale redirect survives Forget: %q", value) }
}
'''


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--output', type=Path, required=True)
    args = p.parse_args()
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        p.error('output must be empty')
    server = Path(__file__).resolve().parents[2]
    shutil.copy2(server/'internal/cache/cache.go', out/'cache.go')
    (out/'cache_review_test.go').write_text(PROBE)
    result = subprocess.run(['go', 'test', '-race', '-count=1', '-v', str(out/'cache.go'),
                             str(out/'cache_review_test.go')], cwd=server, text=True,
                            stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    (out/'probe.log').write_text(result.stdout)
    summary = {'invalidation_safety_passed': result.returncode == 0,
               'exit_code': result.returncode,
               'stale_fill_reproduced': 'stale redirect survives Forget' in result.stdout}
    (out/'result.json').write_text(json.dumps(summary, indent=2))
    print(json.dumps(summary))
    if result.returncode and not summary['stale_fill_reproduced']:
        raise SystemExit('Probe could not complete; inspect probe.log')


if __name__ == '__main__':
    main()
