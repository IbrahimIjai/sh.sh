import http from 'k6/http';
import exec from 'k6/execution';
import { SharedArray } from 'k6/data';
import { Counter, Rate, Trend } from 'k6/metrics';

const fixtures = new SharedArray('dashboard accounts', () => JSON.parse(open('/results/dashboards.json')));
const active = Number(__ENV.ACTIVE_DASHBOARDS);
const refresh = Number(__ENV.REFRESH_SECONDS || 30);
const duration = __ENV.DURATION || '60s';
const run = __ENV.RUN_ID;
const links = Number(__ENV.LINKS);
const linksPerUser = Number(__ENV.LINKS_PER_USER || 5);
const popular = Number(__ENV.POPULAR_LINKS || 100);
const burst = Number(__ENV.BURST_REQUESTS || 0);
const redirectCount = new Counter('successful_redirects');
const humanCount = new Counter('human_redirects');
const botCount = new Counter('bot_redirects');
const redirectAttempts = new Counter('redirect_attempts');
const statsAttempts = new Counter('stats_attempts');
const statsCount = new Counter('successful_stats');
const burstAttempts = new Counter('burst_attempts');
const burstCount = new Counter('successful_burst');
const correctRedirect = new Rate('correct_redirect');
const correctStats = new Rate('correct_stats');
const correctBurst = new Rate('correct_burst');
const redirectLatency = new Trend('redirect_latency', true);
const statsLatency = new Trend('stats_latency', true);
const burstLatency = new Trend('burst_latency', true);
const largeLatency = new Trend('large_link_stats_latency', true);
const popularLatency = new Trend('popular_link_stats_latency', true);
const standardLatency = new Trend('standard_link_stats_latency', true);

const scenarios = {
  redirects: {
    executor: 'constant-arrival-rate', exec: 'redirect', rate: Number(__ENV.REDIRECT_RATE || 1000),
    timeUnit: '1s', duration, preAllocatedVUs: Number(__ENV.REDIRECT_VUS || 250), gracefulStop: '10s',
  },
  dashboards: {
    executor: 'constant-arrival-rate', exec: 'dashboard', rate: active,
    timeUnit: `${refresh}s`, duration, preAllocatedVUs: Number(__ENV.STATS_VUS || 400), gracefulStop: '10s',
  },
};
if (burst > 0) {
  scenarios.report_burst = {
    executor: 'constant-arrival-rate', exec: 'reportBurst', rate: burst,
    timeUnit: '2s', duration: '2s', startTime: '10s',
    preAllocatedVUs: Number(__ENV.BURST_VUS || 600), gracefulStop: '10s',
  };
}

export const options = {
  scenarios, discardResponseBodies: true,
  summaryTrendStats: ['avg', 'med', 'p(95)', 'p(99)', 'max'],
  thresholds: {
    correct_redirect: ['rate==1'], correct_stats: ['rate==1'],
    dropped_iterations: ['count==0'],
    'dropped_iterations{scenario:redirects}': ['count==0'],
    'dropped_iterations{scenario:dashboards}': ['count==0'],
    redirect_latency: ['med<5', 'p(99)<50', {threshold: 'p(99)<2000', abortOnFail: true, delayAbortEval: '30s'}],
    stats_latency: ['p(95)<500'],
    http_req_failed: [{threshold: 'rate<0.01', abortOnFail: true, delayAbortEval: '30s'}],
    ...(burst ? {
      correct_burst: ['rate==1'], burst_latency: ['p(95)<500'],
      'dropped_iterations{scenario:report_burst}': ['count==0'],
    } : {}),
  },
};

export function redirect() {
  const i = exec.scenario.iterationInTest;
  // 80% of traffic goes to the first link of the 100 popular accounts.
  // A separate sequence varies bot status so it is not tied to a link's ID.
  const id = i % 5 !== 0 ? 1 + (Math.floor(i / 5) % popular) * linksPerUser :
    1 + ((Math.floor(i / 5) * 7919) % links);
  const bot = Math.floor(i / 7) % 10 === 0;
  const ua = bot ? 'curl/8.0' :
    `Mozilla/5.0 (Macintosh; Intel Mac OS X 14.5; rv:127.0) Gecko/20100101 Firefox/127.0 Visitor/${i % 10000}`;
  redirectAttempts.add(1);
  const response = http.get(`http://api:8080/dashboard-link-${id}?utm_source=loadtest&utm_campaign=${run}`, {
    redirects: 0, timeout: '5s', headers: {'User-Agent': ua, Referer: 'https://t.co/loadtest'},
    tags: {name: 'redirect'},
  });
  redirectLatency.add(response.timings.duration);
  const ok = response.status === 302 && response.headers.Location ===
    `https://example.com/dashboard/${id}?utm_campaign=${run}&utm_source=loadtest`;
  correctRedirect.add(ok);
  if (ok) {
    redirectCount.add(1);
    if (bot) botCount.add(1); else humanCount.add(1);
  }
}

function statsRequest(isBurst) {
  const i = exec.scenario.iterationInTest;
  // Always exercise the million-click link once in the separate filter burst.
  const fixture = isBurst && i === 0 ?
    {link_id: 1, token: 'dashboard-session-1', tier: 'large'} : fixtures[i % active];
  const days = isBurst ? 90 : 30;
  if (isBurst) burstAttempts.add(1); else statsAttempts.add(1);
  const response = http.get(`http://api:8080/api/v1/links/${fixture.link_id}/stats?days=${days}`, {
    redirects: 0, timeout: '5s', responseType: 'text',
    headers: {Cookie: `limen_session=${fixture.token}`},
    tags: {name: isBurst ? 'report_burst' : 'dashboard_stats', tier: fixture.tier},
  });
  let valid = false;
  try {
    const body = response.json();
    valid = response.status === 200 && body.link_id === fixture.link_id && body.days === days &&
      body.daily.length === days && body.clicks >= 0 && body.bot_clicks >= 0 &&
      body.unique_visitors >= 0 && Array.isArray(body.breakdown.sources);
  } catch (_) {}
  if (isBurst) {
    burstLatency.add(response.timings.duration);
    correctBurst.add(valid);
    if (valid) burstCount.add(1);
  } else {
    statsLatency.add(response.timings.duration);
    correctStats.add(valid);
    if (valid) statsCount.add(1);
    if (fixture.tier === 'large') largeLatency.add(response.timings.duration);
    else if (fixture.tier === 'popular') popularLatency.add(response.timings.duration);
    else standardLatency.add(response.timings.duration);
  }
}

export function dashboard() { statsRequest(false); }
export function reportBurst() { statsRequest(true); }

export function handleSummary(data) {
  return {[`/results/${run}.json`]: JSON.stringify(data, null, 2), stdout: JSON.stringify({run, metrics: data.metrics}) + '\n'};
}
