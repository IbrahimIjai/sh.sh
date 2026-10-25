import { check } from 'k6';
import { arrival, loadOptions, number, request } from './lib/helpers.js';

export const options = loadOptions({ health: arrival('default', number('RATE', 5)) });

export default function () {
  check(request('GET', '/health', null, 'health', 200, ''), { 'API is alive': r => r.status === 200 });
  check(request('GET', '/health/ready', null, 'ready', 200, ''), { 'database is ready': r => r.status === 200 });
}
