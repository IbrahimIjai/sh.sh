import http from 'k6/http';
import exec from 'k6/execution';
import { Counter, Rate, Trend } from 'k6/metrics';

const rate = Number(__ENV.RATE || 100);
const duration = __ENV.DURATION || '30s';
const run = __ENV.RUN_ID || 'manual';
const links = Number(__ENV.LINKS || 1000);
const statsRate = Number(__ENV.STATS_RATE || 0);
const redirects = new Counter('successful_redirects');
const humans = new Counter('human_redirects');
const bots = new Counter('bot_redirects');
const correct = new Rate('correct_redirect');
const latency = new Trend('redirect_latency', true);
const statsLatency = new Trend('stats_latency', true);
const statsOK = new Rate('stats_ok');

const scenarios = {
  redirects: {
    executor: 'constant-arrival-rate', exec: 'redirect', rate, timeUnit: '1s',
    duration, preAllocatedVUs: Number(__ENV.VUS || 200), gracefulStop: '10s',
  },
};
if (statsRate > 0) {
  scenarios.stats = {
    executor: 'constant-arrival-rate', exec: 'stats', rate: statsRate,
    timeUnit: '1s', duration, preAllocatedVUs: 30, gracefulStop: '10s',
  };
}

export const options = {
  scenarios, discardResponseBodies: true,
  summaryTrendStats: ['avg', 'med', 'p(95)', 'p(99)', 'max'],
  thresholds: {
    correct_redirect: ['rate==1'],
    dropped_iterations: ['count==0'],
    redirect_latency: ['p(95)<100', {threshold: 'p(99)<2000', abortOnFail: true, delayAbortEval: '15s'}],
    http_req_failed: [{threshold: 'rate<0.01', abortOnFail: true, delayAbortEval: '15s'}],
    ...(statsRate > 0 ? {stats_ok: ['rate==1'], stats_latency: ['p(95)<500']} : {}),
  },
};

export function redirect() {
  const i = exec.scenario.iterationInTest;
  const id = 1 + (i % links);
  const bot = i % 10 === 0;
  const ua = bot ? 'curl/8.0' :
    `Mozilla/5.0 (Macintosh; Intel Mac OS X 14.5; rv:127.0) Gecko/20100101 Firefox/127.0 Visitor/${i % 1000}`;
  const response = http.get(`http://api:8080/bench-${id}?utm_source=loadtest&utm_campaign=${run}`, {
    redirects: 0, timeout: '5s',
    headers: {'User-Agent': ua, Referer: 'https://t.co/loadtest'},
    tags: {name: 'redirect'},
  });
  latency.add(response.timings.duration);
  const ok = response.status === 302 && response.headers.Location ===
    `https://example.com/benchmark/${id}?utm_campaign=${run}&utm_source=loadtest`;
  correct.add(ok);
  if (ok) {
    redirects.add(1);
    if (bot) bots.add(1); else humans.add(1);
  }
}

export function stats() {
  const id = 1 + exec.scenario.iterationInTest % links;
  const response = http.get(`http://api:8080/api/v1/links/${id}/stats?days=7`, {
    redirects: 0, timeout: '5s', responseType: 'text',
    headers: {Cookie: 'limen_session=local-load-test-session'},
    tags: {name: 'stats'},
  });
  statsLatency.add(response.timings.duration);
  let valid = false;
  try { valid = response.status === 200 && response.json('link_id') === id; } catch (_) {}
  statsOK.add(valid);
}

export function handleSummary(data) {
  return {
    [`/results/${run}.json`]: JSON.stringify(data, null, 2),
    stdout: JSON.stringify({run, metrics: data.metrics}) + '\n',
  };
}
