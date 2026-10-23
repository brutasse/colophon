// The diagram viewer: what it builds around a rendered diagram, and the arithmetic
// behind zoom, reset, expand, and pan.

import test from 'node:test';
import assert from 'node:assert/strict';
import { loadPage, runScript, rect, svgDiagram } from './page.mjs';

// jsdom lays nothing out, so the svg reports the width its own styles ask for.
function laidOut(svg) {
  const px = (s) => parseFloat(s) || 0;
  svg.getBoundingClientRect = () => {
    const authored = /max-width:\s*([\d.]+)px/.exec(svg.getAttribute('style') || '');
    const width = px(svg.style.width) || (authored ? px(authored[1]) : 0);
    return {
      width, height: width / 2, top: 0, bottom: width / 2, left: 0, right: width,
      x: 0, y: 0, toJSON() {},
    };
  };
  return svg;
}

async function diagramPage({ count = 1, render = true, bbox = false, authored = 'max-width: 400px;' } = {}) {
  const dom = await loadPage('guide/second.html');
  const { window } = dom;
  const { document } = window;
  const article = document.querySelector('article');
  const first = article.querySelector('pre.mermaid');
  for (let i = 1; i < count; i++) article.appendChild(first.cloneNode(true));
  const pres = [...article.querySelectorAll('pre.mermaid')];
  const svgs = pres.map((pre) => {
    const svg = render ? svgDiagram(pre, { width: 400, height: 200, authored, bbox }) : null;
    rect(pre, { top: 200, bottom: 400, height: 200 });
    return svg && laidOut(svg);
  });
  window.mermaid = { initialize() {} }; // startOnLoad already ran in the real page
  if (!render) window.setTimeout = () => 0; // no retry loop waiting 15s for an SVG
  runScript(window, 'mermaid-viewer.js');
  return { dom, window, document, article, pres, svgs };
}

const views = (document) => [...document.querySelectorAll('.mm-view')];
const chip = (view) => view.querySelector('.mm-bar');
const control = (view, label) => [...view.querySelectorAll('.mm-btn')].find((b) => b.getAttribute('aria-label') === label);
const click = (window, el) => el.dispatchEvent(new window.MouseEvent('click', { bubbles: true, cancelable: true }));

test('a rendered diagram gets a scroll box and a control chip', async () => {
  const { document, pres } = await diagramPage();
  const wrapped = views(document);
  assert.equal(wrapped.length, 1, 'one diagram, one box');
  const view = wrapped[0];
  assert.equal(view.parentNode, pres[0].parentNode.parentNode, 'the box stands where the pre stood');
  assert.ok(view.contains(pres[0]), 'the diagram is inside the box');
  assert.deepEqual([...chip(view).children].map((b) => b.getAttribute('aria-label')),
    ['zoom out', 'zoom in', 'reset zoom', 'expand'], 'the four controls, in that order');
  assert.equal(pres[0].tabIndex, 0, 'a scroll box a keyboard cannot reach is a trap');
  assert.equal(pres[0].getAttribute('role'), 'region');
  assert.match(pres[0].getAttribute('aria-label'), /scrollable/);
});

test('running the viewer again does not wrap a wrapped diagram', async () => {
  const { window, document } = await diagramPage();
  runScript(window, 'mermaid-viewer.js');
  assert.equal(views(document).length, 1, 'one box per diagram, not one per run');
  assert.equal(views(document)[0].querySelectorAll('.mm-bar').length, 1);
});

test('an undrawn diagram is left alone', async () => {
  const { document } = await diagramPage({ render: false });
  assert.equal(views(document).length, 0, 'nothing to size until mermaid has drawn it');
});

test('a drawing with no viewBox is measured by its drawn extent', async () => {
  const { window, document, svgs } = await diagramPage({ bbox: true });
  click(window, control(views(document)[0], 'zoom in'));
  assert.equal(svgs[0].style.width, '500px', 'the same arithmetic, from getBBox instead');
});

test('zoom sizes the svg and keeps the drawn ratio', async () => {
  const { window, document, svgs } = await diagramPage();
  const view = views(document)[0];
  const svg = svgs[0];

  click(window, control(view, 'zoom in'));
  assert.equal(svg.style.maxWidth, "none", "mermaid's cap on width came off");
  assert.equal(svg.style.width, '500px', 'the 400 it was shown at, times 1.25');
  assert.equal(svg.style.height, '250px', 'height follows the 400:200 it was drawn at');

  click(window, control(view, 'zoom out'));
  assert.equal(svg.style.width, '400px', 'and back');
});

test('zoom stops at the limits rather than running away', async () => {
  const { window, document, svgs } = await diagramPage();
  const view = views(document)[0];
  const in_ = control(view, 'zoom in');
  const out = control(view, 'zoom out');

  for (let i = 0; i < 40; i++) click(window, in_);
  assert.equal(svgs[0].style.width, `${8 * 400}px`, 'eight times the drawn width is as wide as it goes');
  for (let i = 0; i < 80; i++) click(window, out);
  assert.equal(svgs[0].style.width, `${0.25 * 400}px`, 'and a quarter as small as it gets');
});

