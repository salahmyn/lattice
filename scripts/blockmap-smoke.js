#!/usr/bin/env node
// @module-feature ui
// Browser smoke test for the block map (`lattice export map`), with Playwright.
//
//   node scripts/blockmap-smoke.js <map.html> [--shots <dir>] [--perf <blocks>]
//   node scripts/blockmap-smoke.js --served <base-url>
//   node scripts/blockmap-smoke.js <map.html> --layers
//
// It loads the exported page, switches every tab and lens, turns the assurance
// lens on and off, hides a kind from the legend, uses the canvas toolbar (zoom, minimap, Mermaid text, SVG/PNG/
// Mermaid export), focuses a block, re-centres, walks the history, toggles
// system context, uses the right-click menu, filters Discover, groups a flow
// into swimlanes, walks it (keys and a fork) and follows deep links, and fails
// on any console error.
//
// The network is blocked: every request that is not file:// is aborted and
// fails the run, so the page must lay out with the layout library it carries
// inline (ELK's layered layout, not the grid fallback). A second load stops the
// library from starting and checks that the grid fallback still draws. At
// 1600×1000 a generated wide, shallow graph must be laid out wide (its
// width:height near the pane's, Fit at 35% or more, no boxes overlapping), and
// the demo graph no smaller than before. Where many lines converge on one block, closed or open, no two edge
// labels overlap.
//
// --shots writes PNGs of each view in light and dark. --perf N swaps in a
// generated graph of about N blocks and prints timings instead of the smoke run.
//
// --layers runs only the Layer lens checks, on a map whose blocks carry computed
// layers: the lens groups blocks by layer, draws a data block and an unknown block
// in different colours, and lists each layer in its legend.
//
// --served opens <base-url>/map from a running `lattice serve` instead, with
// /api/graph intercepted: a slow graph must show the live load status (elapsed
// seconds, then the first-load hint), a failed one an error naming the cause
// with a retry that loads it, and the status must be gone once the map draws.
// Every request to another origin is blocked and fails the run.
// It needs `require('playwright')` to resolve; it never installs browsers.
'use strict';
const fs = require('fs');
const path = require('path');
const { chromium } = require('playwright');

const args = process.argv.slice(2);
const opt = name => { const i = args.indexOf('--' + name); return i >= 0 ? args[i + 1] || '' : ''; };
const shots = opt('shots'), perf = parseInt(opt('perf'), 10) || 0, served = opt('served'), layers = args.includes('--layers');
const file = served ? '' : path.resolve(args[0] || '');
if (!served && (!args[0] || !fs.existsSync(file))) { console.error('usage: blockmap-smoke.js <map.html> [--shots dir] [--perf n] | --served <base-url>'); process.exit(2); }

let failed = 0;
function check(ok, what) { if (ok) console.log('ok   ' + what); else { failed++; console.log('FAIL ' + what); } }

async function open(browser, url, scheme, noLayout) {
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 }, colorScheme: scheme || 'light' });
  const errors = [];
  page.on('console', m => { if (m.type() === 'error') errors.push(m.text()); });
  page.on('pageerror', e => errors.push('uncaught: ' + e.message));
  // no network: anything but the file itself is aborted, and counts as an error
  await page.route(u => !/^(file|data|blob|about):/.test(u.protocol), r => { errors.push('network request: ' + r.request().url()); return r.abort('blockedbyclient'); });
  if (noLayout) await page.addInitScript(() => Object.defineProperty(window, 'ELK', { get: () => undefined, set: () => {} }));
  await page.goto(url);
  return { page, errors };
}
const pageText = page => page.evaluate(() => document.body.innerText);
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

