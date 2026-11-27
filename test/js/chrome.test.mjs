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

  const meta = document.querySelector('meta[name="color-scheme"]');
  const btn = document.querySelector('.theme-toggle');
  assert.equal(btn.textContent, '\u25d0 system', 'the choice a reader never made is the system one');
  assert.equal(btn.title, 'theme: system \u2014 click for light');

  click(window, btn);
  assert.equal(window.localStorage.theme, 'light');
  assert.equal(meta.content, 'light', 'the meta is what docs.css reads');
  assert.equal(btn.textContent, '\u2600 light');

  click(window, btn);
  assert.equal(window.localStorage.theme, 'dark');
  assert.equal(meta.content, 'dark');
  assert.equal(btn.textContent, '\u263e dark');

  click(window, btn);
  assert.equal(window.localStorage.theme, undefined, 'system is the absence of a stored choice');
  assert.equal(meta.content, 'light dark', 'and the meta goes with it');
  assert.equal(btn.textContent, '\u25d0 system');
});

test('the control paints what a previous page already stored', async () => {
  const { window } = await loadPage('index.html');
  window.localStorage.theme = 'dark';
  // The head's pre-paint script runs on every navigation, so re-run it as the
  // browser would for this reader's next page load.
  const inline = /<script>(document\.querySelector\('meta\[name="color-scheme"\]'\)[\s\S]*?)<\/script>/.exec(
    window.document.documentElement.outerHTML,
  );
  window.eval(inline[1]);
  runScript(window, 'site.js');
  assert.equal(window.document.querySelector('meta[name="color-scheme"]').content, 'dark');
  assert.equal(window.document.querySelector('.theme-toggle').textContent, '\u263e dark');
});

test('a choice made in another tab reaches this one as a storage event', async () => {
  const { window } = await loadPage('index.html');
  const { document } = window;
  runScript(window, 'site.js');

  const storage = new window.StorageEvent('storage', { key: 'theme', newValue: 'dark' });
  window.dispatchEvent(storage);
  assert.equal(document.querySelector('meta[name="color-scheme"]').content, 'dark',
    'every open tab keeps the same scheme');
  assert.equal(document.querySelector('.theme-toggle').textContent, '\u263e dark',
    'and the control follows');

  window.dispatchEvent(new window.StorageEvent('storage', { key: 'theme', newValue: null }));
  assert.equal(document.querySelector('meta[name="color-scheme"]').content, 'light dark',
    'and back to the system when the choice is removed');
});

test('the head applies a stored theme before the body is built', async () => {
  // Read out of the built page rather than copied here, so the two cannot drift.
  const dom = await loadPage('index.html');
  const { window } = dom;
  const inline = /<script>(document\.querySelector\('meta\[name="color-scheme"\]'\)[\s\S]*?)<\/script>/.exec(
    window.document.documentElement.outerHTML,
  );
  assert.ok(inline, 'the built page still carries the pre-paint theme script');

  window.localStorage.theme = 'dark';
  window.eval(inline[1]);
  assert.equal(window.document.querySelector('meta[name="color-scheme"]').content, 'dark',
    'a reader who chose dark must not see the light theme flash first');

  const fresh = await loadPage('index.html');
  fresh.window.eval(inline[1]);
  assert.equal(fresh.window.document.querySelector('meta[name="color-scheme"]').content, 'light dark',
    'and a reader who chose nothing gets the system theme');
});

// jsdom has no popover API and no IntersectionObserver: the stubs stand in for
// the two, and report hands the test the callback the drawer will use to learn
// that its sheet has left.
function stubPopover(window) {
  const calls = [];
  window.HTMLElement.prototype.showPopover = function () { calls.push('show'); };
  window.HTMLElement.prototype.hidePopover = function () { calls.push('hide'); };
  return calls;
}
function stubObserver(window, onCallback) {
  window.IntersectionObserver = class {
    constructor(cb) { onCallback(cb); }
    observe() {}
  };
}
const drawerPage = async ({ script = true } = {}) => {
  const { window } = await loadPage('index.html');
  const { document } = window;
  const calls = stubPopover(window);
  let report;
  stubObserver(window, (cb) => { report = cb; });
  if (script) runScript(window, 'site.js');
  const sidebar = document.getElementById('sidebar');
  // The sheet has scrolled out of the drawer; the observer's report is what
  // takes the popover down after the slide-out.
  const dismissed = () => report([{ intersectionRatio: 0 }]);
  return { window, document, sidebar, calls, dismissed };
};

