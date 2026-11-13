// The URL bar's engine lands a search on any page with ?q=, and the page answers
// it by opening the search modal — the same overlay the sidebar trigger opens —
// with the query pre-filled. Every search the user makes is written to the URL,
// so a search is shareable as a link, and closing the modal clears it. These
// tests serve the built site over loopback HTTP and load it with its real
// scripts, so the template's handler, the modal it opens, and the URL it leaves
// behind are all real. An HTTP origin is not a luxury: history.replaceState
// refuses the file: origin jsdom would otherwise use. Two things stay jsdom
// limits, shimmed or accepted: the bundle loads its search core with a dynamic
// import() the jsdom realm cannot perform, so the result list stays empty, and
// the <dialog> methods are stood in, so the modal's open/close drive the same
// events a browser would fire.

import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { JSDOM, VirtualConsole } from 'jsdom';
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { repoRoot } from './page.mjs';

const siteRoot = path.join(repoRoot, '_build', 'site');
if (!existsSync(siteRoot)) {
  throw new Error(`no built site at ${siteRoot}; run
  go run . build --config testdata/site/colophon.conf --out _build/site`);
}

const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.json': 'application/json',
};

// serveSite answers the loopback requests jsdom makes for the built site's
// files. Test-only: nothing outside this file reaches it.
function serveSite() {
  return http.createServer((req, res) => {
    const rel = path.normalize(decodeURIComponent(req.url.split('?')[0])).replace(/^\/+/, '');
    const file = path.join(siteRoot, rel || 'index.html');
    if (!file.startsWith(siteRoot)) {
      res.writeHead(403);
      return res.end();
    }
    try {
      const b = readFileSync(file);
      res.writeHead(200, { 'Content-Type': MIME[path.extname(file)] || 'application/octet-stream' });
      res.end(b);
    } catch {
      res.writeHead(404);
      res.end();
    }
  });
}

const server = serveSite();
await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
const origin = `http://127.0.0.1:${server.address().port}`;
test.after(() => new Promise((resolve) => server.close(resolve)));

// loadPage loads a built page the way a reader's browser would, running its
// scripts. The 300 ms settle is past the load event, which is past the handler:
// it runs on DOMContentLoaded, synchronously.
async function loadPage(urlPath) {
  const dom = await JSDOM.fromURL(origin + urlPath, {
    runScripts: 'dangerously',
    resources: 'usable',
    pretendToBeVisual: true,
    beforeParse(window) {
      window.HTMLDialogElement.prototype.showModal = function () {
        this.setAttribute('open', '');
        const input = this.querySelector('input');
        if (input) {
          input.focus(); // a real browser moves focus into the dialog
        }
      };
      window.HTMLDialogElement.prototype.close = function () {
        this.removeAttribute('open');
        this.dispatchEvent(new window.Event('close'));
      };
    },
  });
  const { window } = dom;
  await new Promise((resolve) => {
    if (window.document.readyState === 'complete') {
      setTimeout(resolve, 300);
    } else {
      window.addEventListener('load', () => setTimeout(resolve, 300));
    }
  });
  return dom;
}

const queryOf = (window) => new URLSearchParams(window.location.search).get('q');

test('a ?q= opens the search modal, pre-filled', async () => {
  const { window } = await loadPage('/index.html?q=shared+search');

  const modal = window.document.querySelector('pagefind-modal');
  assert.equal(modal.isOpen, true, 'the modal is open');
  const input = modal.querySelector('input[type=search]');
  assert.equal(input.value, 'shared search', 'the query is in the input');
  assert.equal(window.document.activeElement, input, 'the input has focus');
  assert.equal(queryOf(window), 'shared search', 'the URL keeps the query');
});

test('a search made in the modal lands in the URL', async () => {
  const { window } = await loadPage('/index.html');
  const modal = window.document.querySelector('pagefind-modal');
  const instance = window.PagefindComponents.getInstanceManager().getInstance('default');

  modal.open();
  instance.triggerSearch('typed term'); // what the modal's input does after its debounce

  assert.equal(queryOf(window), 'typed term', 'the search is in the URL, ready to share');
  assert.equal(window.location.pathname, '/index.html', 'and the page itself is unchanged');
});

test('closing the search takes the query off the URL', async () => {
  const { window } = await loadPage('/index.html?q=closed');
  const modal = window.document.querySelector('pagefind-modal');
  assert.equal(modal.isOpen, true);

  modal.close(); // reset-on-close triggers the clearing search

  assert.equal(modal.isOpen, false);
  assert.equal(window.location.search, '', 'the URL no longer carries the search');
});

test('no ?q= leaves the page as it is', async () => {
  const { window } = await loadPage('/index.html');

  assert.equal(window.document.querySelector('pagefind-modal').isOpen, false, 'nothing opened');
  assert.ok(window.document.querySelector('article').children.length > 0, 'the article kept its content');
  assert.equal(window.location.search, '');
});

test('a window without the bundle is survivable', async () => {
  // The bundle's tag is the only thing keeping the search alive, so the test
  // page is the built one with that tag cut out. The handler runs at parse,
  // finds no PagefindComponents, and must leave the page exactly as found.
  const html = readFileSync(path.join(siteRoot, 'index.html'), 'utf8').replace(
    /<script defer src="[^"]*pagefind-component-ui\.js"><\/script>\n?/,
    '',
  );
  const vc = new VirtualConsole();
  const pageErrors = [];
  vc.on('jsdomError', (e) => pageErrors.push(e.message));
  const { window } = new JSDOM(html, {
    runScripts: 'dangerously',
    url: 'http://localhost/index.html?q=anything',
    virtualConsole: vc,
  });

  assert.ok(!('PagefindComponents' in window), 'the bundle tag really is gone');
  assert.ok(!window.document.querySelector('dialog[open]'), 'nothing opened');
  assert.ok(window.document.querySelector('article').children.length > 0, 'the article kept its content');
  assert.equal(window.location.search, '?q=anything', 'and nothing the bundle owns ran to touch the URL');
  assert.deepEqual(pageErrors, [], 'the handler threw nothing');
});