// the Layer lens: blocks grouped by the layers the kernel computed, one colour each
async function layerLens(browser) {
  const { page, errors } = await open(browser, 'file://' + file);
  await diagramDrawn(page);
  const g = await graphOf(page);
  const withLayers = g.blocks.filter(b => (b.layers || []).length);
  check(withLayers.length > 0, `the graph carries computed layers (${withLayers.length} blocks)`);
  check((await page.$$eval('[data-act=lens] option', os => os.map(o => o.value))).includes('layer'), 'the Group by menu offers the Layer lens');
  check(await page.$('#llegend:not([hidden])') === null, 'the layer legend is hidden while another lens is on');
  await page.selectOption('[data-act=lens]', 'layer'); await diagramDrawn(page); await settle(page);
  check((await hashParams(page)).lens === 'layer', 'a deep link names the layer lens');
  const fill = key => page.$eval(`#vp .bx[data-box="grp:${key}"] rect[style*="fill"]`, r => getComputedStyle(r).fill).catch(() => '');
  const data = await fill('data'), unknown = await fill('unknown'), ui = await fill('ui'), multi = await fill('multi');
  check(!!data && !!unknown && data !== unknown, `a data block and an unknown block are drawn in different colours (${data} vs ${unknown})`);
  check(!!ui && ui !== data && ui !== unknown, `a ui block has its own colour (${ui})`);
  check(!!multi && ![data, unknown, ui].includes(multi), `a block spanning several layers has a neutral colour of its own (${multi})`);
  const legend = await page.$eval('#llegend', e => e.hidden ? '' : e.innerText).catch(() => '');
  check(/\bdata\b/.test(legend) && /\bunknown\b/.test(legend) && /several layers/.test(legend), 'the legend lists each layer present: ' + legend.replace(/\s+/g, ' ').slice(0, 120));
  await page.click('#vp .bx[data-box="grp:data"]'); await settle(page);
  check((await pageText(page)).includes('Layer · '), 'clicking a group lists its blocks');
  check(errors.length === 0, 'no console errors' + (errors.length ? ': ' + errors.join('; ') : ''));
  await page.close();
}

async function smoke(browser) {
  const url = 'file://' + file;
  const { page, errors } = await open(browser, url);
  await diagramDrawn(page);
  check(await page.$('#gload') === null && !(await pageText(page)).includes('Building the block graph'), 'the exported map shows no load status (its graph is inline)');
  check(await page.evaluate(() => typeof window.ELK === 'function'), 'the layout library starts from the file itself');
  check(!(await pageText(page)).includes('simple grid') && await page.$('#vp path[d*="C"]') !== null, 'the diagram has the layered layout (spline edges), not the grid fallback');
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

  // the assurance lens: each box takes the style the kernel gave its block, the legend says what each means
  if (await page.evaluate(() => !!JSON.parse(document.getElementById('graph').textContent).assurance)) {
    await page.click('#assure'); await diagramDrawn(page); await settle(page);
    check(await page.$eval('#assure', b => b.getAttribute('aria-pressed')) === 'true' && (await hashParams(page)).as === '1', 'the assurance toggle turns the lens on');
    const legend = await page.$eval('#alegend', e => ({ shown: !e.hidden, keys: e.querySelectorAll('.ak').length, text: e.textContent }));
    const nKeys = (await graphOf(page)).assurance.legend.length;
    check(legend.shown && legend.keys === nKeys && legend.text.includes('Unknown is never a pass.'), `the assurance legend lists ${legend.keys} classes and the rule`);
    const drawn = await page.evaluate(() => { // each box's style class, against the style the data names for its block
      const A = JSON.parse(document.getElementById('graph').textContent).assurance;
      const out = { n: 0, bad: [] };
      for (const g of document.querySelectorAll('#vp .bx[data-box]')) {
        const id = g.dataset.box.replace(/^out:/, ''), r = g.querySelector('rect.l, rect.c');
        if (/^(grp|more):/.test(g.dataset.box) || !r) continue;
        out.n++;
        const want = (A.nodes[id] || { style: A.fallback }).style;
        if (!r.classList.contains('as-' + want) || [...r.classList].filter(c => c.startsWith('as-')).length !== 1) out.bad.push(id);
      }
      return out;
    });
    check(drawn.n > 0 && !drawn.bad.length, `the assurance lens draws each box in its kernel style (${drawn.n} boxes)` + (drawn.bad.length ? ': ' + drawn.bad.join(', ') : ''));
    const feat = await page.evaluate(() => Object.entries(JSON.parse(document.getElementById('graph').textContent).assurance.nodes).find(([, n]) => n.class)?.[0] || '');
    if (feat) {
      await page.goto(url + '#tab=diag&as=1&id=' + encodeURIComponent(feat)); await diagramDrawn(page); await settle(page);
      check(await page.$$eval('#detail h3', l => l.some(h => h.textContent === 'Assurance')) && await page.$eval('#alegend', e => !e.hidden), 'a deep link restores the lens; the details show the block\'s assurance');
    }
    await page.click('.tabs [data-tab=discover]'); await page.waitForSelector('#dtable tr[data-row]');
    check(await page.$('th[data-sort=assure]') !== null && await page.$('[data-facet=assure]') !== null, 'Discover gains an assurance column and facet');
    await page.click('.tabs [data-tab=diag]'); await diagramDrawn(page);
    await page.keyboard.press('a'); await diagramDrawn(page); await settle(page);
    check(await page.$('#alegend[hidden]') !== null && await page.$('#vp rect.as') === null && (await hashParams(page)).as === undefined, 'a turns the lens off');
  }

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

  // the layout library does not start: the grid fallback still draws, with no request either
  const off = await open(browser, url, 'light', true);
  await diagramDrawn(off.page);
  check((await pageText(off.page)).includes('layout library not available: simple grid') && await off.page.$('#gload') === null && off.errors.length === 0,
    'without the layout library the grid fallback draws' + (off.errors.length ? ':\n  ' + off.errors.join('\n  ') : ''));
  await off.page.close();

  await paneShape(browser);
  await labelsApart(browser);
}

