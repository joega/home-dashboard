(function () {
  "use strict";
  // Keep this initial document version across HTMX fragment/navigation swaps.
  var loaded = document.body.dataset.appVersion;
  var lastActivity = performance.now(), pointerDown = false;
  var pending = false, checking = false, reloading = false;
  var pollInterval = 60000, idleInterval = 60000;
  if (!/^[a-f0-9]{24}$/.test(loaded || "")) return;

  function activity() { lastActivity = performance.now(); }
  function editable(element) {
    return element && element.matches("input:not([type=hidden]):not([type=checkbox]):not([type=radio]):not([type=button]):not([type=submit]):not([type=reset]),select,textarea,[contenteditable]:not([contenteditable=false])");
  }
  function idle() {
    return !document.hidden && !pointerDown && performance.now() - lastActivity >= idleInterval &&
      !editable(document.activeElement) &&
      !document.querySelector('.sheet,.entry-form,.recipe-shopping-form,[data-cooking],form[data-unsaved],.htmx-request');
  }
  // Timed-out/failed requests preserve the current screen. A reload always
  // follows a fresh, valid response from this same dashboard origin.
  function checkVersion() {
    if (document.hidden || checking || reloading) return;
    checking = true;
    var controller = new AbortController();
    var timeout = setTimeout(function () { controller.abort(); }, 5000);
    fetch("/version", {cache:"no-store",credentials:"same-origin",redirect:"error",signal:controller.signal})
      .then(function (response) {
        if (!response.ok) throw new Error("Version check unavailable");
        return response.json();
      }).then(function (data) {
        if (!data || typeof data.version !== "string" || !/^[a-f0-9]{24}$/.test(data.version)) return;
        // Also clears a queued update if the server has rolled back to our build.
        pending = data.version !== loaded;
        if (pending && idle()) {
          reloading = true;
          location.reload();
        }
      }).catch(function () {
        // Network outages and deployment downtime must not blank the page.
      }).then(function () { clearTimeout(timeout); checking = false; });
  }
  document.addEventListener("pointerdown", function () { pointerDown = true; activity(); }, {passive:true});
  document.addEventListener("pointerup", function () { pointerDown = false; activity(); }, {passive:true});
  document.addEventListener("pointercancel", function () { pointerDown = false; activity(); }, {passive:true});
  document.addEventListener("pointermove", function (event) {
    if (event.buttons || event.pointerType === "touch") activity();
  }, {passive:true});
  ["keydown","wheel","touchmove"].forEach(function (name) {
    document.addEventListener(name, activity, {passive:true});
  });
  ["input","change"].forEach(function (name) {
    document.addEventListener(name, function (event) {
      activity();
      var form = event.target.closest("form");
      if (form) form.setAttribute("data-unsaved", "");
    });
  });
  window.addEventListener("blur", function () { pointerDown = false; activity(); });
  document.addEventListener("visibilitychange", function () {
    pointerDown = false; activity();
    if (!document.hidden) checkVersion();
  });
  setInterval(checkVersion, pollInterval);
  // A queued update can apply shortly after a protected activity ends. Check
  // the server again instead of reloading from a possibly stale observation.
  setInterval(function () { if (pending && idle()) checkVersion(); }, 5000);
})();
