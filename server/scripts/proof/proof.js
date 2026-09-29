// Article-scale workload. Requests spread over every API instance, link popularity is Zipf-like,
// and redirects are never followed. A request that can't reach an instance is retried once on
// another, as a load balancer would, and counted as a failover.
import http from 'k6/http';
import exec from 'k6/execution';
import {Counter, Rate, Trend} from 'k6/metrics';

const run = __ENV.RUN_ID;
const generator = __ENV.GENERATOR || 'gen';
const targets = (__ENV.TARGETS || 'http://127.0.0.1:18080').split(',');
const links = Number(__ENV.LINKS || 1000);
const sessions = Number(__ENV.SESSIONS || 1);
const share = Number(__ENV.SHARE || 1);
const seconds = Number(__ENV.DURATION || 60);
const window = Number(__ENV.WINDOW || 0);
const maxVUs = Number(__ENV.MAX_VUS || 3000);
const reportShape = __ENV.REPORT_SHAPE || 'uniform';
const visibilityEvery = Number(__ENV.VISIBILITY_EVERY || 100);

const redirects = new Counter('successful_redirects');
const humans = new Counter('human_redirects');
const bots = new Counter('bot_redirects');
const creates = new Counter('successful_creates');
const reports = new Counter('successful_reports');
const wrong = new Counter('incorrect_responses');
const failovers = new Counter('failovers');
const visibility = new Rate('new_link_visible_elsewhere');
const redirectLatency = new Trend('redirect_latency', true);
const createLatency = new Trend('create_latency', true);
const reportLatency = new Trend('report_latency', true);

const scenarios = {};
const thresholds = {};
for (const name of ['redirect', 'create', 'report']) {
  const rate = Math.round(Number(__ENV[`${name.toUpperCase()}_RATE`] || 0) * share);
  if (rate > 0) {
    scenarios[name] = {
      executor: 'constant-arrival-rate', exec: name, rate, timeUnit: '1s', duration: `${seconds}s`,
      preAllocatedVUs: Math.ceil(rate / 200) + 20, maxVUs: Math.min(Math.ceil(rate / 10) + 100, maxVUs),
      gracefulStop: '10s',
    };
    thresholds[`dropped_iterations{scenario:${name}}`] = ['count>=0'];
  }
}
// The always-true thresholds make k6 keep these tagged series in its summary: per-window counts
// show when errors began and ended during a fault.
for (let w = 0; window && w < Math.ceil((seconds + 15) / window); w++) {
  for (const metric of ['successful_redirects', 'incorrect_responses', 'failovers']) {
    thresholds[`${metric}{win:${w}}`] = ['count>=0'];
  }
  thresholds[`redirect_latency{win:${w}}`] = ['max>=0'];
}

export const options = {
  scenarios,
  thresholds,
  discardResponseBodies: true,
  summaryTrendStats: ['avg', 'med', 'p(95)', 'p(99)', 'max'],
};

const tags = () => (window ? {win: String(Math.floor(exec.instance.currentTestRunDuration / 1000 / window))} : {});

// P(rank k) is proportional to 1/k (Zipf, s = 1) over ranks 1..links.
const popularLink = () => Math.min(links, Math.floor(Math.exp(Math.random() * Math.log(links + 1))));

const otherThan = (i) => (i + 1 + Math.floor(Math.random() * (targets.length - 1))) % targets.length;

function send(method, path, body, params) {
  let via = Math.floor(Math.random() * targets.length);
  let r = http.request(method, targets[via] + path, body, params);
  // Only dial errors prove a POST never arrived; retrying anything else could create twice.
  const unreached = r.error_code >= 1200 && r.error_code < 1220;
  if (r.status === 0 && targets.length > 1 && (method === 'GET' || unreached)) {
    failovers.add(1, tags());
    via = otherThan(via);
    r = http.request(method, targets[via] + path, body, params);
  }
  return [r, via];
}

export function redirect() {
  const i = exec.scenario.iterationInTest;
  const id = popularLink();
  const bot = i % 10 === 0;
  const ua = bot ? 'curl/8.0' :
    `Mozilla/5.0 (Macintosh; Intel Mac OS X 14.5; rv:127.0) Gecko/20100101 Firefox/127.0 Visitor/${i % 5000}`;
  const [r] = send('GET', `/p-${id}?utm_source=proof&utm_campaign=${run}`, null, {
    redirects: 0, timeout: '5s', headers: {'User-Agent': ua}, tags: {name: 'redirect'},
  });
  redirectLatency.add(r.timings.duration, tags());
  if (r.status === 302 && r.headers.Location === `https://example.com/p/${id}?utm_campaign=${run}&utm_source=proof`) {
    redirects.add(1, tags());
    if (bot) bots.add(1); else humans.add(1);
  } else {
    wrong.add(1, tags());
  }
}

export function create() {
  const i = exec.scenario.iterationInTest;
  const url = `https://example.com/new/${run}/${generator}/${i}`;
  const [r, via] = send('POST', '/api/v1/links', JSON.stringify({target_url: url}), {
    redirects: 0, timeout: '5s', responseType: 'text', tags: {name: 'create'},
    headers: {Cookie: `limen_session=proof-session-${1 + (i % sessions)}`, 'Content-Type': 'application/json'},
  });
  createLatency.add(r.timings.duration);
  let body = {};
  try { body = r.json(); } catch (_) {}
  if (r.status !== 201 || body.target_url !== url || !/^[a-zA-Z0-9]{7}$/.test(body.slug || '')) {
    wrong.add(1, tags());
    return;
  }
  creates.add(1);
  if (i % visibilityEvery !== 0) return;
  // A new link must resolve at once on an instance other than the one that created it.
  const other = targets.length > 1 ? otherThan(via) : via;
  const check = http.get(`${targets[other]}/${body.slug}+`, {
    redirects: 0, timeout: '5s', responseType: 'text', tags: {name: 'visibility'},
  });
  // An instance that is down (a fault stage) can't answer either way.
  if (check.status !== 0) visibility.add(check.status === 200 && check.body.includes(url));
}

export function report() {
  const id = reportShape === 'zipf' ? popularLink() : 1 + Math.floor(Math.random() * links);
  const [r] = send('GET', `/api/v1/links/${id}/stats?days=30`, null, {
    redirects: 0, timeout: '10s', responseType: 'text', tags: {name: 'report'},
    headers: {Cookie: `limen_session=proof-session-${1 + ((id - 1) % sessions)}`},
  });
  reportLatency.add(r.timings.duration);
  let ok = false;
  try { ok = r.status === 200 && r.json('link_id') === id; } catch (_) {}
  if (ok) reports.add(1); else wrong.add(1, tags());
}

export function handleSummary(data) {
  return {stdout: JSON.stringify({generator, run, state: data.state, metrics: data.metrics}) + '\n'};
}
