#!/usr/bin/env node
// @module-feature ui
// Browser smoke test for the pages `lattice serve` renders, with Playwright.
//
//   node scripts/pages-smoke.js <base-url> <path>... [--shots <dir>]
//
// It opens each path under base-url in Chromium with every request to another
// origin blocked, and fails on a blocked request, a console error, an
// unstyled page (the layout's max-w-7xl comes from tailwind.css) or a
// missing brand font. A page with a .mermaid block must draw it as an SVG.
// --shots writes a PNG of each page.
// It needs `require('playwright')` to resolve; it never installs browsers.
'use strict';
const fs = require('fs');
const path = require('path');
const { chromium } = require('playwright');

const args = process.argv.slice(2);
const shotsAt = args.indexOf('--shots');
const shots = shotsAt >= 0 ? args.splice(shotsAt, 2)[1] : '';
const [base, ...paths] = args;
if (!base || paths.length === 0) { console.error('usage: pages-smoke.js <base-url> <path>... [--shots dir]'); process.exit(2); }
const origin = new URL(base).origin;

let failed = 0;
function check(ok, what) { if (ok) console.log('ok   ' + what); else { failed++; console.log('FAIL ' + what); } }

async function visit(browser, p) {
  const page = await browser.newPage({ viewport: { width: 1280, height: 800 } });
  const errors = [], offsite = [];
  page.on('console', m => { if (m.type() === 'error') errors.push(m.text()); });
  page.on('pageerror', e => errors.push('uncaught: ' + e.message));
  await page.route('**/*', r => {
    if (new URL(r.request().url()).origin === origin) return r.continue();
    offsite.push(r.request().url());
    return r.abort('blockedbyclient');
  });
  const resp = await page.goto(origin + p, { waitUntil: 'load' });
  check(resp && resp.status() === 200, `${p}: status ${resp && resp.status()}`);
  if (await page.$('.mermaid')) {
    const drawn = await page.waitForSelector('.mermaid svg', { timeout: 15000 }).then(() => true, () => false);
    check(drawn, `${p}: mermaid draws the diagram`);
  }
  const look = await page.evaluate(async () => {
    await document.fonts.ready;
    return {
      bg: getComputedStyle(document.body).backgroundColor,
      maxWidth: getComputedStyle(document.querySelector('main') || document.body).maxWidth,
      loaded: [...document.fonts].filter(f => f.status === 'loaded').map(f => f.family.replace(/"/g, '')),
    };
  });
  check(look.bg === 'rgb(15, 18, 24)' && look.maxWidth === '1280px', `${p}: styled (background ${look.bg}, main max-width ${look.maxWidth})`);
  check(look.loaded.includes('Geist'), `${p}: Geist loaded from the server (${look.loaded.join(', ') || 'no font'})`);
  check(offsite.length === 0, `${p}: no request to another origin${offsite.length ? ': ' + offsite.join(', ') : ''}`);
  check(errors.length === 0, `${p}: no console error${errors.length ? ': ' + errors.join(' | ') : ''}`);
  if (shots) await page.screenshot({ path: path.join(shots, (p.replace(/[^a-z0-9]+/gi, '-').replace(/^-|-$/g, '') || 'overview') + '.png'), fullPage: true });
  await page.close();
}

(async () => {
  if (shots) fs.mkdirSync(shots, { recursive: true });
  const browser = await chromium.launch();
  try {
    for (const p of paths) await visit(browser, p);
  } finally {
    await browser.close();
  }
  console.log(failed ? `${failed} check(s) failed` : 'all checks passed');
  process.exit(failed ? 1 : 0);
})().catch(e => { console.error(e); process.exit(1); });
