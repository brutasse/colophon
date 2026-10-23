/* The theme control and the mobile navigation. The colour itself comes from the
   inline script in the head, which runs before the first paint so the page does not
   flash the wrong theme. */

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
