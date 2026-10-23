// The theme control and the mobile navigation, plus the one inline script the
// template keeps in the head because it has to run before the first paint.

import test from 'node:test';
import assert from 'node:assert/strict';
import { loadPage, runScript } from './page.mjs';

const click = (window, el) => el.dispatchEvent(new window.MouseEvent('click', { bubbles: true, cancelable: true }));
const key = (window, target, k) => target.dispatchEvent(new window.KeyboardEvent('keydown', { key: k, bubbles: true }));

test('the theme control cycles system, light, dark, and back', async () => {
  const { window } = await loadPage('index.html');
  const { document } = window;
  runScript(window, 'site.js');

  const btn = document.querySelector('.theme-toggle');
  assert.equal(btn.textContent, '\u25d0 system', 'the choice a reader never made is the system one');
  assert.equal(btn.title, 'theme: system \u2014 click for light');

  click(window, btn);
  assert.equal(window.localStorage.theme, 'light');
  assert.equal(document.documentElement.dataset.theme, 'light', 'the attribute is what docs.css reads');
  assert.equal(btn.textContent, '\u2600 light');

  click(window, btn);
  assert.equal(window.localStorage.theme, 'dark');
  assert.equal(btn.textContent, '\u263e dark');

  click(window, btn);
  assert.equal(window.localStorage.theme, undefined, 'system is the absence of a stored choice');
  assert.equal(document.documentElement.dataset.theme, undefined, 'and the attribute goes with it');
  assert.equal(btn.textContent, '\u25d0 system');
});

test('the control paints what a previous page already stored', async () => {
  const { window } = await loadPage('index.html');
  window.localStorage.theme = 'dark';
  runScript(window, 'site.js');
  assert.equal(window.document.querySelector('.theme-toggle').textContent, '\u263e dark');
});

test('the head applies a stored theme before the body is built', async () => {
  // Read out of the built page rather than copied here, so the two cannot drift.
  const dom = await loadPage('index.html');
  const { window } = dom;
  const inline = /<script>(if \(localStorage\.theme[\s\S]*?)<\/script>/.exec(window.document.documentElement.outerHTML);
  assert.ok(inline, 'the built page still carries the pre-paint theme script');

  window.localStorage.theme = 'dark';
  window.eval(inline[1]);
  assert.equal(window.document.documentElement.dataset.theme, 'dark',
    'a reader who chose dark must not see the light theme flash first');

  const fresh = await loadPage('index.html');
  fresh.window.eval(inline[1]);
  assert.equal(fresh.window.document.documentElement.dataset.theme, undefined,
    'and a reader who chose nothing gets the system theme');
});

test('the mobile nav opens and closes', async () => {
  const { window } = await loadPage('index.html');
  const { document } = window;
  runScript(window, 'site.js');

  const btn = document.querySelector('.nav-toggle');
  click(window, btn);
  assert.ok(document.body.classList.contains('nav-open'));
  assert.equal(btn.getAttribute('aria-expanded'), 'true', 'the state is announced, not just styled');

  click(window, btn);
  assert.ok(!document.body.classList.contains('nav-open'));
  assert.equal(btn.getAttribute('aria-expanded'), 'false');
});

test('the nav closes however the reader leaves it', async () => {
  const { window } = await loadPage('index.html');
  const { document } = window;
  runScript(window, 'site.js');
  const open = () => document.body.classList.contains('nav-open');
  const clickBtn = () => click(window, document.querySelector('.nav-toggle'));

  clickBtn();
  click(window, document.querySelector('.scrim'));
  assert.ok(!open(), 'a tap beside the panel dismisses it');

  clickBtn();
  key(window, document, 'Escape');
  assert.ok(!open(), 'Escape dismisses it');

  clickBtn();
  click(window, document.querySelector('#sidebar .navlist a'));
  assert.ok(!open(), 'a tap on a link lands the reader on the page they asked for');

  clickBtn();
  click(window, document.querySelector('#sidebar .navlist a').closest('li'));
  assert.ok(open(), 'a tap in the panel that is not a link leaves it open');
});

test('chrome absent from a page is not an error', async () => {
  const { window } = await loadPage('index.html');
  window.document.querySelector('.theme-toggle').remove();
  window.document.querySelector('.nav-toggle').remove();
  runScript(window, 'site.js'); // each half gives up when its control is missing
  assert.ok(true, 'a control can be absent from a page');
});
