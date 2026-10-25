import execution from 'k6/execution';
import http from 'k6/http';
import { check, sleep } from 'k6';
import { baseURL, browser, createLink, json, number, params, request, requireSession } from './lib/helpers.js';

const count = number('REQUESTS', 100);
export const options = {
  scenarios: { clicks: { executor: 'shared-iterations', vus: number('VUS', 5), iterations: count, maxDuration: '5m' } },
  thresholds: { checks: ['rate==1'], http_req_failed: ['rate==0'], iterations: [`count==${count}`] },
};

export function setup() {
  requireSession();
  return createLink(`https://example.com/k6/clicks/${Date.now()}`);
}

export default function (link) {
  const bot = execution.scenario.iterationInTest % 10 === 0;
  const options = params(bot ? 'bot' : 'human', 302, '');
  options.headers['User-Agent'] = bot ? 'curl/8.0' : browser;
  const response = http.get(`${baseURL}/${link.slug}`, options);
  check(response, {
    'click redirects correctly': r => r.status === 302 && r.headers.Location === link.target_url,
  });
}

export function teardown(link) {
  const bots = Math.ceil(count / 10);
  let stats;
  for (let attempt = 0; attempt < 100; attempt++) {
    stats = json(request('GET', `/api/v1/links/${link.id}/stats?days=1`, null, 'stored_clicks'));
    if (stats?.clicks === count - bots && stats?.bot_clicks === bots) break;
    sleep(0.2);
  }
  check(stats, {
    'every human click stored exactly once': b => b?.clicks === count - bots,
    'every bot click stored exactly once': b => b?.bot_clicks === bots,
  });
  console.log(`Click check: link ${link.id}; expected ${count - bots} human + ${bots} bot clicks.`);
}