// edge labels: where many lines converge on one block, no two labels overlap (beyond 1.5 px, at 100% so the text
// boxes are the browser's own), whether the block is closed or open with the lines lifted to its frame
async function labelsApart(browser) {
  for (const [name, hub, n] of [['a closed block', 'composite', 8], ['an open block', 'system', 6]]) {
    const g = converging(hub, n), lines = new Set(g.block_edges.map(e => e.from.split('.')[0])).size;
    const conv = path.join(path.dirname(file), path.basename(file, '.html') + `-converging-${hub}.html`);
    fs.writeFileSync(conv, withGraph(fs.readFileSync(file, 'utf8'), g));
    const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } });
    await page.route(u => !/^(file|data|blob|about):/.test(u.protocol), r => r.abort('blockedbyclient'));
    await page.goto('file://' + conv); await diagramDrawn(page); await settle(page);
    const m = await page.evaluate(() => {
      const vp = document.getElementById('vp'), tf = vp.getAttribute('transform');
      vp.setAttribute('transform', '');
      const ls = [...vp.querySelectorAll('text.el')].map(t => { const b = t.getBBox(); return { t: t.textContent, x0: b.x, y0: b.y, x1: b.x + b.width, y1: b.y + b.height }; });
      vp.setAttribute('transform', tf);
      const pairs = [];
      let area = 0;
      for (let i = 0; i < ls.length; i++) for (let j = i + 1; j < ls.length; j++) {
        const a = ls[i], b = ls[j], w = Math.min(a.x1, b.x1) - Math.max(a.x0, b.x0), h = Math.min(a.y1, b.y1) - Math.max(a.y0, b.y0);
        if (w > 1.5 && h > 1.5) { pairs.push(a.t + ' / ' + b.t); area += w * h; }
      }
      return { n: ls.length, pairs, area: Math.round(area), grid: document.body.innerText.includes('simple grid') };
    });
    check(!m.grid && m.n === lines && !m.pairs.length, `edge labels: ${lines} lines into ${name}, ${m.n} labels, ${m.pairs.length} overlapping (${m.area} px²)` +
      (m.pairs.length ? ': ' + m.pairs.slice(0, 5).join(', ') : ''));
    await page.close();
  }
}

