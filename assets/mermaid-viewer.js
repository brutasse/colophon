/* Diagram viewer. A rendered diagram keeps the size it was drawn at inside a scroll
   box, and the control chip over it gives the reader zoom in, zoom out, reset, and
   expand; expand lifts the box onto the screen at the zoom it already had. A diagram
   starts fitted, so nothing moves until a control is used and a page without
   JavaScript still shows it. */

/* Deferred after /_assets/vendor/mermaid.min.js, so mermaid is defined by now.
   startOnLoad offers no callback, so wait for the SVGs below. */
if (window.mermaid) mermaid.initialize({ startOnLoad: true, securityLevel: 'strict' });

var MIN = 0.25, MAX = 8;

function natural(svg) {
  var vb = svg.viewBox && svg.viewBox.baseVal;
  if (vb && vb.width) return { w: vb.width, h: vb.height };
  var b = svg.getBBox();
  return { w: b.width, h: b.height };
}

function size(view, px) {
  var svg = view.querySelector('svg'), n = view.nat;
  // Capture the style mermaid authored the first time we are about to change it.
  // Not at wrap time: mermaid applies its max-width cap after inserting the SVG,
  // so an early snapshot can come back empty and reset would then upscale.
  if (view.authored === null) view.authored = svg.getAttribute('style') || '';
  view.px = Math.round(Math.min(MAX * n.w, Math.max(MIN * n.w, px)));
  // mermaid caps the SVG at its natural width inline (useMaxWidth), and that cap
  // has to come off before an explicit width means anything.
  svg.style.maxWidth = 'none';
  svg.style.width = view.px + 'px';
  svg.style.height = Math.round(view.px * n.h / n.w) + 'px';
}

// reset is the state before anything was touched, so it restores mermaid's own
// inline style verbatim: dropping the max-width cap it authored would let the SVG
// grow past what it was given and upscale the drawing.
function reset(view) {
  if (view.authored === null) return;
  view.px = null;
  view.querySelector('svg').setAttribute('style', view.authored);
}

function shown(view) {
  var svg = view.querySelector('svg');
  return view.px || svg.getBoundingClientRect().width;
}

function btn(text, label, onclick) {
  var b = document.createElement('button');
  b.type = 'button';
  b.className = 'mm-btn';
  b.textContent = text;
  b.setAttribute('aria-label', label);
  b.title = label;
  b.onclick = function (e) { e.preventDefault(); onclick(); };
  return b;
}

function decorate(pre) {
  var svg = pre.querySelector('svg');
  if (!svg || pre.parentNode.className.indexOf('mm-view') >= 0) return;
  var n = natural(svg);
  if (!n.w) return;
  var view = document.createElement('div');
  view.className = 'mm-view';
  view.nat = n;
  view.authored = null;
  view.open = false;
  pre.parentNode.insertBefore(view, pre);
  var bar = document.createElement('div');
  bar.className = 'mm-bar';
  bar.appendChild(btn('\u2212', 'zoom out', function () { size(view, shown(view) / 1.25); }));
  bar.appendChild(btn('+', 'zoom in', function () { size(view, shown(view) * 1.25); }));
  bar.appendChild(btn('\u27f2', 'reset zoom', function () { reset(view); }));
  var expand = btn('\u26f6', 'expand', function () { setOpen(!view.open); });
  bar.appendChild(expand);
  // Only one diagram can be on the screen at a time: two panels inset 1rem from the
  // viewport are exactly coincident and both opaque, so the reader could never reach
  // the one underneath. setOpen is hung on the view so another view can close this
  // one, which is also where the label and the body class stay consistent.
  view.setOpen = setOpen;
  function setOpen(open) {
    open = !!open;
    if (view.open === open) return;
    if (open) {
      [].slice.call(document.querySelectorAll('.mm-view.mm-full')).forEach(function (other) {
        if (other !== view && other.setOpen) other.setOpen(false);
      });
    }
    view.open = open;
    view.classList.toggle('mm-full', open);
    document.body.classList.toggle('mm-fullscreen', !!document.querySelector('.mm-view.mm-full'));
    var label = open ? 'collapse' : 'expand';
    expand.textContent = open ? '\u2715' : '\u26f6';
    expand.setAttribute('aria-label', label);
    expand.title = label;
  }
  view.appendChild(bar);
  view.appendChild(pre);
  // a scroll box a keyboard cannot reach is a trap
  pre.tabIndex = 0;
  pre.setAttribute('role', 'region');
  pre.setAttribute('aria-label', 'diagram, scrollable; the controls over it zoom and expand it');
  var drag = null;
  pre.addEventListener('pointerdown', function (e) {
    if (e.button !== 0) return;
    drag = { x: e.clientX, y: e.clientY, l: pre.scrollLeft, t: pre.scrollTop };
    view.classList.add('mm-grab');
  });
  pre.addEventListener('pointermove', function (e) {
    if (!drag) return;
    pre.scrollLeft = drag.l - (e.clientX - drag.x);
    pre.scrollTop = drag.t - (e.clientY - drag.y);
  });
  ['pointerup', 'pointercancel', 'pointerleave'].forEach(function (ev) {
    pre.addEventListener(ev, function () { drag = null; view.classList.remove('mm-grab'); });
  });
  // ctrl/cmd + wheel is also what a trackpad pinch sends
  pre.addEventListener('wheel', function (e) {
    if (!e.ctrlKey && !e.metaKey) return;
    e.preventDefault();
    size(view, shown(view) * (e.deltaY < 0 ? 1.1 : 1 / 1.1));
  }, { passive: false });
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && view.open) setOpen(false);
  });
}

function scan() {
  var pending = 0;
  [].slice.call(document.querySelectorAll('pre.mermaid')).forEach(function (pre) {
    if (pre.querySelector('svg')) decorate(pre); else pending++;
  });
  if (pending && scan.tries++ < 60) setTimeout(scan, 250);
}
scan.tries = 0;

if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', scan); else scan();
