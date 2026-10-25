import http from 'k6/http';
import { check, fail, sleep } from 'k6';
import { baseURL, browser, createLink, json, params, request, requireSession, session } from './lib/helpers.js';

export const options = {
  vus: 1, iterations: 1,
  thresholds: { checks: ['rate==1'], http_req_failed: ['rate==0'] },
};

// Expected failures (401, 404, etc.) are checked explicitly and aren't HTTP test errors.
function expect(method, path, body, status, label, cookie = session) {
  const response = request(method, path, body, label, status, cookie);
  if (!check(response, { [label]: r => r.status === status })) {
    fail(`${label}: expected HTTP ${status}, got ${response.status}`);
  }
  return json(response);
}

export default function () {
  requireSession();
  const other = __ENV.OTHER_SESSION_COOKIE;
  if (!other || other === session) fail('Set OTHER_SESSION_COOKIE to a different test account (provided by local Compose).');
  expect('GET', '/api/v1/links', null, 200, 'second account is signed in', other);
  const run = `k6-${Date.now()}`;
  expect('GET', '/health', null, 200, 'API is alive', '');
  expect('GET', '/health/ready', null, 200, 'database is ready', '');
  expect('GET', '/api/v1/links', null, 401, 'listing requires a session', '');
  expect('POST', '/api/v1/links', { target_url: 'javascript:alert(1)' }, 400, 'unsafe URL rejected');
  expect('POST', '/api/v1/links', { target_url: 'https://bit.ly/test' }, 400, 'another shortener rejected');
  expect('POST', '/api/v1/links', { target_url: 'https://example.com', slug: 'health' }, 400, 'reserved slug rejected');
  expect('POST', '/api/v1/links', { target_url: 'https://example.com', slug: run }, 401, 'anonymous custom slug rejected', '');

  const anon = expect('POST', '/api/v1/links', { target_url: 'https://example.com/anonymous' }, 201, 'anonymous link created', '');
  check(anon, {
    'anonymous link has a claim token': b => typeof b.claim_token === 'string' && b.claim_token.length >= 40,
    'private fields stay private': b => !('user_id' in b) && !('claim_token_hash' in b) && !('creator_hash' in b),
  });
  const tokens = { claim_tokens: [anon.claim_token] };
  const lookup = expect('POST', '/api/v1/links/lookup', tokens, 200, 'anonymous link can be looked up', '');
  check(lookup, { 'lookup returns the created link': b => b.length === 1 && b[0].id === anon.id });
  expect('POST', '/api/v1/links/claim', tokens, 401, 'claim requires a session', '');
  const claimed = expect('POST', '/api/v1/links/claim', tokens, 200, 'anonymous link claimed');
  check(claimed, { 'claim keeps link identity': b => b.claimed?.[0]?.id === anon.id });
  const spent = expect('POST', '/api/v1/links/lookup', tokens, 200, 'spent token lookup', '');
  check(spent, { 'claim token is consumed': b => b.length === 0 });
  const listed = expect('GET', '/api/v1/links', null, 200, 'links listed');
  check(listed, { 'claimed link is owned': b => b.some(link => link.id === anon.id) });
  expect('GET', `/api/v1/links/${anon.id}/stats`, null, 404, 'other account cannot read analytics', other);
  expect('DELETE', `/api/v1/links/${anon.id}`, null, 404, 'other account cannot delete link', other);

  expect('GET', `/${run}`, null, 404, 'missing slug returns 404', '');
  const link = createLink('https://example.com/track?utm_source=existing', run);
  check(link, { 'signed-in link has no claim token': b => !('claim_token' in b) });
  const preview = request('GET', `/${run}+`, null, 'preview', 200, '');
  check(preview, { 'preview shows destination': r => r.status === 200 && r.body.includes('example.com/track') });
  const humanParams = params('human_redirect', 302, '');
  humanParams.headers['User-Agent'] = browser;
  humanParams.headers.Referer = 'https://t.co/test';
  const human = http.get(`${baseURL}/${run}?utm_source=incoming&utm_campaign=smoke`, humanParams);
  check(human, {
    'new link redirects and destination UTM wins': r => r.status === 302 &&
      r.headers.Location === 'https://example.com/track?utm_source=existing&utm_campaign=smoke',
  });
  const botParams = params('bot_redirect', 302, '');
  botParams.headers['User-Agent'] = 'curl/8.0';
  check(http.get(`${baseURL}/${run}`, botParams), { 'bots still redirect': r => r.status === 302 });

  // Click persistence is asynchronous. Poll instead of relying on one fixed sleep.
  let stats;
  for (let attempt = 0; attempt < 50; attempt++) {
    stats = json(request('GET', `/api/v1/links/${link.id}/stats?days=7`, null, 'stats'));
    if (stats?.clicks === 1 && stats?.bot_clicks === 1) break;
    sleep(0.2);
  }
  check(stats, {
    'human and bot clicks are stored once; preview adds none': b => b?.clicks === 1 && b?.bot_clicks === 1,
    'daily chart has seven days': b => b?.daily?.length === 7,
    'visitor counted': b => b?.unique_visitors === 1,
    'referrer classified': b => b?.breakdown?.sources?.some(row => row.name === 'X (Twitter)' && row.clicks === 1),
  });
  expect('GET', `/api/v1/links/${link.id}/stats?days=0`, null, 400, 'invalid date range rejected');

  const renamed = expect('PATCH', `/api/v1/links/${link.id}`, { slug: `${run}-renamed` }, 200, 'slug renamed');
  check(renamed, { 'old slug appears in history': b => b.previous_slugs.includes(run) });
  expect('GET', `/${run}`, null, 302, 'old slug still redirects', '');
  expect('GET', `/${run}-renamed`, null, 302, 'new slug redirects', '');
  expect('DELETE', `/api/v1/links/${link.id}`, null, 204, 'link deleted');
  expect('GET', `/${run}`, null, 404, 'deleted cached old slug unavailable', '');
  expect('GET', `/${run}-renamed`, null, 404, 'deleted cached new slug unavailable', '');
  expect('POST', '/api/v1/links', { target_url: 'https://example.com/new', slug: run }, 409, 'deleted slug cannot be reused');

  const collisionBody = JSON.stringify({ target_url: 'https://example.com/collision', slug: `${run}-race` });
  const raceParams = params('concurrent_create', 201);
  raceParams.responseCallback = http.expectedStatuses(201, 409);
  const responses = http.batch(Array.from({ length: 10 }, () =>
    ['POST', `${baseURL}/api/v1/links`, collisionBody, raceParams]));
  check(responses, {
    'concurrent custom slug has one winner': rows => rows.filter(r => r.status === 201).length === 1,
    'remaining requests get conflicts': rows => rows.filter(r => r.status === 409).length === 9,
  });
  for (const response of responses) {
    if (response.status === 201) expect('DELETE', `/api/v1/links/${json(response).id}`, null, 204, 'collision link deleted');
  }

  const domain = expect('POST', '/api/v1/domains', { domain: `${run}.example.com` }, 201, 'domain created');
  const domains = expect('GET', '/api/v1/domains', null, 200, 'domains listed');
  check(domains, { 'created domain is listed': rows => rows.some(row => row.id === domain.id) });
  expect('DELETE', `/api/v1/domains/${domain.id}`, null, 204, 'domain deleted');

  if (__ENV.ADMIN_TOKEN) {
    const blocked = createLink('https://example.com/blocked');
    expect('GET', `/${blocked.slug}`, null, 302, 'takedown link cached', '');
    const adminParams = params('takedown', 200, '');
    adminParams.headers.Authorization = `Bearer ${__ENV.ADMIN_TOKEN}`;
    check(http.post(`${baseURL}/api/v1/admin/takedown`, JSON.stringify({ slugs: [blocked.slug] }), adminParams),
      { 'admin takedown succeeds': r => r.status === 200 });
    expect('GET', `/${blocked.slug}`, null, 404, 'takedown invalidates cache', '');
  }
  expect('DELETE', `/api/v1/links/${anon.id}`, null, 204, 'claimed test link deleted');
}