test('reset puts back the style mermaid authored, width cap included', async () => {
  const { window, document, svgs } = await diagramPage();
  const view = views(document)[0];
  const svg = svgs[0];
  const authored = svg.getAttribute('style');

  click(window, control(view, 'zoom in'));
  assert.notEqual(svg.getAttribute('style'), authored, 'zooming changed it');
  click(window, control(view, 'reset zoom'));
  assert.equal(svg.getAttribute('style'), authored,
    'verbatim, because dropping the cap would let the drawing grow past its size');
});

test('reset before any zoom leaves the diagram alone', async () => {
  const { window, document, svgs } = await diagramPage();
  const view = views(document)[0];
  const authored = svgs[0].getAttribute('style');
  click(window, control(view, 'reset zoom'));
  assert.equal(svgs[0].getAttribute('style'), authored);
});

test('only one diagram is on the screen at a time', async () => {
  const { window, document } = await diagramPage({ count: 2 });
  const [a, b] = views(document);
  // Captured before expanding: expanding is what renames the label they are found by.
  const expandA = control(a, 'expand');
  const expandB = control(b, 'expand');

  click(window, expandA);
  assert.ok(a.classList.contains('mm-full'));
  assert.equal(expandA.getAttribute('aria-label'), 'collapse');
  assert.equal(expandA.textContent, '\u2715');
  assert.ok(document.body.classList.contains('mm-fullscreen'));

  click(window, expandB);
  assert.ok(!a.classList.contains('mm-full'),
    'two panels inset 1rem are exactly coincident, so the one under this is unreachable');
  assert.ok(b.classList.contains('mm-full'));
  assert.equal(expandA.getAttribute('aria-label'), 'expand', 'its label went with it');
  assert.ok(document.body.classList.contains('mm-fullscreen'), 'a panel is still covering the page');
});;

test('Escape puts the expanded diagram back', async () => {
  const { window, document } = await diagramPage();
  const view = views(document)[0];
  click(window, control(view, 'expand'));

  document.dispatchEvent(new window.KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
  assert.ok(!view.classList.contains('mm-full'));
  assert.ok(!document.body.classList.contains('mm-fullscreen'), 'the body class follows the panels');
  assert.equal(control(view, 'expand').getAttribute('aria-label'), 'expand');

  document.dispatchEvent(new window.KeyboardEvent('keydown', { key: 'x', bubbles: true }));
  assert.ok(!view.classList.contains('mm-full'), 'another key changes nothing');
});

test('the box drags to pan', async () => {
  const { window, document, pres } = await diagramPage();
  const view = views(document)[0];
  const pre = pres[0];
  pre.scrollLeft = 0;
  pre.scrollTop = 0;

  const pointer = (type, x, y) => new window.PointerEvent(type, {
    bubbles: true, cancelable: true, button: 0, clientX: x, clientY: y,
  });
  pre.dispatchEvent(pointer('pointerdown', 100, 50));
  pre.dispatchEvent(pointer('pointermove', 60, 30));
  assert.equal(pre.scrollLeft, 40, 'the box moved with the pointer');
  assert.equal(pre.scrollTop, 20);

  pre.dispatchEvent(pointer('pointerup', 60, 30));
  pre.dispatchEvent(pointer('pointermove', 10, 10));
  assert.equal(pre.scrollLeft, 40, 'and stopped when the pointer came up');
});

test('a right-button press does not start a pan', async () => {
  const { window, pres } = await diagramPage();
  const pre = pres[0];
  pre.scrollLeft = 0;
  pre.dispatchEvent(new window.PointerEvent('pointerdown', { bubbles: true, button: 2, clientX: 100 }));
  pre.dispatchEvent(new window.PointerEvent('pointermove', { bubbles: true, button: 2, clientX: 20 }));
  assert.equal(pre.scrollLeft, 0, 'only the left button drags');
});

test('ctrl+wheel zooms a diagram, plain wheel scrolls it', async () => {
  const { window, document, svgs } = await diagramPage();
  const pre = views(document)[0].querySelector('pre.mermaid');
  const svg = svgs[0];

  const plain = new window.WheelEvent('wheel', { bubbles: true, cancelable: true, deltaY: -100 });
  pre.dispatchEvent(plain);
  assert.equal(plain.defaultPrevented, false, 'a plain wheel still scrolls the box');
  assert.equal(svg.style.width, '', 'and did not resize it');

  const pinch = new window.WheelEvent('wheel', { bubbles: true, cancelable: true, ctrlKey: true, deltaY: -100 });
  pre.dispatchEvent(pinch);
  assert.equal(pinch.defaultPrevented, true, 'the browser page-zoom is not what a pinch means here');
  assert.equal(svg.style.width, '440px', 'the wheel zoomed the diagram instead');
});

test('a page with no diagram carries no viewer of its own', async () => {
  const dom = await loadPage('index.html');
  const { window } = dom;
  window.mermaid = { initialize() {} };
  window.setTimeout = () => 0;
  runScript(window, 'mermaid-viewer.js');
  assert.equal(views(window.document).length, 0);
});
