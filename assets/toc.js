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
  // The heading whose top has passed the sticky breadcrumb row is the one being read.
  function sync() {
    var top = main.getBoundingClientRect().top;
    var act = -1;
    for (var i = 0; i < heads.length; i++) {
      if (heads[i] && heads[i].getBoundingClientRect().top - top <= 64) act = i;
    }
    links.forEach(function (l, i) { l.classList.toggle('active', i === act); });
    if (act >= 0) keep(act);
  }
  main.addEventListener('scroll', sync, { passive: true });
  sync();
})();
