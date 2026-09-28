// Mixed workload for the article's creation/redirect requirements. Never follows destinations.
import http from 'k6/http';
import exec from 'k6/execution';
import {Counter, Rate, Trend} from 'k6/metrics';

const run = __ENV.RUN_ID;
const links = Number(__ENV.LINKS || 1000);
const duration = `${__ENV.DURATION || 30}s`;
const redirectRate = Number(__ENV.REDIRECT_RATE || 0);
const createRate = Number(__ENV.CREATE_RATE || 0);
const statsRate = Number(__ENV.STATS_RATE || 0);
const shape = __ENV.SHAPE || 'uniform';
const cookie = {Cookie: 'limen_session=article-session-1'};
const redirects = new Counter('successful_redirects');
const humans = new Counter('human_redirects');
const bots = new Counter('bot_redirects');
const creates = new Counter('successful_creates');
const reports = new Counter('successful_reports');
const wrong = new Counter('incorrect_responses');
const redirectOK = new Rate('redirect_ok');
const createOK = new Rate('create_ok');
const reportOK = new Rate('report_ok');
const redirectLatency = new Trend('redirect_latency', true);
const createLatency = new Trend('create_latency', true);
const reportLatency = new Trend('report_latency', true);
const visibility = new Rate('new_link_visible');
const scenarios = {};
for (const [name, rate, vus] of [
  ['redirect', redirectRate, Number(__ENV.VUS || 400)],
  ['create', createRate, Number(__ENV.CREATE_VUS || 100)],
  ['report', statsRate, Number(__ENV.STATS_VUS || 50)],
]) {
  if (rate > 0) scenarios[name] = {executor: 'constant-arrival-rate', exec: name,
    rate, timeUnit: '1s', duration, preAllocatedVUs: vus, gracefulStop: '10s'};
}
export const options = {
  scenarios, discardResponseBodies: true,
  summaryTrendStats: ['avg', 'med', 'p(95)', 'p(99)', 'max'],
  thresholds: {
    http_req_failed: [{threshold: 'rate<0.01', abortOnFail: true, delayAbortEval: '15s'}],
    dropped_iterations: ['count==0'],
    ...Object.fromEntries(Object.keys(scenarios).map(name =>
      [`dropped_iterations{scenario:${name}}`, ['count==0']])),
    ...(redirectRate > 0 ? {redirect_ok: ['rate==1'],
      redirect_latency: ['med<5', 'p(99)<50',
        {threshold: 'p(99)<2000', abortOnFail: true, delayAbortEval: '15s'}]} : {}),
    ...(createRate > 0 ? {create_ok: ['rate==1'], new_link_visible: ['rate==1']} : {}),
    ...(statsRate > 0 ? {report_ok: ['rate==1'], report_latency: ['p(95)<500']} : {}),
  },
};

export function redirect() {
  const i = exec.scenario.iterationInTest;
  // Independent visitor/bot mix per link across successive rotations.
  const bot = (Math.floor(i / links) + i) % 10 === 0;
  const id = shape === 'hot' ? 1 : shape === 'skewed' && i % 5 !== 0
    ? 1 + Math.floor(i / 5) % Math.min(100, links) : 1 + i % links;
  const ua = bot ? 'curl/8.0' :
    `Mozilla/5.0 (Macintosh; Intel Mac OS X 14.5; rv:127.0) Gecko/20100101 Firefox/127.0 Visitor/${Math.floor(i / links) % 1000}`;
  const r = http.get(`http://api:8080/bench-${id}?utm_source=loadtest&utm_campaign=${run}`, {
    redirects: 0, timeout: '5s', headers: {'User-Agent': ua}, tags: {name: 'redirect'},
  });
  redirectLatency.add(r.timings.duration);
  const ok = r.status === 302 && r.headers.Location ===
    `https://example.com/benchmark/${id}?utm_campaign=${run}&utm_source=loadtest`;
  redirectOK.add(ok);
  if (ok) { redirects.add(1); if (bot) bots.add(1); else humans.add(1); }
  else wrong.add(1);
}

export function create() {
  const i = exec.scenario.iterationInTest;
  const target = `https://example.com/article/${run}/${i}`;
  const r = http.post('http://api:8080/api/v1/links', JSON.stringify({target_url: target}), {
    redirects: 0, timeout: '5s', responseType: 'text',
    headers: {...cookie, 'Content-Type': 'application/json'}, tags: {name: 'create'},
  });
  createLatency.add(r.timings.duration);
  let body;
  try { body = r.json(); } catch (_) { body = {}; }
  const ok = r.status === 201 && body.target_url === target && /^[a-zA-Z0-9]{7}$/.test(body.slug || '');
  createOK.add(ok);
  if (!ok) { wrong.add(1); return; }
  creates.add(1);
  // Preview checks immediate read visibility without adding another analytics event.
  // These extra reads are measured separately, and disclosed in the report.
  const check = http.get(`http://api:8080/${body.slug}+`, {
    redirects: 0, timeout: '5s', responseType: 'text', tags: {name: 'creation_visibility'},
  });
  const visible = check.status === 200 && check.body.includes(target);
  visibility.add(visible);
  if (!visible) wrong.add(1);
}

export function report() {
  const id = 1 + exec.scenario.iterationInTest % links;
  const r = http.get(`http://api:8080/api/v1/links/${id}/stats?days=30`, {
    redirects: 0, timeout: '5s', responseType: 'text', headers: cookie, tags: {name: 'report'},
  });
  reportLatency.add(r.timings.duration);
  let ok = false;
  try { ok = r.status === 200 && r.json('link_id') === id; } catch (_) {}
  reportOK.add(ok);
  if (ok) reports.add(1); else wrong.add(1);
}

export function handleSummary(data) {
  return {[`/results/${run}.json`]: JSON.stringify(data, null, 2)};
}
