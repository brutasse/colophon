// The copy control site.js injects into a highlighted fence: what reaches the
// clipboard, and the feedback that says it got there.

import test from 'node:test';
import assert from 'node:assert/strict';
import { loadPage, runScript } from './page.mjs';

// What the renderer put on the page is what the clipboard is compared against, so a
// fence in the fixture never has to be typed a second time here.
const sources = (document) => [...document.querySelectorAll('article .codeblock > pre')]
  .map((pre) => pre.textContent);

// The fixture's guide/first.html holds two fences: an included yaml fragment and a
// bash fence.
async function codePage({ clipboard = true } = {}) {
  const dom = await loadPage('guide/first.html');
  const { window, window: { document } } = dom;
  const copied = [];

  // jsdom provides neither the Clipboard API nor execCommand, so each test states the
  // one the page is meant to find.
  if (clipboard) {
    Object.defineProperty(window.navigator, 'clipboard', {
      configurable: true,
      value: { writeText: (text) => { copied.push(text); return Promise.resolve(); } },
    });
  } else {
    document.execCommand = (name) => {
      // The helper textarea is the only one on the page, and it holds the selection.
      copied.push([name, document.querySelector('textarea').value]);
      return true;
    };
  }

  const timers = [];
  window.setTimeout = (fn) => { timers.push(fn); return 0; }; // the revert, run by hand
  runScript(window, 'site.js');
  const chips = [...document.querySelectorAll('.code-copy')];
  const click = (chip) => chip.dispatchEvent(new window.MouseEvent('click', { bubbles: true }));
  // A Clipboard write settles in a microtask, so a click needs one turn of the event
  // loop before its effect can be read.
  const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
  return { document, chips, copied, timers, click, settle };
}

test('every fence gets one control, hung beside the pre it copies', async () => {
  const { document, chips } = await codePage();
  assert.equal(chips.length, 2, 'one per fence');
  for (const chip of chips) {
    assert.equal(chip.parentNode.className, 'codeblock');
    assert.equal(chip.type, 'button', 'a button, not a link that would navigate away');
    assert.equal(chip.querySelector('svg').getAttribute('aria-hidden'), 'true',
      'the icon is decoration, so the label is the accessible name');
    assert.equal(chip.textContent, 'Copy');
  }
});

test('a diagram gets no copy control', async () => {
  const dom = await loadPage('guide/second.html');
  runScript(dom.window, 'site.js');
  assert.equal(dom.window.document.querySelectorAll('.code-copy').length, 0,
    'a rendered diagram is not code a reader would paste');
});

test('what is copied is the fence, not the rendered markup', async () => {
  const { document, chips, copied, click, settle } = await codePage();
  chips.forEach(click);
  await settle();
  assert.deepEqual(copied, sources(document),
    'the chroma spans leave no trace, and no trailing newline arrives');
});

test('the label says Copied and then goes back', async () => {
  const { chips, click, timers, settle } = await codePage();
  click(chips[0]);
  await settle();
  assert.equal(chips[0].textContent, 'Copied', 'the message is the label alone, so the icon stays');
  assert.equal(timers.length, 1, 'one revert was scheduled');
  timers[0]();
  assert.equal(chips[0].textContent, 'Copy');
});

test('a page with no Clipboard API still copies', async () => {
  const { document, chips, copied, click } = await codePage({ clipboard: false });
  click(chips[1]);
  assert.deepEqual(copied, [['copy', sources(document)[1]]],
    'a selected textarea and execCommand is what a plain http origin has');
  assert.equal(document.querySelectorAll('textarea').length, 0, 'the helper is taken back out');
});
