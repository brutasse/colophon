/* The theme control, the mobile navigation, and the copy control on a fenced code
   block. The theme itself is the document's color scheme, and the meta in the head
   is the single source of truth for it: the inline script after the meta applies
   the reader's stored choice before the first paint, and this control rewrites the
   same content. The CSS maps the content to the palette, so no attribute is set. */

(function () {
  var meta = document.querySelector('meta[name="color-scheme"]');
  var btn = document.querySelector('.theme-toggle');
  if (!meta || !btn) return;
  var next = { 'system': 'light', 'light': 'dark', 'dark': 'system' };
  var glyph = { 'system': '\u25d0', 'light': '\u2600', 'dark': '\u263e' };
  // 'light dark' is the choice the reader never made, which is what lets the
  // head's pre-paint script and this one agree on what to show.
  function state() {
    return meta.content === 'light' ? 'light' : meta.content === 'dark' ? 'dark' : 'system';
  }
  function apply(s) {
    meta.content = s === 'system' ? 'light dark' : s;
    if (s === 'system') delete localStorage.theme;
    else localStorage.theme = s;
    btn.textContent = glyph[s] + ' ' + s;
    btn.title = 'theme: ' + s + ' \u2014 click for ' + next[s];
  }
  btn.onclick = function () { apply(next[state()]); };
  // A choice made in another tab arrives as a storage event; the meta follows so
  // every open tab keeps the same scheme.
  window.addEventListener('storage', function (e) {
    if (e.key !== 'theme') return;
    apply(e.newValue === 'light' || e.newValue === 'dark' ? e.newValue : 'system');
  });
  apply(state());
})();

/* The mobile navigation drawer. Below the fit threshold the sidebar is a
   popover: its closed state is the UA's display:none, the scroller's two snap
   stops are the open and closed positions, and a scroll-driven animation fades
   the backdrop with the swipe. On the wide side the same element is the page's
   always-visible sidebar, and the scroller flattens away. */
(function () {
  var btn = document.querySelector('.nav-toggle');
  if (!btn) return;
  var sidebar = document.getElementById('sidebar');
  var scroller = sidebar.querySelector('.sidebar-scroller');
  var sheet = sidebar.querySelector('.sidebar-sheet');
  var main = document.querySelector('main');
  var canObserve = 'IntersectionObserver' in window;

  // Two frames apart, so the second scroll does not cancel the first before the
  // browser has applied it; without rAF the wait is skipped rather than waited on.
  function settle(fn) {
    if (!window.requestAnimationFrame) return fn();
    window.requestAnimationFrame(function () { window.requestAnimationFrame(fn); });
  }

  function teardown() {
    if (!sidebar.hasAttribute('aria-modal')) return; // the load-time report is not a close
    sidebar.hidePopover();
    main.removeAttribute('inert');
    sheet.removeAttribute('tabindex');
    sidebar.removeAttribute('aria-modal');
    sidebar.removeAttribute('role');
    btn.setAttribute('aria-expanded', 'false');
    btn.focus();
  }

  function open() {
    // A drawer is a dialog to assistive technology; a sidebar is plain navigation,
    // so the role and the modality go with the state, not the markup.
    sidebar.setAttribute('role', 'dialog');
    sidebar.setAttribute('aria-modal', 'true');
    btn.setAttribute('aria-expanded', 'true');
    sidebar.showPopover();
    main.setAttribute('inert', '');
    if (CSS.supports('scroll-initial-target', 'nearest')) {
      // The drawer entered the top layer at the closed stop; the scroll, on the
      // scroller's CSS scroll-behavior, animates it in.
      scroller.scrollLeft = 0;
    } else {
      // It entered at the open position; jump it off-screen first, without the
      // smooth behavior, so the scroll below reads as the slide-in.
      scroller.style.scrollBehavior = 'auto';
      scroller.scrollLeft = scroller.offsetWidth;
      settle(function () {
        scroller.style.scrollBehavior = '';
        scroller.scrollLeft = 0;
      });
    }
    // The sheet takes focus for the keyboard reader; the tabindex goes with the
    // state, so the tab order stays clean while it acts as a sidebar.
    sheet.setAttribute('tabindex', '-1');
    sheet.focus();
  }

  function close() {
    // The slide-out stays visible only because the popover is taken down by
    // teardown once the sheet is fully off-screen, not by this scroll.
    scroller.scrollLeft = scroller.offsetWidth;
    if (!canObserve) teardown();
  }

  btn.onclick = function () {
    if (sidebar.hasAttribute('aria-modal')) close();
    else open();
  };
  // Escape, from anywhere focus may sit while the drawer is open.
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && sidebar.hasAttribute('aria-modal')) close();
  });
  // A tap on the dimmed area: anywhere inside the drawer but outside the sheet.
  sidebar.addEventListener('click', function (e) {
    if (!sheet.contains(e.target)) close();
  });
  if (canObserve) {
    var observer = new IntersectionObserver(function (entries) {
      // The most recent entry in a batch describes the current state; the
      // observer also reports the sheet off-screen on load, which teardown's
      // open-state guard drops.
      if (entries[entries.length - 1].intersectionRatio < 1 / window.innerWidth) teardown();
    }, { root: sidebar, threshold: [1 / window.innerWidth, 1] });
    observer.observe(sheet);
  }
  // Crossing to the wide side ends the drawer state outright: the scroller is
  // gone there, so there is no slide-out to observe.
  if (window.matchMedia) {
    window.matchMedia('(min-width: 60rem)').addEventListener('change', function (e) {
      if (e.matches) teardown();
    });
  }
  // Where scroll-driven animations are missing, the backdrop is a plain mapping
  // from scroll position: open at 0, closed at the sheet's width.
  if (!CSS.supports('animation-timeline: scroll()')) {
    scroller.addEventListener('scroll', function () {
      if (!sheet.offsetWidth) return;
      sidebar.style.setProperty('--drawer-backdrop', 1 - scroller.scrollLeft / sheet.offsetWidth);
    }, { passive: true });
  }
})();

