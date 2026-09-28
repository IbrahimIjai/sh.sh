"""Functional probes against the runner-owned disposable stack; never follows redirects."""
from concurrent.futures import ThreadPoolExecutor
import time


def check_functional(api, sql):
    checks = []

    def check(name, got, expected):
        checks.append({'name': name, 'got': got, 'expected': expected, 'passed': got == expected})

    def create(body, user=1):
        return api('/api/v1/links', 'POST', body, user)

    check('liveness', api('/health')[0], 200)
    check('readiness', api('/health/ready')[0], 200)
    check('anonymous custom slug forbidden', create({'target_url': 'https://example.com', 'slug': 'custom'}, 0)[0], 401)
    check('invalid URL rejected', create({'target_url': 'javascript:alert(1)'})[0], 400)
    check('reserved slug rejected', create({'target_url': 'https://example.com', 'slug': 'health'})[0], 400)
    status, anon, _ = create({'target_url': 'https://example.com/anonymous'}, 0)
    check('anonymous creation', status, 201)
    token = anon['claim_token']
    check('anonymous lookup', len(api('/api/v1/links/lookup', 'POST', {'claim_tokens': [token]}, 0)[1]), 1)
    check('claim requires login', api('/api/v1/links/claim', 'POST', {'claim_tokens': [token]}, 0)[0], 401)
    check('claim keeps link identity', api('/api/v1/links/claim', 'POST', {'claim_tokens': [token]})[1]['claimed'][0]['id'], anon['id'])
    check('claim token consumed', api('/api/v1/links/lookup', 'POST', {'claim_tokens': [token]}, 0)[1], [])
    check('ownership stats', api(f"/api/v1/links/{anon['id']}/stats", user=2)[0], 404)
    check('ownership delete', api(f"/api/v1/links/{anon['id']}", 'DELETE', user=2)[0], 404)

    # Simultaneous real inserts: exactly one winner; no pre-check race.
    with ThreadPoolExecutor(max_workers=20) as pool:
        responses = list(pool.map(lambda _: create({'target_url': 'https://example.com/collision', 'slug': 'contended'}), range(20)))
    codes = [r[0] for r in responses]
    check('concurrent custom slug: one winner', codes.count(201), 1)
    check('concurrent custom slug: nineteen conflicts', codes.count(409), 19)
    winner = next(r[1] for r in responses if r[0] == 201)
    check('warm redirect', api('/contended')[0], 302)
    check('delete', api(f"/api/v1/links/{winner['id']}", 'DELETE')[0], 204)
    check('deleted cached link unavailable', api('/contended')[0], 404)
    check('deleted slug never reused', create({'target_url': 'https://example.com/new', 'slug': 'contended'})[0], 409)

    with ThreadPoolExecutor(max_workers=20) as pool:
        responses = list(pool.map(lambda i: create({'target_url': f'https://example.com/random/{i}'}), range(50)))
    check('concurrent generated creates', sum(r[0] == 201 for r in responses), 50)
    check('generated slug uniqueness', len({r[1].get('slug') for r in responses}), 50)
    check('unknown slug', api('/functional-new')[0], 404)
    created = create({'target_url': 'https://example.com/track?utm_source=existing', 'slug': 'functional-new'})[1]
    check('creation invalidates negative cache', api('/functional-new+')[0], 200)
    check('preview contains destination', 'example.com/track' in api('/functional-new+')[1], True)
    status, _, headers = api('/functional-new?utm_source=incoming&utm_campaign=functional', browser=True)
    check('redirect status', status, 302)
    check('destination UTM wins', headers.get('Location'), 'https://example.com/track?utm_source=existing&utm_campaign=functional')
    api('/functional-new?utm_campaign=functional', browser=False)
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        stats = api(f"/api/v1/links/{created['id']}/stats?days=7")[1]
        if stats.get('clicks') == 1 and stats.get('bot_clicks') == 1:
            break
        time.sleep(.2)
    check('human/bot classification', [stats['clicks'], stats['bot_clicks']], [1, 1])
    check('daily chart zero filling', len(stats['daily']), 7)
    check('preview does not add events', int(sql(f"SELECT count(*) FROM clicks WHERE link_id={created['id']};")), 2)

    # No public expires_at input exists; exercise persisted expiry through a fixture.
    sql("INSERT INTO links (user_id,slug,target_url,expires_at) VALUES (1,'expiring','https://example.com/expiry',now()+interval '2 seconds');")
    check('link before expiry', api('/expiring')[0], 302)
    time.sleep(2.1)
    check('cached link respects expiry', api('/expiring')[0], 404)
    # Anonymous sequential quota; concurrent exact enforcement is not claimed.
    codes = [create({'target_url': 'https://example.com/quota'}, 0)[0] for _ in range(20)]
    check('anonymous quota after initial anonymous link', [codes.count(201), codes.count(429)], [19, 1])
    return checks
