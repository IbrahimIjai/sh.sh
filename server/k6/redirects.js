import { arrival, loadOptions, number, prepareLinks } from './lib/helpers.js';
import { redirect } from './lib/workloads.js';

export const options = loadOptions({ redirects: arrival('default', number('RATE', 100)) });
export const setup = prepareLinks;
export default function (links) { redirect(links); }
