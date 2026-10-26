#!/usr/bin/env node
// @module-feature ui
// Browser smoke test for the block map (`lattice export map`), with Playwright.
//
//   node scripts/blockmap-smoke.js <map.html> [--elk <elk.bundled.js>] [--shots <dir>] [--perf <blocks>]
//
// It loads the exported page, switches every tab and lens, hides a kind from
// the legend, uses the canvas toolbar (zoom, minimap, Mermaid text, SVG/PNG/
// Mermaid export), focuses a block, re-centres, walks the history, toggles
// system context, uses the right-click menu, filters Discover, groups a flow
// into swimlanes, walks it (keys and a fork) and follows deep links, and fails
// on any console error.
//
// The page loads ELK from a CDN. Without --elk the request is blocked and the
// page runs its offline grid fallback (that one blocked request is the only
// error ignored). With --elk the request is answered from the given file, which
// must be elkjs 0.9.3 (the page's SRI hash checks it).
//
// --shots writes PNGs of each view in light and dark. --perf N swaps in a
// generated graph of about N blocks and prints timings instead of the smoke run.
// It needs `require('playwright')` to resolve; it never installs browsers.
'use strict';
const fs = require('fs');
const path = require('path');
const { chromium } = require('playwright');

const args = process.argv.slice(2);
const file = path.resolve(args[0] || '');
const opt = name => { const i = args.indexOf('--' + name); return i > 0 ? args[i + 1] : ''; };
const elk = opt('elk'), shots = opt('shots'), perf = parseInt(opt('perf'), 10) || 0;
if (!args[0] || !fs.existsSync(file)) { console.error('usage: blockmap-smoke.js <map.html> [--elk file] [--shots dir] [--perf n]'); process.exit(2); }

const CDN = 'cdn.jsdelivr.net/npm/elkjs@0.9.3/';
let failed = 0;
function check(ok, what) { if (ok) console.log('ok   ' + what); else { failed++; console.log('FAIL ' + what); } }

async function open(browser, url, scheme) {
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 }, colorScheme: scheme || 'light' });
  const errors = [];
  page.on('console', m => {
    if (m.type() !== 'error') return;
    const loc = m.location()?.url || '';
    if (!elk && (loc.includes(CDN) || /ERR_FAILED|ERR_BLOCKED/.test(m.text()) && loc.includes('elkjs'))) return; // the blocked CDN request
    errors.push(m.text());
  });
  page.on('pageerror', e => errors.push('uncaught: ' + e.message));
  await page.route('**/' + CDN + '**', r => elk
    ? r.fulfill({ body: fs.readFileSync(elk), contentType: 'application/javascript', headers: { 'access-control-allow-origin': '*' } })
    : r.abort('blockedbyclient'));
  await page.goto(url);
  return { page, errors };
}
const settle = page => page.waitForTimeout(350);
const diagramDrawn = page => page.waitForSelector('#vp .bx', { timeout: 15000 });
const hashParams = async page => Object.fromEntries(new URLSearchParams((await page.evaluate(() => location.hash)).slice(1)));
const centre = page => page.$eval('#fvp .fc.me', g => g.dataset.f);
const miniShown = page => page.$eval('#mini', m => m.style.display !== 'none');
const underMini = (page, sel) => page.evaluate(sel => { // the ids of the boxes that overlap the minimap, or ''
  const m = document.getElementById('mini').getBoundingClientRect();
  return [...document.querySelectorAll(sel)].filter(e => {
    const b = e.getBoundingClientRect();
    return b.left < m.right && b.right > m.left && b.top < m.bottom && b.bottom > m.top;
  }).map(e => e.closest('[data-box], [data-f]')?.getAttribute('data-box') || e.closest('[data-f]')?.dataset.f).join(', ');
}, sel);
const graphOf = page => page.evaluate(() => JSON.parse(document.getElementById('graph').textContent));

