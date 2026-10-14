(function () {
  "use strict";
  var gracePeriod = 60000, firstFailure = null, failures = 0;
  // HTMX can emit afterRequest twice when its source was replaced by a swap.
  var handled = new WeakSet();

  function background(detail) {
    var element = detail.elt;
    var headers = detail.requestConfig && detail.requestConfig.headers;
    return !!((headers && headers["X-Home-HQ-Background"] === "true") ||
      (element && (element.id === "clock" || element.id === "appearance-refresh" ||
        element.hasAttribute("data-media-poll"))));
  }
  function show(message) {
    var banner = document.getElementById("connection");
    var text = document.getElementById("connection-message");
    if (text) text.textContent = message;
    if (banner) banner.hidden = false;
  }
  function complete(event, networkFailure) {
    var detail = event.detail || {}, xhr = detail.xhr;
    // Abort/canceled requests are not outages. Network errors/timeouts have no
    // failed flag in HTMX, so their dedicated events enter explicitly below.
    if (!detail.successful && !detail.failed && !networkFailure) return;
    if (xhr) {
      if (handled.has(xhr)) return;
      handled.add(xhr);
    }
    if (detail.successful) {
      firstFailure = null; failures = 0;
      var banner = document.getElementById("connection");
      if (banner) banner.hidden = true;
      return;
    }
    if (!background(detail)) {
      firstFailure = null; failures = 0;
      show("The request failed. Check the result before trying again.");
      return;
    }
    if (firstFailure === null) firstFailure = performance.now();
    failures++;
    if (failures >= 2 && performance.now() - firstFailure >= gracePeriod) {
      show("Home HQ is temporarily unavailable. Retrying automatically.");
    }
  }
  document.addEventListener("htmx:afterRequest", function (event) { complete(event, false); });
  document.addEventListener("htmx:sendError", function (event) { complete(event, true); });
  document.addEventListener("htmx:timeout", function (event) { complete(event, true); });
})();
