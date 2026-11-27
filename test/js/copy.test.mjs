// The copy control site.js injects into a highlighted fence: what reaches the
// clipboard, the feedback that says it got there, and the state that keeps the
// control reachable.

import test from 'node:test';
import assert from 'node:assert/strict';
import { loadPage, runScript } from './page.mjs';

// What the renderer put on the page is what the clipboard is compared against, so a
// fence in the fixture never has to be typed a second time here.
const sources = (document) => [...document.querySelectorAll('article .codeblock > pre')]
  .map((pre) => pre.textContent);

// The fixture's guide/first.html holds two fences: an included yaml fragment and a
// bash fence.
async function codePage({ clipboard = true, reject = false, execOk = true } = {}) {
  const dom = await loadPage('guide/first.html');
  const { window, window: { document } } = dom;
  const copied = [];

  // jsdom provides neither the Clipboard API nor execCommand, so each test states
  // the state of the page it is meant to run on.
  if (clipboard) {
    Object.defineProperty(window.navigator, 'clipboard', {
      configurable: true,
      value: {
        writeText: (text) => {
          copied.push(text);
          return reject ? Promise.reject(new Error('denied')) : Promise.resolve();
        },
      },
    });
  }
  // Only reachable when the Clipboard path is absent or rejects; the helper
  // textarea is the only one on the page, and it holds the selection.
  document.execCommand = (name) => {
    copied.push([name, document.querySelector('textarea').value]);
    return execOk;
  };

  const pending = new Map();
  let nextId = 1;
  window.setTimeout = (fn) => { pending.set(nextId, fn); return nextId++; }; // run by hand
  window.clearTimeout = (id) => { pending.delete(id); };
  runScript(window, 'site.js');
  const chips = [...document.querySelectorAll('.code-copy')];
  const buttons = [...document.querySelectorAll('.code-copy-btn')];
  const click = (button) => button.dispatchEvent(new window.MouseEvent('click', { bubbles: true }));
  // A Clipboard write settles in a microtask, so a click needs one turn of the event
  // loop before its effect can be read.
  const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
  return { document, chips, buttons, copied, pending, click, settle };
}

test('every fence gets one control, hung beside the pre it copies', async () => {
  const { chips, buttons } = await codePage();
  assert.equal(chips.length, 2, 'one per fence');
  chips.forEach((chip, i) => {
    assert.equal(chip.parentNode.className, 'codeblock');
    const button = buttons[i];
    assert.equal(button.parentNode, chip, 'the chip is the bubble\'s positioning context');
    assert.equal(button.type, 'button', 'a button, not a link that would navigate away');
    assert.equal(button.getAttribute('aria-label'), 'Copy code',
      'the icon-only button names what it copies');
    assert.equal(button.querySelector('svg').getAttribute('aria-hidden'), 'true',
      'the icon is decoration, so the aria-label is the accessible name');
    const tip = button.nextElementSibling;
    assert.equal(chip.lastElementChild, tip);
    assert.equal(tip.getAttribute('role'), 'status', 'the bubble is the live region');
    assert.equal(tip.textContent, '', 'and it stays empty until a copy');
  });
});

test('a diagram gets no copy control', async () => {
  const dom = await loadPage('guide/second.html');
  runScript(dom.window, 'site.js');
  assert.equal(dom.window.document.querySelectorAll('.code-copy').length, 0,
    'a rendered diagram is not code a reader would paste');
});

test('what is copied is the fence, not the rendered markup', async () => {
  const { document, buttons, copied, click, settle } = await codePage();
  buttons.forEach(click);
  await settle();
  assert.deepEqual(copied, sources(document),
    'the chroma spans leave no trace, and no trailing newline arrives');
});

test('the bubble says Copied, and the cleanup takes it back', async () => {
  const { buttons, click, pending, settle } = await codePage();
  const button = buttons[0];
  const tip = button.nextElementSibling;
  click(button);
  await settle();
  assert.equal(tip.textContent, 'Copied', 'the message lands in the live region');
  assert.ok(button.hasAttribute('data-copied'), 'and the state keeps the chip revealed');
  assert.equal(pending.size, 1, 'one cleanup was scheduled');
  [...pending.values()][0]();
  assert.equal(tip.textContent, '', 'the cleanup empties the bubble for browse mode');
  assert.ok(!button.hasAttribute('data-copied'), 'and drops the state');
});

test('a repeat click restarts the cleanup instead of stacking it', async () => {
  const { buttons, click, pending, settle } = await codePage();
  const button = buttons[0];
  const tip = button.nextElementSibling;
  click(button);
  await settle();
  click(button);
  await settle();
  assert.equal(pending.size, 1, 'the second click restarted the first cleanup');
  assert.equal(tip.textContent, 'Copied\u00a0', 'and the repeated message is told apart');
  [...pending.values()][0]();
  assert.equal(tip.textContent, '', 'and the one cleanup empties the bubble');
});

test('a denied write is saved by the execCommand fallback', async () => {
  const { document, buttons, copied, click, settle } = await codePage({ reject: true });
  click(buttons[1]);
  await settle();
  const fallback = copied.find((entry) => Array.isArray(entry));
  assert.deepEqual(fallback, ['copy', sources(document)[1]],
    'the rejected write falls through to the selected helper');
});

test('a denied write is reported when the fallback fails too', async () => {
  const { buttons, click, settle } = await codePage({ reject: true, execOk: false });
  const button = buttons[0];
  const tip = button.nextElementSibling;
  click(button);
  await settle();
  assert.equal(tip.textContent, 'Copy failed', 'the reader is told, not left guessing');
  assert.ok(button.hasAttribute('data-failed'), 'and the state keeps the chip revealed');
});

test('a page with no Clipboard API still copies', async () => {
  const { document, buttons, copied, click } = await codePage({ clipboard: false });
  click(buttons[1]);
  assert.deepEqual(copied, [['copy', sources(document)[1]]],
    'a selected textarea and execCommand is what a plain http origin has');
  assert.equal(document.querySelectorAll('textarea').length, 0, 'the helper is taken back out');
});

test('a fence wide enough to scroll is named as its own scroll container', async () => {
  const dom = await loadPage('guide/first.html');
  const { window, window: { document } } = dom;
  const [pre, other] = document.querySelectorAll('article .codeblock > pre');
  Object.defineProperty(pre, 'scrollWidth', { value: 500, configurable: true });
  Object.defineProperty(pre, 'clientWidth', { value: 300, configurable: true });
  runScript(window, 'site.js');
  assert.equal(pre.getAttribute('tabindex'), '0', 'the keyboard can reach the scroll range');
  assert.equal(pre.getAttribute('role'), 'region');
  assert.equal(pre.getAttribute('aria-label'), 'Scrollable code block');
  assert.equal(other.hasAttribute('tabindex'), false, 'a fence that fits needs no focus');
});