async function smoke(browser) {
  const url = 'file://' + file;
  const { page, errors } = await open(browser, url);
  await diagramDrawn(page);
  const tabs = await page.$$eval('.tabs button', bs => bs.map(b => b.textContent.trim()));
  check(['Diagram', 'Focus', 'Flows', 'Discover'].every(t => tabs.includes(t)), 'tabs: ' + tabs.join(', '));
  check(await page.$('#klegend:not([hidden]) span') !== null, 'kind legend shows');
  check((await hashParams(page)).tab === 'diag', 'the hash names the tab');

  // the diagram: containment depth, then every lens, with and without a proposal
  const boxes = () => page.$$eval('#vp .bx', l => l.length);
  const b1 = await boxes();
  await page.click('[data-depth="2"]'); await diagramDrawn(page); await settle(page);
  check(await boxes() > b1, `show inside 2 opens boxes (${b1} -> ${await boxes()})`);
  const proposal = await page.$eval('#init', s => [...s.options].map(o => o.value).find(Boolean) || '');
  const lenses = async () => page.$$eval('[data-act=lens] option', os => os.map(o => o.value));
  for (const lens of await lenses()) {
    await page.selectOption('[data-act=lens]', lens); await diagramDrawn(page); await settle(page);
    check(await boxes() > 0 && (await hashParams(page)).lens === (lens === 'containment' ? undefined : lens), `lens ${lens}: ${await boxes()} boxes`);
  }
  await page.selectOption('[data-act=lens]', 'system'); await diagramDrawn(page); await settle(page);
  await page.$$eval('#vp .bx[data-box^="grp:"]', gs => { // the biggest group
    const n = g => +((g.textContent.match(/(\d+) blocks/) || [])[1] || 0);
    gs.sort((a, b) => n(b) - n(a))[0].dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
  });
  await settle(page); await diagramDrawn(page); await settle(page);
  check(await page.$('#vp .bx:not([data-box^="grp:"])') !== null, 'double-click opens a lens group');
  if (await page.evaluate(() => JSON.parse(document.getElementById('graph').textContent).blocks.some(b => b.attrs?.owner)))
    check(await page.$$eval('#vp text.tg', l => l.some(t => t.textContent.startsWith('@'))), 'boxes show their owner');

  // the legend: a count per kind; a click hides that kind and the hash keeps it
  const legendKind = await page.$eval('#klegend [data-hide]', b => b.dataset.hide);
  const before = await boxes();
  await page.click(`#klegend [data-hide="${legendKind}"]`); await diagramDrawn(page).catch(() => {}); await settle(page);
  check(await page.$(`#klegend [data-hide="${legendKind}"].off`) !== null && (await hashParams(page)).hide === legendKind, `legend hides ${legendKind} (${before} -> ${await boxes()} boxes)`);
  await page.click(`#klegend [data-hide="${legendKind}"]`); await diagramDrawn(page); await settle(page);
  check(await page.$('#klegend .off') === null, 'legend shows it again');

  // the canvas toolbar: zoom, minimap, the view as Mermaid, export
  const zoom = () => page.$eval('#zp', z => z.textContent);
  const z0 = await zoom();
  await page.click('#ctool [data-tool=in]'); await settle(page);
  check(await zoom() !== z0, `zoom in (${z0} -> ${await zoom()})`);
  await page.click('#ctool [data-tool=fit]'); await settle(page);
  check(!(await miniShown(page)), 'the minimap hides while the whole drawing is in view');
  for (let i = 0; i < 4; i++) await page.click('#ctool [data-tool=in]');
  await settle(page);
  check(await miniShown(page), 'the minimap appears when part of the drawing is off screen');
  await page.click('#ctool [data-tool=mini]');
  check(!(await miniShown(page)), 'the minimap toggles off');
  await page.click('#ctool [data-tool=mini]');
  check(await page.$eval('#mini', m => m.style.display !== 'none' && m.querySelectorAll('rect').length > 1), 'the minimap toggles on and shows the boxes');
  await page.click('#ctool [data-tool=fit]'); await settle(page);
  const underD = await underMini(page, '#vp .bx rect.l, #vp .bx rect.c');
  check(await miniShown(page) && !underD, 'Diagram: after Fit with the minimap on, no box is under the minimap' + (underD ? ': ' + underD : ''));
  await page.click('#ctool [data-tool=text]'); await settle(page);
  check(/^flowchart LR/m.test(await page.$eval('#mmd', p => p.textContent)), 'the view as Mermaid text');
  await page.keyboard.press('Escape');
  check(await page.$('#tview[hidden]') !== null, 'Escape leaves the text view');
  for (const [x, ext, sniff] of [['svg', '.svg', '<svg'], ['png', '.png', '\x89PNG'], ['mmd', '.mmd', 'flowchart']]) {
    await page.click('#ctool [data-tool=export]');
    const [dl] = await Promise.all([page.waitForEvent('download', { timeout: 10000 }), page.click(`#xmenu [data-x=${x}]`)]);
    const body = fs.readFileSync(await dl.path(), 'latin1');
    check(dl.suggestedFilename().endsWith(ext) && body.includes(sniff), `export ${x}: ${dl.suggestedFilename()} (${body.length} bytes)`);
  }
  if (proposal) {
    await page.selectOption('#init', proposal); await settle(page);
    check((await lenses()).includes('verdict'), 'the verdict lens appears with a proposal');
    await page.selectOption('[data-act=lens]', 'verdict'); await diagramDrawn(page); await settle(page);
    check(await page.$$eval('#vp .bx[data-box^="grp:"]', l => l.length) >= 2, 'verdict lens groups blocks');
    check(await page.$('#legend:not([hidden])') !== null, 'verdict legend shows');
  }
  await page.selectOption('[data-act=lens]', 'containment'); await diagramDrawn(page); await settle(page);

  // focus: find a block, hops, re-centre, history, details, system context
  await page.click('.tabs [data-tab=focus]'); await page.waitForSelector('#fvp .fc.me');
  const target = await page.evaluate(() => { // a block with links on both sides
    const g = JSON.parse(document.getElementById('graph').textContent);
    const deg = {};
    for (const e of g.block_edges) { (deg[e.from] ||= [0, 0])[1]++; (deg[e.to] ||= [0, 0])[0]++; }
    const ok = g.blocks.filter(b => !b.initiative && deg[b.id] && deg[b.id][0] && deg[b.id][1]);
    ok.sort((a, b) => (deg[b.id][0] + deg[b.id][1]) - (deg[a.id][0] + deg[a.id][1]));
    return ok[0] && { id: ok[0].id, title: ok[0].title };
  });
  await page.fill('#fq', target.title); await page.press('#fq', 'Enter'); await settle(page);
  check(await centre(page) === target.id, `focus search centres ${target.id}`);
  const cols = () => page.$$eval('#fvp text.colh', l => l.length);
  const c1 = await cols();
  await page.click('[data-hops="2"]'); await settle(page);
  check(await cols() >= c1 && (await hashParams(page)).hops === '2', `hops 2 (${c1} -> ${await cols()} columns)`);
  await page.click('[data-hops="3"]'); await settle(page);
  check((await hashParams(page)).hops === '3', 'hops 3');
  await page.click('[data-hops="1"]'); await settle(page);
  const neighbour = await page.$eval('#fvp .fc:not(.me)', g => g.dataset.f);
  await page.click(`#fvp .fc[data-f="${neighbour}"] rect.b`); await page.waitForTimeout(600);
  check(await centre(page) === neighbour && (await hashParams(page)).id === neighbour, 'clicking a neighbour re-centres it');
  await page.click('#fbar [data-act=back]'); await settle(page);
  check(await centre(page) === target.id, 'back returns to the previous centre');
  await page.click('#fbar [data-act=fwd]'); await settle(page);
  check(await centre(page) === neighbour, 'forward goes again');
  await page.goBack(); await settle(page);
  check(await centre(page) === target.id, 'the browser back button walks focus too');
  await page.dblclick('#fvp .fc:not(.me) rect.b'); await settle(page);
  check(await page.$('#detail:not(.none) h2') !== null, 'double-click opens the details');
  await page.keyboard.press('Escape'); await settle(page);
  check(await page.$('#detail.none') !== null, 'Escape closes the details');
  await page.check('[data-act=ctx]'); await settle(page);
  const ctxKinds = await page.$$eval('#fvp .fc', l => l.map(g => g.querySelector('text.kk').textContent));
  check(ctxKinds.length > 0 && (await hashParams(page)).ctx === '1', 'system context: ' + [...new Set(ctxKinds)].join(', '));
  await page.keyboard.press('c'); await settle(page);
  check((await hashParams(page)).ctx === undefined, 'c toggles system context off');
  await page.click('#ctool [data-tool=fit]');

  // every block once in Focus: system context around a system, at each hop count
  const g0 = await graphOf(page);
  const sys = (g0.blocks.find(b => b.id === 'sys.checkout') || g0.blocks.find(b => b.kind === 'system' && !b.initiative) || {}).id;
  if (sys) {
    await page.goto(url + '#tab=focus&id=' + encodeURIComponent(sys) + '&ctx=1&hops=2'); await page.waitForSelector('#fvp .fc.me'); await settle(page);
    for (const hops of [2, 3, 1]) {
      if (hops !== 2) { await page.click(`[data-hops="${hops}"]`); await settle(page); }
      const ids = await page.$$eval('#fvp .fc', l => l.map(g => g.dataset.f));
      const heads = await page.$$eval('#fvp text.colh', l => l.reduce((n, t) => n + +t.textContent.split('·').pop(), 0));
      const more = await page.$$eval('#fvp .fmore', l => l.length);
      // the headers count each card once (and the blocks behind a "+N more" box)
      const headsOk = more ? heads >= ids.length - 1 : heads === ids.length - 1;
      check(new Set(ids).size === ids.length && headsOk, `system context at ${hops} hop(s): each block once (${ids.length} cards, headers count ${heads})`);
      if (hops === 2 && sys === 'sys.checkout') {
        check(await page.$('#fvp .fc .both') !== null, 'a block linked both ways is marked ⇄ both ways');
        check(await page.$('#fvp path.fe[data-a^="out|"][data-b^="c|"]') !== null, 'its inbound edge points back at the centre');
      }
    }
    if (!(await miniShown(page))) await page.click('#ctool [data-tool=mini]');
    await page.click('#ctool [data-tool=fit]'); await settle(page);
    const underF = await underMini(page, '#fvp .fc rect.b, #fvp .fmore rect');
    check(await miniShown(page) && !underF, 'Focus: after Fit with the minimap on, no card is under the minimap' + (underF ? ': ' + underF : ''));
  }

  // edge filter is shared
  await page.click('#fbar details.kinds summary');
  await page.click('#fbar details.kinds input[data-kind]'); await settle(page);
  check(await page.$eval('#fbar details.kinds summary', s => /of/.test(s.textContent)), 'the edge filter applies to Focus');
  await page.click('#fbar details.kinds input[data-kind]'); await settle(page);
  await page.click('#fbar details.kinds summary'); await settle(page); // close the menu

  // right-click a card: copy its id, then focus another
  await page.click('#fvp .fc.me rect.b', { button: 'right' });
  check(await page.$('.cmenu:not([hidden])') !== null, 'right-click opens the block menu');
  await page.click('.cmenu [data-cm=copy]'); await settle(page);
  check(await page.$('.toast') !== null, 'copy id');
  const other = await page.$eval('#fvp .fc:not(.me)', g => g.dataset.f);
  await page.click(`#fvp .fc[data-f="${other}"] rect.b`, { button: 'right' });
  await page.click('.cmenu [data-cm=focus]'); await settle(page);
  check(await centre(page) === other, 'the menu focuses a block');

  // flows: swimlanes, then a walkthrough on the diagram
  await page.click('.tabs [data-tab=flows]'); await settle(page);
  const hasFlows = await page.$('[data-play]');
  if (hasFlows) {
    check(await page.$$eval('.lanes text.ln', l => l.length) >= 1, 'steps grouped by system in lanes');
    await page.keyboard.press('g'); await settle(page);
    check(await page.$eval('[data-lanes].on', b => b.dataset.lanes) === 'owner', 'g cycles the grouping');
    await page.click('[data-lmode=boxes]'); await settle(page);
    check(await page.$('.lanes rect.gbox') !== null, 'groups as boxes');
    await page.click('[data-play]'); await diagramDrawn(page); await settle(page);
    check(await page.$('#vp .bx.cur') !== null && await page.$('#walk:not([hidden])') !== null, 'a flow walks on the diagram');
    const stepNo = async () => +(await page.$eval('#walk .wh', e => e.textContent)).match(/step (\d+)/)[1];
    await page.keyboard.press('ArrowRight'); await settle(page);
    check(await stepNo() === 2, '→ steps forward');
    await page.keyboard.press('ArrowLeft'); await settle(page);
    check(await stepNo() === 1, '← steps back');
    let forked = false;
    for (let i = 0; i < 25 && !forked; i++) {
      if (await page.$$eval('#walk .ways button', l => l.length) > 1) {
        await page.keyboard.press('2'); await settle(page);
        forked = /off the recorded walk/.test(await page.$eval('#walk .wh', e => e.textContent));
        break;
      }
      await page.keyboard.press('ArrowRight'); await page.waitForTimeout(120);
    }
    check(forked, 'a number takes another way at a fork');
    await page.keyboard.press('Escape'); await settle(page);
    check(await page.$('#walk[hidden]') !== null, 'Esc stops the walkthrough');
  }

  // discover: proposal-only blocks are named, text filter, a chip, sorting, a row opens Focus
  await page.selectOption('#init', ''); await settle(page);
  await page.click('.tabs [data-tab=discover]'); await page.waitForSelector('#dtable tr[data-row]');
  const proposed = (await graphOf(page)).blocks.filter(b => b.initiative).length;
  if (proposed) {
    const counts = await page.$eval('#counts', e => e.textContent);
    check(counts.includes(`+${proposed} in proposals`), `the header counts proposal blocks apart: ${counts}`);
    const rowsOf = async () => +(await page.$eval('#dcount', e => e.textContent)).match(/of (\d+) blocks/)[1];
    const hint = await page.$eval('#dcount', e => e.textContent), r0 = await rowsOf();
    check(hint.includes(`${proposed} more only in proposals`), `Discover names the proposal-only blocks: ${hint}`);
    await page.click('#dcount [data-pick-init]'); await page.waitForSelector('#dtable tr[data-row]'); await settle(page);
    const r1 = await rowsOf();
    check(await page.$eval('#init', s => s.value) !== '' && r1 > r0, `clicking it selects the proposal (${r0} -> ${r1} rows)`);
  }
  const total = await page.$$eval('#dtable tr[data-row]', l => l.length);
  await page.fill('#dq', target.title.split(' ')[0]); await settle(page);
  const filtered = await page.$$eval('#dtable tr[data-row]', l => l.length);
  check(filtered > 0 && filtered <= total, `text filter (${total} -> ${filtered} rows)`);
  await page.fill('#dq', ''); await settle(page);
  const chip = await page.$('[data-facet=kind]');
  const kind = await chip.getAttribute('data-v');
  await chip.click(); await settle(page);
  const kinds = await page.$$eval('#dtable tr[data-row] td:nth-child(2)', l => [...new Set(l.map(td => td.textContent.trim()))]);
  check(kinds.length === 1 && kinds[0] === kind, `kind chip ${kind} filters the table`);
  await page.click('th[data-sort=in]'); await settle(page);
  const ins = await page.$$eval('#dtable tr[data-row] td.num:nth-of-type(7)', l => l.map(td => +td.textContent || 0));
  check(ins.every((v, i) => !i || ins[i - 1] >= v), 'sorting by inbound');
  const row = await page.$eval('#dtable tr[data-row]', r => r.dataset.row);
  await page.click(`#dtable tr[data-row="${row}"]`); await page.waitForSelector('#fvp .fc.me'); await settle(page);
  check(await centre(page) === row, 'a Discover row opens Focus on it');

  // keyboard and deep links
  await page.keyboard.press('/');
  check(await page.evaluate(() => document.activeElement?.id) === 'fq', '/ focuses the search');
  await page.keyboard.press('Escape');
  await page.goto(url + '#tab=focus&id=' + encodeURIComponent(target.id) + '&hops=2&ctx=1'); await page.waitForSelector('#fvp .fc.me'); await settle(page);
  check(await page.$eval('[data-act=ctx]', c => c.checked) && await page.$eval('[data-hops="2"]', b => b.classList.contains('on')), 'a deep link restores focus, hops and context');
  await page.goto(url + '#tab=diag&lens=owner'); await diagramDrawn(page); await settle(page);
  check(await page.$eval('[data-act=lens]', s => s.value) === 'owner', 'a deep link restores the lens');
  await page.goto(url + '#tab=discover'); await page.waitForSelector('#dtable tr[data-row]');
  check(true, 'a deep link opens Discover');

  check(errors.length === 0, 'no console errors' + (errors.length ? ':\n  ' + errors.join('\n  ') : ''));
  await page.close();
}

