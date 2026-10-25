import { fail } from 'k6';
import { arrival, json, loadOptions, number, prepareLinks, request, requireSession } from './lib/helpers.js';
import { stats } from './lib/workloads.js';

export const options = loadOptions({ stats: arrival('default', number('RATE', 10)) });
export function setup() {
  requireSession();
  if (__ENV.LINK_ID) {
    const id = number('LINK_ID', 1);
    const response = request('GET', `/api/v1/links/${id}/stats`, null, 'stats_setup');
    if (response.status !== 200 || json(response)?.link_id !== id) fail('LINK_ID must belong to the test account.');
    return [{ id }];
  }
  if (__ENV.SLUG) fail('Use LINK_ID for analytics tests, or omit it to create test links.');
  return prepareLinks();
}
export default function (links) { stats(links); }
