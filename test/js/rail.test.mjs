// The rail scroll-spy, driven by stated rects: what it highlights, and where it
// decides to scroll.

import test from 'node:test';
import assert from 'node:assert/strict';
import { loadPage, runScript, rect, measure } from './page.mjs';

const LINK = 24; // the height every link is given below

// The fixture's rail holds two links; the case that matters is more headings than the
// screen, so the built link markup is cloned to lengthen it.
async function railPage({ headings = 12, clientHeight = 100 } = {}) {
  const dom = await loadPage('guide/index.html');
  const { window } = dom;
  const { document } = window;
  const rail = document.querySelector('.toc');
  const main = document.querySelector('main');
  const article = main.querySelector('article');
  const template = rail.querySelector('a');

  while (rail.querySelectorAll('a').length < headings) {
    const n = rail.querySelectorAll('a').length;
    const a = template.cloneNode(true);
    const id = `generated-heading-${n}`;
    a.setAttribute('href', `#${id}`);
    rail.appendChild(a);
    const h = document.createElement('h2');
    h.id = id;
    article.appendChild(h);
  }

  const links = [...rail.querySelectorAll('a')];
  const heads = links.map((a) => document.getElementById(decodeURIComponent(a.hash.slice(1))));
  measure(rail, { clientHeight, scrollHeight: LINK * links.length });
  rect(rail, { top: 0, bottom: clientHeight, height: clientHeight });
  rect(main, { top: 0 });
  links.forEach((a, i) => rect(a, { top: LINK * i, bottom: LINK * i + LINK, height: LINK }));
  // Placed before the script runs: the spy reads once on load, and unplaced headings
  // all answer top 0, which reads as scrolled to the end of the page.
  heads.forEach((h) => rect(h, { top: 5000 }));

  const scroll = () => main.dispatchEvent(new window.Event('scroll', { bubbles: true }));
  const highlight = () => links.map((a) => a.classList.contains('active')).indexOf(true);
  runScript(window, 'toc.js');
  assert.equal(rail.scrollTop, 0, 'a freshly loaded page has not scrolled its rail');
  return { window, document, rail, links, heads, scroll, highlight };
}

// 64 is the bottom of the sticky breadcrumb row. The headings not yet reached sit at
// 65 rather than far below, so a threshold that drifted either way fails.
const passed = (heads, i) => heads.forEach((h, n) => rect(h, { top: n <= i ? 64 : 65 }));

test('one link is highlighted, and it is the heading being read', async () => {
  const { heads, links, scroll } = await railPage();
  passed(heads, 9);
  scroll();
  const active = links.filter((a) => a.classList.contains('active'));
  assert.equal(active.length, 1, 'exactly one');
  assert.equal(links.indexOf(active[0]), 9, 'the last heading to have passed the top');
});

test('moving back up moves the highlight back', async () => {
  const { heads, links, rail, scroll, highlight } = await railPage();
  passed(heads, 9);
  scroll();
  passed(heads, 3);
  scroll();
  assert.equal(highlight(), 3);
  passed(heads, -1);
  const scrolledTo = rail.scrollTop;
  scroll();
  assert.equal(highlight(), -1, 'above the first heading, nothing is highlighted');
  assert.equal(rail.scrollTop, scrolledTo, 'and no highlight means no move to make');
});

test('the rail scrolls when its highlight is below the fold', async () => {
  const { heads, rail, scroll } = await railPage({ headings: 12, clientHeight: 100 });
  passed(heads, 9);
  scroll();
  assert.equal(rail.scrollTop, 152,
    'the highlight is brought to the bottom edge; five links of lead cannot fit');
});

test('the rail stays put when the highlight is already in sight', async () => {
  const { heads, rail, scroll } = await railPage({ headings: 12, clientHeight: 400 });
  passed(heads, 1);
  scroll();
  assert.equal(rail.scrollTop, 0, 'nothing moved, because the link was already where it belongs');
});

test('when the lead above and below cannot both fit, the narrow window wins', async () => {
  // A 100-tall rail cannot hold five links of lead either way around a 24-tall link,
  // so the spy falls back to the narrow window: the highlighted link's top less the
  // 12 it keeps clear. At the top of the rail that target is negative, and a browser
  // clamps it to 0, so the rail stays put; jsdom stores what it is given.
  const { heads, rail, links, scroll } = await railPage({ headings: 12, clientHeight: 100 });
  passed(heads, 0);
  scroll();
  assert.equal(rail.scrollTop, links[0].getBoundingClientRect().top - 12);
});

test('the highlight keeps lead above and below when the rail can hold both', async () => {
  const { heads, rail, links, scroll } = await railPage({ headings: 20, clientHeight: 300 });
  passed(heads, 12);
  scroll();
  assert.equal(links[17].getBoundingClientRect().bottom - rail.scrollTop, 300,
    'the fifth link below ends at the rail edge');
  assert.ok(links[7].getBoundingClientRect().top - rail.scrollTop >= 0,
    'and the fifth link above is still inside the rail');
});

test('a page with no rail runs nothing', async () => {
  const dom = await loadPage('index.html');
  runScript(dom.window, 'toc.js');
  assert.ok(true, 'no rail, no spy');
});
