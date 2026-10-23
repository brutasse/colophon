/* The theme control, the mobile navigation, and the copy control on a fenced code
   block. The colour itself comes from the inline script in the head, which runs
   before the first paint so the page does not flash the wrong theme. */

(function () {
  var btn = document.querySelector('.theme-toggle');
  if (!btn) return;
  var next = { 'system': 'light', 'light': 'dark', 'dark': 'system' };
  var glyph = { 'system': '\u25d0', 'light': '\u2600', 'dark': '\u263e' };
  function state() { return localStorage.theme || 'system'; }
  function paint() {
    var s = state();
    btn.textContent = glyph[s] + ' ' + s;
    btn.title = 'theme: ' + s + ' \u2014 click for ' + next[s];
  }
  btn.onclick = function () {
    var n = next[state()];
    // "system" is the absence of a stored choice, which is what lets the
    // head's one-line script and this one agree on what the reader picked.
    if (n === 'system') {
      delete localStorage.theme;
      delete document.documentElement.dataset.theme;
    } else {
      localStorage.theme = n;
      document.documentElement.dataset.theme = n;
    }
    paint();
  };
  paint();
})();

(function () {
  var btn = document.querySelector('.nav-toggle');
  if (!btn) return;
  function open(o) {
    document.body.classList.toggle('nav-open', o);
    btn.setAttribute('aria-expanded', o);
  }
  btn.onclick = function () { open(!document.body.classList.contains('nav-open')); };
  document.querySelector('.scrim').onclick = function () { open(false); };
  // A tap on a sidebar link lands the reader on the page they asked for; the
  // panel would otherwise stay over it on a narrow screen.
  document.getElementById('sidebar').addEventListener('click', function (e) {
    if (e.target.closest('a')) open(false);
  });
  document.addEventListener('keydown', function (e) { if (e.key === 'Escape') open(false); });
})();

/* A highlighted fence gets one copy control, injected here the way the diagram
   viewer injects its chip: the rendered markup carries no button of its own, so a
   page without JavaScript keeps the code and loses the button. */
(function () {
  var blocks = [].slice.call(document.querySelectorAll('article .codeblock'));
  if (!blocks.length) return;

  function copyText(text, done) {
    if (navigator.clipboard) {
      // Denied permission is the browser refusing the write, which the reader
      // already sees as nothing happening; it must not also be a console error.
      navigator.clipboard.writeText(text).then(done, function () {});
      return;
    }
    // Plain http on a host that is not localhost has no Clipboard API, which is the
    // case for a docs site served over an internal address or an IP.
    var ta = document.createElement('textarea');
    ta.value = text;
    document.body.appendChild(ta);
    ta.select();
    document.execCommand('copy');
    ta.remove();
    done();
  }

  // The octicons copy-16 glyph drawn at the chip's size, in the inline-octicon shape
  // petroglyph already writes into an alert title.
  var ICON = '<svg viewBox="0 0 16 16" width="12" height="12" fill="currentColor" aria-hidden="true">'
    + '<path d="M0 6.75C0 5.784.784 5 1.75 5h1.5a.75.75 0 0 1 0 1.5h-1.5a.25.25 0 0 0-.25.25v7.5'
    + 'c0 .138.112.25.25.25h7.5a.25.25 0 0 0 .25-.25v-1.5a.75.75 0 0 1 1.5 0v1.5A1.75 1.75 0 0 1 9.25'
    + ' 16h-7.5A1.75 1.75 0 0 1 0 14.25Z"/>'
    + '<path d="M5 1.75C5 .784 5.784 0 6.75 0h7.5C15.216 0 16 .784 16 1.75v7.5A1.75 1.75 0 0 1 14.25'
    + ' 11h-7.5A1.75 1.75 0 0 1 5 9.25Zm1.75-.25a.25.25 0 0 0-.25.25v7.5c0 .138.112.25.25.25h7.5a.25.25'
    + ' 0 0 0 .25-.25v-7.5a.25.25 0 0 0-.25-.25Z"/></svg>';

  blocks.forEach(function (block) {
    var pre = block.querySelector('pre');
    if (!pre) return;
    var btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'code-copy';
    btn.innerHTML = ICON + '<span>Copy</span>';
    var label = btn.querySelector('span');
    btn.onclick = function () {
      // The pre's own text is what the reader wrote: petroglyph trims the fence's
      // trailing newline and chroma adds none, so nothing has to be cleaned up.
      copyText(pre.textContent, function () {
        // The label alone, so the icon holds its place through the message.
        label.textContent = 'Copied';
        setTimeout(function () { label.textContent = 'Copy'; }, 1500);
      });
    };
    block.appendChild(btn);
  });
})();