// n blocks of two parts each, every part with a line into one hub block, so the n lines of 2 into the hub converge
function converging(hubKind, n) {
  const blocks = [{ id: 'hub', kind: hubKind, title: 'orders.checkout', truth: 'declared', provenance: { kind: 'human' } }], edges = [];
  for (let i = 1; i <= 2; i++) blocks.push({ id: 'hub.p' + i, kind: 'invariant', parent: 'hub', title: 'INV-' + i, truth: 'declared', provenance: { kind: 'human' } });
  const kinds = ['flow', 'uses', 'api', 'event'];
  for (let s = 0; s < n; s++) {
    blocks.push({ id: 'src' + s, kind: 'composite', title: `fees.part${s}`, truth: 'declared', provenance: { kind: 'human' } });
    for (let i = 1; i <= 2; i++) {
      blocks.push({ id: `src${s}.p${i}`, kind: 'invariant', parent: 'src' + s, title: 'INV-' + i, truth: 'declared', provenance: { kind: 'human' } });
      edges.push({ from: `src${s}.p${i}`, to: 'hub.p' + i, kind: kinds[s % 4], provenance: { kind: 'human' } });
    }
  }
  return { blocks, block_edges: edges, flows: [], sources: [] };
}

// the first layout follows the pane's shape: a wide, shallow graph is drawn wide, not as one tall column, and the
// demo graph is drawn no smaller than before
async function paneShape(browser) {
  const g = wideShallow();
  const wide = path.join(path.dirname(file), path.basename(file, '.html') + '-wide.html');
  fs.writeFileSync(wide, withGraph(fs.readFileSync(file, 'utf8'), g));
  const tops = g.blocks.filter(b => !b.parent).length;
  for (const [name, url, minZoom, minRatio] of [[`a wide, shallow graph (${g.blocks.length} blocks, ${tops} on top, ${g.block_edges.length} links)`, 'file://' + wide, 35, 0.6], ['the demo graph', 'file://' + file, 35, 0]]) {
    const page = await browser.newPage({ viewport: { width: 1600, height: 1000 }, deviceScaleFactor: 1 });
    const errors = [];
    page.on('pageerror', e => errors.push('uncaught: ' + e.message));
    await page.route(u => !/^(file|data|blob|about):/.test(u.protocol), r => r.abort('blockedbyclient'));
    await page.goto(url); await diagramDrawn(page); await settle(page);
    const m = await page.evaluate(() => {
      const vp = document.getElementById('vp'), bb = vp.getBBox(), svg = document.getElementById('dsvg').getBoundingClientRect();
      const top = (document.getElementById('dbar')?.offsetHeight || 0) + 10;
      const rs = [...vp.querySelectorAll('.bx')].map(g => { const r = g.querySelector('rect').getBBox(); return { id: g.dataset.box, x0: r.x, y0: r.y, x1: r.x + r.width, y1: r.y + r.height }; });
      const within = (a, b) => a.x0 >= b.x0 && a.y0 >= b.y0 && a.x1 <= b.x1 && a.y1 <= b.y1; // an open box holds what is inside it
      const overlaps = [];
      for (let i = 0; i < rs.length; i++) for (let j = i + 1; j < rs.length; j++) {
        const a = rs[i], b = rs[j];
        if (a.x0 < b.x1 && b.x0 < a.x1 && a.y0 < b.y1 && b.y0 < a.y1 && !within(a, b) && !within(b, a)) overlaps.push(a.id + ' / ' + b.id);
      }
      return { ratio: bb.width / bb.height, area: (svg.width - 40) / (svg.height - 60 - top), zoom: parseInt(document.getElementById('zp').textContent, 10), boxes: rs.length, overlaps, grid: document.body.innerText.includes('simple grid'),
        at: rs.map(r => `${r.id}@${Math.round(r.x0)},${Math.round(r.y0)}`).sort().join(' ') };
    });
    const ok = !m.grid && m.zoom >= minZoom && m.ratio >= minRatio * m.area && !m.overlaps.length && !errors.length;
    check(ok, `1600×1000, ${name}: Fit at ${m.zoom}% (want ≥ ${minZoom}%), width:height ${m.ratio.toFixed(2)} for a pane of ${m.area.toFixed(2)}` +
      (minRatio ? ` (want ≥ ${(minRatio * m.area).toFixed(2)})` : '') + `, ${m.boxes} boxes, ${m.overlaps.length} overlapping` +
      (m.overlaps.length ? ': ' + m.overlaps.slice(0, 5).join(', ') : '') + (errors.length ? ':\n  ' + errors.join('\n  ') : ''));
    if (minRatio) { // the same graph in the same pane: the same layout
      await page.reload(); await diagramDrawn(page); await settle(page);
      const again = await page.evaluate(() => [...document.querySelectorAll('#vp .bx')].map(g => { const r = g.querySelector('rect').getBBox(); return `${g.dataset.box}@${Math.round(r.x)},${Math.round(r.y)}`; }).sort().join(' '));
      check(again === m.at, `1600×1000, a wide, shallow graph: a reload lays it out the same`);
    }
    await page.close();
  }
}