async function screenshots(browser) {
  fs.mkdirSync(shots, { recursive: true });
  const target = await (async () => {
    const { page } = await open(browser, 'file://' + file);
    const t = await page.evaluate(() => {
      const g = JSON.parse(document.getElementById('graph').textContent);
      const deg = {};
      for (const e of g.block_edges) { (deg[e.from] ||= [0, 0])[1]++; (deg[e.to] ||= [0, 0])[0]++; }
      const ok = g.blocks.filter(b => !b.initiative && b.kind !== 'area' && deg[b.id] && deg[b.id][0] && deg[b.id][1] > 1);
      ok.sort((a, b) => (deg[b.id][0] + deg[b.id][1]) - (deg[a.id][0] + deg[a.id][1]));
      const sys = g.blocks.filter(b => b.kind === 'system' && !b.initiative).sort((a, b) => (deg[b.id]?.[0] || 0) - (deg[a.id]?.[0] || 0));
      return { id: ok[0].id, sys: (sys.find(s => /checkout/i.test(s.id)) || sys[0]).id };
    });
    await page.close();
    return t;
  })();
  const id = encodeURIComponent(target.id);
  const views = [
    ['diagram-containment', '#tab=diag', '#vp .bx'],
    ['diagram-system-lens', '#tab=diag&lens=system', '#vp .bx'],
    ['focus-hop1', `#tab=focus&id=${id}`, '#fvp .fc'],
    ['focus-hop2', `#tab=focus&id=${id}&hops=2`, '#fvp .fc'],
    ['focus-system-context', `#tab=focus&id=${encodeURIComponent(target.sys)}&ctx=1&hops=2`, '#fvp .fc'],
    ['discover', '#tab=discover', '#dtable tr'],
  ];
  for (const scheme of ['light', 'dark']) for (const [name, hash, sel] of views) {
    const { page, errors } = await open(browser, 'file://' + file + hash, scheme);
    await page.waitForSelector(sel, { timeout: 15000 }); await page.waitForTimeout(500);
    const out = path.join(shots, `${name}-${scheme}.png`);
    await page.screenshot({ path: out });
    console.log('wrote ' + out + (errors.length ? ' (console errors: ' + errors.join('; ') + ')' : ''));
    failed += errors.length;
    await page.close();
  }
}

