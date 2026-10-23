// The scripts under test are the real assets/*.js, and the markup they are handed is
// the built fixture site in _build/site: a hook renamed in the template is a hook
// missing here, not a fixture that kept the old name.
//
// jsdom has no layout. Rects come back zero and scroll positions are stored rather
// than clamped, so every test states its geometry with rect() and measure().

import { JSDOM } from 'jsdom';
import { existsSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const here = path.dirname(fileURLToPath(import.meta.url));
export const repoRoot = path.resolve(here, '..', '..');
export const siteRoot = path.join(repoRoot, '_build', 'site');

function requireBuilt(rel) {
  if (!existsSync(siteRoot)) {
    throw new Error(`no built site at ${siteRoot}; run
    go run . build --config testdata/site/colophon.conf --out _build/site`);
  }
  const p = path.join(siteRoot, rel);
  if (!existsSync(p)) {
    throw new Error(`no ${rel} in the built site; rebuild it`);
  }
  return p;
}

// The url is what makes localStorage usable: jsdom gives no storage to an opaque
// origin, and a file: page is one.
export async function loadPage(rel) {
  const dom = await JSDOM.fromFile(requireBuilt(rel), {
    runScripts: 'outside-only',
    url: 'http://localhost/',
  });
  return dom;
}

export function runScript(window, name) {
  const src = readFileSync(path.join(repoRoot, 'assets', name), 'utf8');
  window.eval(src);
  return window;
}

export function rect(el, { top = 0, bottom = top, left = 0, right = left, width = 0, height = bottom - top } = {}) {
  el.getBoundingClientRect = () => ({
    x: left, y: top, top, bottom, left, right, width, height, toJSON() {},
  });
  return el;
}

export function measure(el, { clientHeight, scrollHeight } = {}) {
  for (const [k, v] of Object.entries({ clientHeight, scrollHeight })) {
    if (v === undefined) continue;
    Object.defineProperty(el, k, { value: v, configurable: true, writable: true });
  }
  return el;
}

// svgDiagram stands in for what mermaid leaves in the pre: the svg it drew, with the
// viewBox that sized it and the inline style it authored. bbox: true has no viewBox,
// which is the other branch the viewer reads a drawing's extent by.
export function svgDiagram(pre, { width = 400, height = 200, authored = `max-width: ${width}px;`, bbox = false } = {}) {
  const { document } = pre.ownerDocument.defaultView;
  // createElement('svg') in an HTML document makes an element with no viewBox.
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  if (bbox) {
    svg.getBBox = () => ({ x: 0, y: 0, width, height });
  } else {
    svg.setAttribute('viewBox', `0 0 ${width} ${height}`);
  }
  if (authored) svg.setAttribute('style', authored);
  pre.appendChild(svg);
  return svg;
}
