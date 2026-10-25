import http from 'k6/http';
import execution from 'k6/execution';
import { check } from 'k6';
import { Counter } from 'k6/metrics';
import { baseURL, browser, createLink, json, number, params, request } from './helpers.js';

const successfulRedirects = new Counter('successful_redirects');
const humanRedirects = new Counter('human_redirects');
const botRedirects = new Counter('bot_redirects');

export function redirect(links) {
  const i = execution.scenario.iterationInTest;
  const link = links[i % links.length];
  // Rotate the bot flag independently of the link, so each link receives both kinds.
  const bot = Math.floor(i / links.length) % 10 === 0;
  const options = params('redirect', 302, '');
  options.responseType = 'none';
  options.headers['User-Agent'] = bot ? 'curl/8.0' : browser;
  const response = http.get(`${baseURL}/${link.slug}`, options);
  const valid = check(response, {
    'redirect points to the right destination': r => r.status === 302 && r.headers.Location === link.target_url,
  });
  if (valid) {
    successfulRedirects.add(1);
    if (bot) botRedirects.add(1); else humanRedirects.add(1);
  }
}

export function create() {
  const target = `https://example.com/k6/create/${Date.now()}/${execution.vu.idInTest}/${execution.scenario.iterationInTest}`;
  const link = createLink(target);
  const preview = request('GET', `/${link.slug}+`, null, 'preview');
  check(preview, { 'new link is immediately visible': r => r.status === 200 && r.body.includes(target) });
}

export function stats(links) {
  const link = links[execution.scenario.iterationInTest % links.length];
  const days = number('DAYS', 30);
  if (days > 365) throw new Error('DAYS must be <= 365');
  const response = request('GET', `/api/v1/links/${link.id}/stats?days=${days}`, null, 'stats');
  const body = json(response);
  check(response, {
    'analytics have the requested link and date range': r => r.status === 200 && body &&
      body.link_id === link.id && body.days === days && Array.isArray(body.daily) &&
      body.daily.length === days && body.clicks >= 0 && body.bot_clicks >= 0 &&
      body.unique_visitors >= 0 && body.breakdown && Array.isArray(body.breakdown.sources),
  });
}