function generate(n) { // a synthetic graph of about n blocks: systems, areas, nested blocks, typed edges, flows
  const kinds = ['screen', 'api', 'action', 'route', 'service', 'data', 'rule', 'config', 'invariant', 'consumer'];
  const blocks = [], edges = [], flows = [];
  let seed = 7;
  const rnd = k => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed % k; };
  const sys = 12, areas = 8, per = Math.max(4, Math.round((n - sys * (areas + 1) - 6) / (sys * areas)));
  blocks.push({ id: 'actor.user', kind: 'actor', title: 'User', truth: 'declared', provenance: { kind: 'judgment' } });
  for (let x = 0; x < 5; x++) blocks.push({ id: 'ext.' + x, kind: 'external', title: 'External ' + x, truth: 'declared', provenance: { kind: 'judgment' } });
  const leaves = [];
  for (let s = 0; s < sys; s++) {
    const sid = 'sys.' + s;
    blocks.push({ id: sid, kind: 'system', title: 'System ' + s, truth: 'declared', provenance: { kind: 'judgment' }, attrs: { owner: 'team-' + (s % 5) } });
    for (let a = 0; a < areas; a++) {
      const aid = `s${s}:a${a}`;
      blocks.push({ id: aid, kind: 'area', parent: sid, title: `Area ${s}.${a}`, tier: s % 2 ? 'backend' : 'frontend', system: sid, truth: 'declared', provenance: { kind: 'adapter' } });
      let prev = '';
      for (let i = 0; i < per; i++) {
        const id = `${aid}.b${i}`, parent = i > 2 && rnd(3) === 0 ? `${aid}.b${i - 1 - rnd(2)}` : aid;
        blocks.push({ id, kind: kinds[rnd(kinds.length)], parent, title: `Block ${s}.${a}.${i} ${['cart', 'pay', 'search', 'order', 'stock'][rnd(5)]}`,
          tier: s % 2 ? 'backend' : 'frontend', system: sid, truth: rnd(4) ? 'declared' : 'wired', provenance: { kind: 'adapter' }, attrs: { status: rnd(3) ? 'built' : 'planned' } });
        if (prev && parent === aid) edges.push({ from: prev, to: id, kind: 'flow', labels: ['next'], provenance: { kind: 'adapter' } });
        prev = parent === aid ? id : prev;
        leaves.push(id);
      }
    }
  }
  const kindsE = ['api', 'event', 'server_api', 'uses', 'configures'];
  for (let i = 0; i < n * 1.2; i++) {
    const a = leaves[rnd(leaves.length)], b = leaves[rnd(leaves.length)];
    if (a.split('.')[0] !== b.split('.')[0] || rnd(4) === 0) edges.push({ from: a, to: b, kind: kindsE[rnd(kindsE.length)], provenance: { kind: 'adapter' } });
  }
  for (let x = 0; x < 40; x++) edges.push({ from: leaves[rnd(leaves.length)], to: 'ext.' + rnd(5), kind: 'server_api', provenance: { kind: 'judgment' } });
  for (let x = 0; x < 6; x++) edges.push({ from: 'actor.user', to: `s${x}:a0`, kind: 'uses', provenance: { kind: 'judgment' } });
  for (let f = 0; f < 5; f++) flows.push({ id: 'flow' + f, title: 'Flow ' + f, end: 'ends', steps: Array.from({ length: 12 }, (_, i) => ({ to: leaves[rnd(leaves.length)], kind: i ? 'flow' : 'start' })) });
  const seen = new Set();
  return { blocks, block_edges: edges.filter(e => { const k = e.from + '>' + e.to + '>' + e.kind; if (seen.has(k) || e.from === e.to) return false; seen.add(k); return true; }), flows, sources: [] };
}

