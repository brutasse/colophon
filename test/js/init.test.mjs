// mermaid is configured by the viewer, which the deferred tag order leaves running
// after the vendor file.

import test from 'node:test';
import assert from 'node:assert/strict';
import { loadPage, runScript } from './page.mjs';

test('the viewer configures mermaid before it draws anything', async () => {
  const { window } = await loadPage('guide/second.html');
  const calls = [];
  window.mermaid = { initialize: (opts) => calls.push(opts) };
  window.setTimeout = () => 0;
  runScript(window, 'mermaid-viewer.js');

  assert.equal(calls.length, 1, 'configured once');
  // The options object came from this realm, so comparing it whole would fail on its
  // prototype rather than on its contents.
  assert.equal(calls[0].startOnLoad, true);
  assert.equal(calls[0].securityLevel, 'strict',
    'strict, because a diagram is text a reader can put anything in');
  assert.deepEqual(Object.keys(calls[0]).sort(), ['securityLevel', 'startOnLoad'],
    'and nothing else is configured');
});

test('a window without mermaid is survivable', async () => {
  const { window } = await loadPage('index.html');
  window.setTimeout = () => 0;
  runScript(window, 'mermaid-viewer.js');
  assert.ok(!('mermaid' in window), 'the vendor tag can fail to load');
});
