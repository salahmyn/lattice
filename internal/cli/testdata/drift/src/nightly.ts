// @module-feature shop

import { sendDigest } from './digest.ts';

/** A scheduled job declared as a coverage root: it calls the digest, and no flow names the digest. */
export function nightlyJob(): string {
  return sendDigest(0);
}
