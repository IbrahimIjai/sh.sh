import http from 'k6/http';
import { check } from 'k6';
import exec from 'k6/execution';
import { Counter, Rate, Trend } from 'k6/metrics';

const BASE = __ENV.BASE || 'https://shortened.cv';
const SLUG = __ENV.SLUG;
const TARGET = __ENV.TARGET;
const RATE = Number(__ENV.RATE || 50);
const WARMUP_S = Number(__ENV.WARMUP_S || 20);
const HOLD_S = Number(__ENV.HOLD_S || 60);

const holdDuration = new Trend('hold_duration', true);
const holdFailed = new Rate('hold_failed');
const holdCorrect = new Rate('hold_correct');
const holdRequests = new Counter('hold_requests');

export const options = {
  discardResponseBodies: true,
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
  scenarios: {
    redirects: {
      executor: 'ramping-arrival-rate',
      startRate: Math.ceil(RATE / 10),
      timeUnit: '1s',
      preAllocatedVUs: Number(__ENV.PRE_VUS || Math.max(50, Math.ceil(RATE / 2))),
      maxVUs: Number(__ENV.MAX_VUS || Math.max(200, RATE * 2)),
      stages: [
        { target: RATE, duration: `${WARMUP_S}s` },
        { target: RATE, duration: `${HOLD_S}s` },
      ],
    },
  },
  thresholds: {
    hold_failed: [{ threshold: 'rate<0.01', abortOnFail: true, delayAbortEval: '15s' }],
    hold_duration: [{ threshold: 'p(95)<500', abortOnFail: true, delayAbortEval: '15s' }],
    hold_correct: ['rate>0.99'],
  },
};

export default function () {
  const res = http.get(`${BASE}/${SLUG}`, {
    redirects: 0,
    headers: { 'User-Agent': 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/130.0 Safari/537.36 shortened-bench' },
  });
  const ok = check(res, { '302 to target': (r) => r.status === 302 && r.headers.Location === TARGET });
  if (Date.now() - exec.scenario.startTime >= WARMUP_S * 1000) {
    holdDuration.add(res.timings.duration);
    holdFailed.add(res.status === 0 || res.status >= 500);
    holdCorrect.add(ok);
    holdRequests.add(1);
  }
}