async function measure(browser) {
  const g = generate(perf);
  const html = fs.readFileSync(file, 'utf8').replace(/(<script id="graph" type="application\/json">)[\s\S]*?(<\/script>)/,
    (_, a, b) => a + JSON.stringify(g).replace(/</g, '\\u003c') + b);
  const big = path.join(path.dirname(file), path.basename(file, '.html') + `-perf-${g.blocks.length}.html`);
  fs.writeFileSync(big, html);
  console.log(`generated ${g.blocks.length} blocks, ${g.block_edges.length} edges, ${g.flows.length} flows (${big})`);
  const t0 = Date.now();
  const { page, errors } = await open(browser, 'file://' + big);
  await page.evaluate(() => { window.__long = []; new PerformanceObserver(l => l.getEntries().forEach(e => window.__long.push(e.duration))).observe({ entryTypes: ['longtask'] }); });
  await diagramDrawn(page);
  const rows = [['load to first diagram', Date.now() - t0]];
  const time = async (what, fn) => { const s = Date.now(); await fn(); rows.push([what, Date.now() - s]); };
  const frame = () => page.evaluate(() => new Promise(r => requestAnimationFrame(() => setTimeout(r, 0))));
  for (const lens of ['system', 'owner', 'kind', 'tier', 'truth', 'containment'])
    await time(`lens ${lens}`, async () => { await page.selectOption('[data-act=lens]', lens); await frame(); await diagramDrawn(page); await page.waitForFunction(() => !document.querySelector('#vp:empty')); await frame(); });
  await time('show inside 2', async () => { await page.click('[data-depth="2"]'); await frame(); await diagramDrawn(page); await page.waitForTimeout(50); await page.waitForFunction(() => document.querySelectorAll('#vp .bx').length > 30, null, { timeout: 60000 }); });
  await time('tree search "pay"', async () => { await page.fill('#q', 'pay'); await frame(); });
  await page.fill('#q', '');
  const deg = {};
  for (const e of g.block_edges) { deg[e.from] = (deg[e.from] || 0) + 1; deg[e.to] = (deg[e.to] || 0) + 1; }
  const busiest = Object.keys(deg).sort((a, b) => deg[b] - deg[a])[0];
  await time(`Focus via a deep link (hop 1, ${deg[busiest]} links)`, async () => {
    await page.evaluate(id => { location.hash = 'tab=focus&id=' + encodeURIComponent(id); }, busiest);
    await page.waitForSelector('#fvp .fc.me'); await frame();
  });
  await time('Focus hop 3', async () => { await page.click('[data-hops="3"]'); await frame(); });
  const inView = await page.$$eval('#fvp .fc:not(.me) rect.b', rs => rs.map(r => r.getBoundingClientRect())
    .filter(b => b.left > 0 && b.top > 60 && b.right < innerWidth && b.bottom < innerHeight - 80).map(b => [b.left + b.width / 2, b.top + b.height / 2])[0]);
  await time('Focus re-centre (incl. the 240 ms double-click wait)', async () => { await page.mouse.click(...inView); await page.waitForSelector('#fvp .fc.me'); await page.waitForTimeout(260); await frame(); });
  await time('Focus system context', async () => { await page.check('[data-act=ctx]'); await frame(); });
  await time('Discover tab', async () => { await page.click('.tabs [data-tab=discover]'); await page.waitForSelector('#dtable tr[data-row]'); await frame(); });
  await time('Discover filter keystroke', async () => { await page.type('#dq', 'p'); await frame(); });
  await time('Discover chip', async () => { await page.click('[data-facet=kind]'); await frame(); });
  await time('Discover sort', async () => { await page.click('th[data-sort=out]'); await frame(); });
  const long = await page.evaluate(() => window.__long);
  for (const [w, ms] of rows) console.log(`${String(ms).padStart(6)} ms  ${w}`);
  console.log(`long tasks: ${long.length}, longest ${Math.round(Math.max(0, ...long))} ms`);
  check(errors.length === 0, 'no console errors' + (errors.length ? ': ' + errors.join('; ') : ''));
  await page.close();
}

(async () => {
  const browser = await chromium.launch();
  try {
    if (perf) await measure(browser);
    else if (shots) await screenshots(browser);
    else await smoke(browser);
  } finally { await browser.close(); }
  console.log(failed ? `${failed} failed` : 'all passed');
  process.exit(failed ? 1 : 0);
})().catch(e => { console.error(e); process.exit(1); });
