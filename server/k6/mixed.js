import { fail } from 'k6';
import { arrival, loadOptions, number, prepareLinks } from './lib/helpers.js';
import { create, redirect, stats } from './lib/workloads.js';

const scenarios = {};
for (const [name, rate] of [
  ['redirects', number('REDIRECT_RATE', 100, 0)],
  ['creates', number('CREATE_RATE', 10, 0)],
  ['reports', number('STATS_RATE', 5, 0)],
]) {
  if (rate > 0) scenarios[name] = arrival(name, rate);
}
if (!Object.keys(scenarios).length) throw new Error('Enable at least one traffic rate.');
export const options = loadOptions(scenarios);
export function setup() {
  if (__ENV.SLUG) fail('Mixed tests create their own links; use redirects.js to test an existing SLUG.');
  return prepareLinks();
}
export function redirects(links) { redirect(links); }
export function creates() { create(); }
export function reports(links) { stats(links); }