// the exported page with another graph inline in place of its own
function withGraph(html, g) {
  return html.replace(/(<script id="graph" type="application\/json">)[\s\S]*?(<\/script>)/, (_, a, b) => a + JSON.stringify(g).replace(/</g, '\\u003c') + b);
}

// a wide, shallow graph: 60 features of 2 to 4 invariants each, 36 entry points that each start a flow into one
// feature, and features used in pairs; no system, so every feature and entry point is on top, in small groups
function wideShallow() {
  const blocks = [], edges = [], area = ['orders', 'fees', 'wallet', 'ledger', 'refunds', 'search', 'stock', 'reports'];
  for (let f = 0; f < 60; f++) {
    const id = 'feat:f' + f;
    blocks.push({ id, kind: 'composite', title: `${area[f % 8]}.part${f}`, description: 'A feature of the generated workspace.', truth: 'declared', provenance: { kind: 'human' } });
    for (let i = 1; i <= 2 + f % 3; i++) blocks.push({ id: `inv:f${f}.INV-${i}`, kind: 'invariant', parent: id, title: 'INV-' + i, truth: 'declared', provenance: { kind: 'human' } });
    if (f % 2) edges.push({ from: 'feat:f' + (f - 1), to: id, kind: 'uses', labels: ['depends on'], provenance: { kind: 'human' } });
  }
  for (let e = 0; e < 36; e++) {
    blocks.push({ id: 'ep:e' + e, kind: 'boundary-in', title: `POST /${area[e % 8]}/${e}`, truth: 'wired', provenance: { kind: 'extraction' } });
    edges.push({ from: 'ep:e' + e, to: 'feat:f' + (e * 7) % 60, kind: 'flow', provenance: { kind: 'extraction' } });
  }
  return { blocks, block_edges: edges, flows: [], sources: [] };
}