test('the mobile nav opens and closes', async () => {
  const { window, document, sidebar, calls, dismissed } = await drawerPage();
  const btn = document.querySelector('.nav-toggle');
  const sheet = sidebar.querySelector('.sidebar-sheet');
  const main = document.querySelector('main');

  click(window, btn);
  assert.deepEqual(calls, ['show'], 'opening shows the popover');
  assert.equal(sidebar.getAttribute('role'), 'dialog', 'as a drawer it is a dialog');
  assert.equal(sidebar.getAttribute('aria-modal'), 'true', 'and modal, which a popover is not by itself');
  assert.ok(main.hasAttribute('inert'), 'the page behind is inert');
  assert.equal(btn.getAttribute('aria-expanded'), 'true', 'the state is announced, not just styled');
  assert.equal(sheet.getAttribute('tabindex'), '-1', 'the sheet takes focus for the keyboard reader');

  dismissed();
  assert.deepEqual(calls, ['show', 'hide'], 'and closing hides it once the sheet is off-screen');
  assert.ok(!sidebar.hasAttribute('role'), 'back to plain navigation');
  assert.ok(!sidebar.hasAttribute('aria-modal'));
  assert.ok(!main.hasAttribute('inert'));
  assert.equal(btn.getAttribute('aria-expanded'), 'false');
  assert.equal(document.activeElement, btn, 'focus returns to the trigger');
  assert.equal(sheet.getAttribute('tabindex'), null, 'and the focus hook goes with the state');
});

test('the nav closes however the reader leaves it', async () => {
  const { window, document, sidebar, dismissed } = await drawerPage();
  const btn = document.querySelector('.nav-toggle');
  const sheet = sidebar.querySelector('.sidebar-sheet');
  const open = () => sidebar.hasAttribute('aria-modal');
  const clickBtn = () => click(window, btn);

  clickBtn();
  click(window, sidebar.querySelector('.sidebar-scroller'));
  dismissed();
  assert.ok(!open(), 'a tap on the dim, anywhere but the sheet, dismisses it');

  clickBtn();
  key(window, document, 'Escape');
  dismissed();
  assert.ok(!open(), 'Escape dismisses it');

  clickBtn();
  click(window, sheet.querySelector('.navlist a').closest('li'));
  assert.ok(open(), 'a tap in the sheet leaves it open');
  dismissed();
  assert.ok(!open(), 'and the leave still happens when the sheet goes');
});

test('crossing to the wide side ends the drawer outright', async () => {
  // matchMedia must be in place before the script reads it, so the script runs last.
  const { window, document, sidebar, calls, dismissed } = await drawerPage({ script: false });
  let wideChange;
  window.matchMedia = (q) => ({
    media: q,
    matches: false,
    addEventListener: (t, fn) => { if (t === 'change') wideChange = fn; },
    removeEventListener: () => {},
  });
  runScript(window, 'site.js');

  click(window, document.querySelector('.nav-toggle'));
  assert.ok(sidebar.hasAttribute('aria-modal'));
  wideChange({ matches: true });
  assert.deepEqual(calls, ['show', 'hide'], 'no slide-out to observe: the scroller is gone there');
  assert.ok(!sidebar.hasAttribute('aria-modal'));
  assert.equal(document.querySelector('.nav-toggle').getAttribute('aria-expanded'), 'false');
  dismissed(); // the later observer report is noise, not a second close
});

test('chrome absent from a page is not an error', async () => {
  const { window } = await loadPage('index.html');
  window.document.querySelector('.theme-toggle').remove();
  window.document.querySelector('.nav-toggle').remove();
  runScript(window, 'site.js'); // each half gives up when its control is missing
  assert.ok(true, 'a control can be absent from a page');
});