/* A highlighted fence gets one copy control, injected here the way the diagram
   viewer injects its chip: the rendered markup carries no button of its own, so a
   page without JavaScript keeps the code and loses the button. The button is
   hidden until hover or focus, and the bubble beside it is the live region that
   announces the outcome, for both outcomes, to every reader. */
(function () {
  var blocks = [].slice.call(document.querySelectorAll('article .codeblock'));
  if (!blocks.length) return;

  // The octicons copy-16 glyph drawn at the chip's size, in the inline-octicon shape
  // petroglyph already writes into an alert title.
  var ICON = '<svg viewBox="0 0 16 16" width="12" height="12" fill="currentColor" aria-hidden="true">'
    + '<path d="M0 6.75C0 5.784.784 5 1.75 5h1.5a.75.75 0 0 1 0 1.5h-1.5a.25.25 0 0 0-.25.25v7.5'
    + 'c0 .138.112.25.25.25h7.5a.25.25 0 0 0 .25-.25v-1.5a.75.75 0 0 1 1.5 0v1.5A1.75 1.75 0 0 1 9.25'
    + ' 16h-7.5A1.75 1.75 0 0 1 0 14.25Z"/>'
    + '<path d="M5 1.75C5 .784 5.784 0 6.75 0h7.5C15.216 0 16 .784 16 1.75v7.5A1.75 1.75 0 0 1 14.25'
    + ' 11h-7.5A1.75 1.75 0 0 1 5 9.25Zm1.75-.25a.25.25 0 0 0-.25.25v7.5c0 .138.112.25.25.25h7.5a.25.25'
    + ' 0 0 0 .25-.25v-7.5a.25.25 0 0 0-.25-.25Z"/></svg>';

  // A plain http origin is no secure context, so navigator.clipboard is absent
  // there; the execCommand below is what those readers get. The selection must be
  // restored, since the helper takes focus to be copied.
  function legacyCopy(text) {
    var prev = document.activeElement;
    var field = document.createElement('textarea');
    field.value = text;
    field.setAttribute('readonly', '');
    field.style.cssText = 'position:fixed;inset-block-start:0;opacity:0';
    document.body.appendChild(field);
    field.select();
    var ok;
    try { ok = document.execCommand('copy'); } finally { field.remove(); }
    if (prev && prev.focus) prev.focus({ preventScroll: true });
    return ok;
  }

  var timers = new WeakMap();
  var lastMessage = '';

  function report(button, ok) {
    var tip = button.nextElementSibling; // the role="status" bubble
    // A repeat click restarts the one pending cleanup rather than stacking it.
    clearTimeout(timers.get(button));
    button.removeAttribute('data-copied');
    button.removeAttribute('data-failed');
    var message = ok ? 'Copied' : 'Copy failed';
    // A screen reader skips a message identical to the last one from the same
    // region; the trailing space is not read aloud.
    if (message === lastMessage) message += '\u00a0';
    lastMessage = message;
    tip.textContent = message;
    button.setAttribute(ok ? 'data-copied' : 'data-failed', '');
    timers.set(button, setTimeout(function () {
      button.removeAttribute('data-copied');
      button.removeAttribute('data-failed');
      tip.textContent = '';
    }, 2000));
  }

  function copy(button, pre) {
    // The pre's own text is what the reader wrote: petroglyph trims the fence's
    // trailing newline and chroma adds none, so nothing has to be cleaned up.
    var text = pre.textContent;
    if (!text) return;
    if (navigator.clipboard) {
      navigator.clipboard.writeText(text).then(function () {
        report(button, true);
      }, function () {
        report(button, legacyCopy(text));
      });
      return;
    }
    report(button, legacyCopy(text));
  }

  blocks.forEach(function (block) {
    var pre = block.querySelector('pre');
    if (!pre) return;
    var chip = document.createElement('span');
    chip.className = 'code-copy';
    var button = document.createElement('button');
    button.type = 'button';
    button.className = 'code-copy-btn';
    button.setAttribute('aria-label', 'Copy code');
    button.innerHTML = ICON;
    var tip = document.createElement('span');
    tip.className = 'code-copy-tip';
    tip.setAttribute('role', 'status');
    chip.appendChild(button);
    chip.appendChild(tip);
    block.appendChild(chip);
    button.onclick = function () { copy(button, pre); };
    // A pre wider than its box is its own scroll container; give it focus so a
    // keyboard reader can move the code the way the wheel does, and name it so
    // the focus is not announced as an unnamed region.
    if (pre.scrollWidth > pre.clientWidth) {
      pre.setAttribute('tabindex', '0');
      pre.setAttribute('role', 'region');
      pre.setAttribute('aria-label', 'Scrollable code block');
    }
  });
})();
