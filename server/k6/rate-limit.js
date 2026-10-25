import { check, fail } from 'k6';
import { json, request } from './lib/helpers.js';

// Run on a fresh local stack: the quota is per IP, shared with anonymous smoke requests.
export const options = {
  vus: 1, iterations: 1,
  thresholds: { checks: ['rate==1'], http_req_failed: ['rate==0'] },
};

export default function () {
  for (let i = 0; i < 21; i++) {
    const status = i < 20 ? 201 : 429;
    const response = request('POST', '/api/v1/links', {
      target_url: `https://example.com/k6/quota/${Date.now()}/${i}`,
    }, 'anonymous_create', status, '');
    if (!check(response, { [i < 20 ? 'first 20 anonymous links allowed' : '21st anonymous link rejected']: r => r.status === status })) {
      fail('Unexpected quota result. Run this test on a fresh local k6 stack with no other anonymous requests.');
    }
    if (status === 201) check(json(response), { 'anonymous link includes claim token': body => !!body?.claim_token });
  }
}