// the served map while /api/graph is slow or failing: a live status, then an error with a retry, and neither once drawn
async function loading(browser) {
  const origin = new URL(served).origin, url = origin + '/map';
  async function openServed(graph, noLayout) { // graph(route, n) answers the n-th /api/graph request
    const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
    const errors = [];
    page.on('console', m => { if (m.type() === 'error' && !/^Failed to load resource/.test(m.text())) errors.push(m.text()); });
    page.on('pageerror', e => errors.push('uncaught: ' + e.message));
    let n = 0;
    await page.route('**/*', r => {
      const u = new URL(r.request().url());
      if (u.origin !== origin) { errors.push('request to another origin: ' + u.href); return r.abort('blockedbyclient'); }
      if (u.pathname === '/api/graph') return graph(r, ++n);
      if (noLayout && u.pathname.startsWith('/map/elkjs')) return r.abort('failed');
      return r.continue();
    });
    await page.goto(url);
    return { page, errors, count: () => n };
  }
  const status = page => page.evaluate(() => {
    const el = document.getElementById('gload');
    if (!el) return null;
    const hint = el.querySelector('.ld-hint');
    return { shown: !el.hidden, role: el.getAttribute('role'), live: el.getAttribute('aria-live'), err: el.classList.contains('err'),
      text: el.textContent, sec: el.querySelector('.ld-sec')?.textContent || '', hint: !!hint && !hint.hidden, retry: !!el.querySelector('button[data-retry]') };
  });
  const wait = (page, fn, arg, ms) => page.waitForFunction(fn, arg, { timeout: ms || 10000 }).then(() => true, () => false);

  // slow: the status counts the seconds, adds the first-load hint after about 3 s, and goes once the map draws
  const slow = await openServed(async r => { await new Promise(t => setTimeout(t, 4200)); return r.continue(); });
  await wait(slow.page, () => !document.getElementById('gload')?.hidden);
  const s0 = await status(slow.page);
  check(s0 && s0.shown && s0.role === 'status' && s0.live === 'polite' && /Building the block graph/.test(s0.text) && !s0.hint,
    'served, slow graph: a polite live status says the graph is being built' + (s0 ? ` ("${s0.text.trim()}")` : ''));
  const ticked = await wait(slow.page, () => /^[1-9]\d* s$/.test(document.querySelector('#gload .ld-sec')?.textContent || ''), null, 2500);
  const s1 = await status(slow.page);
  check(ticked && s1 && !s1.hint, `served, slow graph: the elapsed seconds update (${s0 && s0.sec} -> ${s1 && s1.sec})`);
  const hinted = await wait(slow.page, () => { const h = document.querySelector('#gload .ld-hint'); return h && !h.hidden; }, null, 4000);
  const s2 = await status(slow.page);
  check(hinted && /first load builds the graph and the audit/.test(s2.text) && /cached/.test(s2.text) && +s2.sec.split(' ')[0] >= 3,
    `served, slow graph: after about 3 s the status says the first load builds the graph and later loads are cached (at ${s2 && s2.sec})`);
  await diagramDrawn(slow.page);
  check(await slow.page.$('#gload') === null, 'served, slow graph: the status is gone once the map draws');
  check(slow.errors.length === 0, 'served, slow graph: no console errors' + (slow.errors.length ? ':\n  ' + slow.errors.join('\n  ') : ''));
  await slow.page.close();

  // failing: a 500 names its status and the server's error, a network failure names itself; each offers a retry
  const fail = await openServed((r, n) => n === 1 ? r.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'graph build failed' }) })
    : n === 2 ? r.abort('failed') : r.continue());
  const errState = () => wait(fail.page, () => !!document.querySelector('#gload.err button[data-retry]'));
  const e1 = (await errState()) && await status(fail.page);
  check(e1 && e1.shown && e1.role === 'status' && /500/.test(e1.text) && /graph build failed/.test(e1.text) && e1.retry && !/Building/.test(e1.text),
    'served, HTTP 500: the status turns into an error naming the status and the cause, with a retry' + (e1 ? ` ("${e1.text.trim()}")` : ''));
  await fail.page.click('#gload button[data-retry]');
  const e2 = (await wait(fail.page, () => { const e = document.querySelector('#gload.err'); return !!e && !/500/.test(e.textContent) && !!e.querySelector('button[data-retry]'); })) && await status(fail.page);
  check(e2 && fail.count() === 2 && e2.retry && /fetch|network|failed/i.test(e2.text), 'served, network failure: retry fetches again and the error names the failure' + (e2 ? ` ("${e2.text.trim()}")` : ''));
  await fail.page.click('#gload button[data-retry]');
  await diagramDrawn(fail.page);
  check(fail.count() === 3 && await fail.page.$('#gload') === null, 'served: retry after a failure loads the graph, and the status is gone once the map draws');
  check(fail.errors.length === 0, 'served, failing graph: no console errors' + (fail.errors.length ? ':\n  ' + fail.errors.join('\n  ') : ''));
  await fail.page.close();

  // the layout library does not load: the grid fallback draws, and the status is gone with it
  const grid = await openServed(async r => { await new Promise(t => setTimeout(t, 600)); return r.continue(); }, true);
  await diagramDrawn(grid.page);
  check((await pageText(grid.page)).includes('layout library not available: simple grid') && await grid.page.$('#gload') === null && grid.errors.length === 0,
    'served, no layout library: the grid fallback draws and the status is gone' + (grid.errors.length ? ':\n  ' + grid.errors.join('\n  ') : ''));
  await grid.page.close();
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
  const html = withGraph(fs.readFileSync(file, 'utf8'), g);
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
    if (served) await loading(browser);
    else if (layers) await layerLens(browser);
    else if (perf) await measure(browser);
    else if (shots) await screenshots(browser);
    else await smoke(browser);
  } finally { await browser.close(); }
  console.log(failed ? `${failed} failed` : 'all passed');
  process.exit(failed ? 1 : 0);
})().catch(e => { console.error(e); process.exit(1); });
