import http from 'k6/http';
import { check, fail } from 'k6';

export const baseURL = (__ENV.BASE_URL || 'http://localhost:18080').replace(/\/$/, '');
export const session = __ENV.SESSION_COOKIE || '';
export const browser = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 14.5; rv:127.0) Gecko/20100101 Firefox/127.0';

export function number(name, fallback, minimum = 1) {
  const value = Number(__ENV[name] === undefined ? fallback : __ENV[name]);
  if (!Number.isInteger(value) || value < minimum) {
    throw new Error(`${name} must be an integer >= ${minimum}`);
  }
  return value;
}

export function requireSession() {
  if (!session) fail('Set SESSION_COOKIE to a test account cookie, or use the local k6 Compose stack.');
  const response = request('GET', '/api/v1/links', null, 'session', 200);
  if (response.status !== 200) fail('Test session is invalid or expired; rerun seed.sql for the local stack.');
}

export function params(name, expected = 200, cookie = session) {
  return {
    redirects: 0,
    timeout: '5s',
    responseType: 'text',
    headers: { 'Content-Type': 'application/json', Cookie: cookie },
    tags: { name },
    responseCallback: http.expectedStatuses(expected),
  };
}

export function request(method, path, body, name, expected = 200, cookie = session) {
  return http.request(method, baseURL + path, body === null ? null : JSON.stringify(body),
    params(name, expected, cookie));
}

export function json(response) {
  try { return response.json(); } catch (_) { return null; }
}

export function createLink(target, slug) {
  const response = request('POST', '/api/v1/links', { target_url: target, ...(slug ? { slug } : {}) }, 'create', 201);
  const link = json(response);
  const valid = check(response, {
    'link created with correct target': r => r.status === 201 && link && link.id > 0 &&
      typeof link.slug === 'string' && link.target_url === target,
  });
  if (!valid) fail(`Link creation failed (HTTP ${response.status})`);
  return link;
}

export function prepareLinks() {
  if (__ENV.SLUG) {
    if (!__ENV.TARGET) fail('Set TARGET to the exact destination of SLUG.');
    return [{ slug: __ENV.SLUG, target_url: __ENV.TARGET }];
  }
  requireSession();
  const run = Date.now();
  return Array.from({ length: number('LINKS', 10) }, (_, i) =>
    createLink(`https://example.com/k6/${run}/${i}`));
}

export function arrival(exec, rate) {
  const vus = number('VUS', 20);
  const maxVUs = number('MAX_VUS', Math.max(vus, 100));
  if (maxVUs < vus) throw new Error('MAX_VUS must be >= VUS');
  return {
    executor: 'constant-arrival-rate', exec, rate, timeUnit: '1s',
    duration: __ENV.DURATION || '30s', preAllocatedVUs: vus, maxVUs, gracefulStop: '10s',
  };
}

export function loadOptions(scenarios) {
  return {
    scenarios,
    discardResponseBodies: true,
    // Stable request names keep generated slugs and URLs out of metric labels.
    systemTags: ['status', 'method', 'name', 'scenario', 'expected_response'],
    summaryTrendStats: ['avg', 'med', 'p(95)', 'p(99)', 'max'],
    thresholds: {
      checks: ['rate==1'],
      http_req_failed: ['rate<0.01'],
      dropped_iterations: ['count==0'],
      ...Object.fromEntries(Object.keys(scenarios).map(name =>
        [`http_req_duration{scenario:${name}}`, [`p(95)<${number('P95_MS', 500)}`]])),
    },
  };
}
