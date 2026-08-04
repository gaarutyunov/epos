// The catalog's whole client-side budget.
//
// Two behaviours, and nothing else: filter an index the page already carries,
// and copy a string to the clipboard. No fetching, no routing, no templating,
// no Markdown parser, and — the one worth writing down — no statistics query.
// The numbers arrive in the HTML, because a static host cannot hold a database
// credential and an open SQL endpoint is a denial-of-service surface.
//
// The third behaviour a browser-side script usually carries here is the theme,
// and there is none: the catalog is dark only, matching both references, and
// `data-theme="dark"` on <html> is the whole of it. A toggle would be a
// preference to persist, a flash to suppress and a second colour set to test,
// for a page that has one look.
//
// Every page works with scripting off. This file only ever adds.
(function () {
  "use strict";

  // ── Filter ───────────────────────────────────────────────────────────────
  //
  // Client-side, over rows already in the document. Nothing is fetched. The
  // field is hidden in the markup and revealed here, so a browser that will
  // never run this does not show a control that would do nothing.
  var filter = document.querySelector("[data-filter]");
  var target = document.querySelector("[data-filter-target]");
  var empty = document.querySelector("[data-filter-empty]");

  if (filter && target) {
    filter.hidden = false;
    filter.addEventListener("input", function () {
      var query = filter.value.trim().toLowerCase();
      var rows = target.querySelectorAll("[data-haystack]");
      var shown = 0;

      for (var i = 0; i < rows.length; i++) {
        var match =
          query === "" ||
          rows[i].getAttribute("data-haystack").toLowerCase().indexOf(query) !== -1;
        rows[i].hidden = !match;
        if (match) shown++;
      }
      if (empty) empty.hidden = shown !== 0;
    });
  }

  // ── Copy ─────────────────────────────────────────────────────────────────
  //
  // <ga-code> carries its own copy button, so this covers only the elements
  // that are not one. It is deliberately not a polyfill: a browser without
  // navigator.clipboard leaves the text selectable, which is what it was
  // before.
  document.addEventListener("click", function (event) {
    var trigger = event.target.closest ? event.target.closest("[data-copy]") : null;
    if (!trigger || !navigator.clipboard) return;

    navigator.clipboard.writeText(trigger.getAttribute("data-copy")).then(function () {
      var was = trigger.getAttribute("data-copied-label") || "copied";
      var original = trigger.textContent;
      trigger.textContent = was;
      setTimeout(function () {
        trigger.textContent = original;
      }, 1200);
    });
  });
})();
