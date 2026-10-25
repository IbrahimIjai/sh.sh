import { arrival, loadOptions, number, requireSession } from './lib/helpers.js';
import { create } from './lib/workloads.js';

export const options = loadOptions({ create: arrival('default', number('RATE', 10)) });
export function setup() { requireSession(); }
export default function () { create(); }
