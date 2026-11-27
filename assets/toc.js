/* Rail scroll-spy. The rail is capped at the viewport height and scrolls on its own,
   so a page with more headings than the screen would leave the highlighted link below
   its fold. */
(function () {
  var rail = document.querySelector('.toc');
  if (!rail) return;
  var main = document.querySelector('main');
  var links = [].slice.call(rail.querySelectorAll('a'));
  var heads = links.map(function (a) {
    return document.getElementById(decodeURIComponent(a.hash.slice(1)));
  });
  // Keep five links of lead under the highlight, and five above for a reader
  // scrolling back up, so the section coming next is named before it is reached.
  // docs.css animates the move.
  function keep(i) {
    var n = links.length;
    var rr = rail.getBoundingClientRect();
    var off = rail.scrollTop - rr.top;
    var low = links[Math.min(i + 5, n - 1)].getBoundingClientRect().bottom;
    var up = links[Math.max(i - 5, 0)].getBoundingClientRect().top;
    var el = links[i].getBoundingClientRect();
    var lo = off + low - rail.clientHeight;
    var hi = off + up;
    // five above and five below do not always fit in the rail together: then clear the highlight
    if (lo > hi) {
      lo = off + el.bottom - rail.clientHeight + 12;
      hi = off + el.top - 12;
    }
    if (rail.scrollTop < lo) rail.scrollTop = lo; else if (rail.scrollTop > hi) rail.scrollTop = hi;
  }
  // The highlight must mirror in aria-current: neither :target-current nor the
  // fallback class is exposed to assistive technology on its own.
  function mark(i) {
    links.forEach(function (l, j) {
      l.classList.toggle('active', j === i);
      l.setAttribute('aria-current', j === i ? 'true' : 'false');
    });
    if (i >= 0) keep(i);
  }
  // The property and the pseudo-class ship together; asking for both keeps a
  // browser that knows one but not the other on the fallback.
  if (CSS.supports('scroll-target-group: auto') && CSS.supports('selector(:target-current)')) {
    // The browser that ships scroll-target-group tracks which section the rail's
    // markers point at, and :target-current does the highlighting in CSS; this
    // mirrors the state to aria-current and keeps the highlight scrolled into the
    // rail.
    var sync = function () {
      mark(links.indexOf(rail.querySelector('a:target-current')));
    };
    sync();
    document.addEventListener('scrollend', sync);
    main.addEventListener('scroll', sync, { passive: true });
    return;
  }
  // The heading whose top has passed the sticky breadcrumb row is the one being read.
  function sync() {
    var top = main.getBoundingClientRect().top;
    var act = -1;
    for (var i = 0; i < heads.length; i++) {
      if (heads[i] && heads[i].getBoundingClientRect().top - top <= 64) act = i;
    }
    mark(act);
  }
  main.addEventListener('scroll', sync, { passive: true });
  sync();
})();
