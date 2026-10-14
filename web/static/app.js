(function () {
  "use strict";
  var anchor = null, clockOrigin = 0, clockStart = 0, formatter = null, lastTrigger = null;
  var scrollTop = 0, currentDialog = null;
  var timeline = null, timelineKey = "", timelineOffsets = {}, swipeStart = null, suppressClick = 0;
  function clock() {
    var element = document.getElementById("clock");
    if (element === anchor) return;
    anchor = element;
    if (!anchor) return;
    clockOrigin = Number(anchor.dataset.now);
    clockStart = performance.now();
    formatter = new Intl.DateTimeFormat("en-US", {hour:"numeric",minute:"2-digit",timeZone:anchor.dataset.zone});
    tick();
  }
  function tick() {
    if (anchor && anchor.isConnected && formatter) anchor.textContent = formatter.format(new Date(clockOrigin + performance.now() - clockStart));
  }

  function recipeProgress() {
    var recipe = document.querySelector('[data-recipe-progress]');
    if (recipe && !recipe.dataset.progressInitialized) {
      recipe.dataset.progressInitialized="1";
      var key = 'hq-recipe-' + recipe.dataset.recipeProgress, saved = {};
      try { saved = JSON.parse(localStorage.getItem(key) || '{}'); } catch (_) {}
      recipe.querySelectorAll('[data-recipe-check]').forEach(function (box) {
        box.checked = !!saved[box.dataset.recipeCheck];
        box.onchange = function () {
          saved[box.dataset.recipeCheck] = box.checked;
          try { localStorage.setItem(key, JSON.stringify(saved)); } catch (_) {}
        };
      });
      var reset = recipe.querySelector('[data-reset-recipe]');
      if (reset) reset.onclick = function () {
        saved = {}; recipe.querySelectorAll('[data-recipe-check]').forEach(function (box) {box.checked = false;});
        try { localStorage.removeItem(key); } catch (_) {}
      };
    }
    var shopping = document.querySelector('[data-recipe-shopping]');
    if (shopping && !shopping.hasAttribute("data-shopping-retry") && !shopping.dataset.progressInitialized) {
      shopping.dataset.progressInitialized="1";
      var have = {};
      try { have = JSON.parse(localStorage.getItem('hq-recipe-' + shopping.dataset.recipeShopping) || '{}'); } catch (_) {}
      shopping.querySelectorAll('input[name=need]').forEach(function (box) {box.checked = !have['have-' + box.value];});
    }
  }

  function closeEditor() {
    document.getElementById("drawer").replaceChildren();
    document.body.classList.remove("sheet-open");
    if (lastTrigger && lastTrigger.isConnected) lastTrigger.focus();
    currentDialog = null;
  }
  function configureCalendar() {
    var cal = document.querySelector(".calendar-app");
    if (!cal) {timeline = null;return;}
    cal.querySelectorAll("[data-top]").forEach(function (element) {
      element.style.top = Number(element.dataset.top) + "px";
    });
    cal.querySelectorAll(".timeline-event").forEach(function (element) {
      element.style.height = Number(element.dataset.height) + "px";
      element.style.left = "calc(" + Number(element.dataset.left) + "% + 3px)";
      element.style.width = "calc(" + Number(element.dataset.width) + "% - 6px)";
      element.classList.toggle("short", Number(element.dataset.height) < 60);
    });
    var now = cal.querySelector(".now-line");
    if (now) {
      var count = Number(now.dataset.lanes), index = Number(now.dataset.index);
      now.style.left = now.dataset.mode === "day" ? "0" : (index * 100 / count) + "%";
      now.style.width = now.dataset.mode === "day" ? "100%" : (100 / count) + "%";
      if (index > 0) now.querySelector("span").hidden = true;
    }
    var nextTimeline = cal.querySelector(".timeline-scroll");
    if (nextTimeline && nextTimeline !== timeline) {
      timeline = nextTimeline; timelineKey = cal.dataset.key;
      timeline.scrollTop = timelineOffsets[timelineKey] !== undefined ? timelineOffsets[timelineKey] : Number(cal.dataset.scroll);
      timeline.addEventListener("scroll", function () {
        if (this === timeline) timelineOffsets[timelineKey] = this.scrollTop;
      });
    } else if (!nextTimeline) timeline = null;
  }
  function configureSchedule() {
    var select=document.querySelector("[data-repeat]");if(!select)return;
    var form=select.closest("form"), value=select.value, unit={daily:"days",weekly:"weeks",custom:"weeks",monthly:"months",yearly:"years"};
    form.querySelector("[data-repeat-interval]").hidden=!unit[value];
    form.querySelector("[name=repeat_every]").disabled=!unit[value];
    form.querySelector("[data-repeat-unit]").textContent=unit[value]||"days";
    form.querySelector("[data-repeat-days]").hidden=value!=="custom";
    form.querySelectorAll("[name=repeat_days]").forEach(function(input){input.disabled=value!=="custom";});
    form.querySelector("[data-repeat-end]").hidden=!value;
    form.querySelector("[name=repeat_until]").disabled=!value;
    form.querySelector("[data-monthly-hint]").hidden=value!=="monthly"&&value!=="yearly";
    var start=form.querySelector("[data-chore-start]");if(start)start.required=!!value;
  }
  document.addEventListener("change",function(event){if(event.target.matches("[data-repeat]"))configureSchedule();});
  function scrollSectionNav() {
    var nav=document.querySelector('.topbar nav'), selected=nav && nav.querySelector('[aria-current]');
    if(!selected)return;
    var box=nav.getBoundingClientRect(), item=selected.getBoundingClientRect();
    if(item.right>box.right)nav.scrollLeft+=item.right-box.right;
    else if(item.left<box.left)nav.scrollLeft-=box.left-item.left;
  }
  window.addEventListener("resize", scrollSectionNav);
  function scrollStoreFilters() {
    document.querySelectorAll(".store-filters,.sports-team-filters").forEach(function(filters) {
      var selected=filters.querySelector("[aria-current]");if(!selected)return;
      var box=filters.getBoundingClientRect(), selectedBox=selected.getBoundingClientRect();
      if(selectedBox.right>box.right)filters.scrollLeft+=selectedBox.right-box.right;
      else if(selectedBox.left<box.left)filters.scrollLeft-=box.left-selectedBox.left;
    });
  }

  function update() {
    recipeProgress();
    // Swap markers belong to response fragments, not cached history snapshots.
    document.querySelectorAll("[hx-swap-oob]").forEach(function (element) {element.removeAttribute("hx-swap-oob");});
    var weatherState=document.getElementById("weather-scene-state");
    if(weatherState){document.body.dataset.weather=weatherState.dataset.scene;document.body.dataset.theme=weatherState.dataset.theme;}
    clock();
    configureCalendar();
    configureSchedule();
    scrollStoreFilters();
    scrollSectionNav();
    var dialog = document.querySelector(".sheet");
    document.body.classList.toggle("sheet-open", !!dialog);
    if (dialog && dialog !== currentDialog) {
      var input = dialog.querySelector("[autofocus]");
      if (input) input.focus(); else dialog.focus();
    }
    currentDialog = dialog;
    var heading = document.querySelector("#content h1");
    var calTitle = document.querySelector("[data-page-title]");
    if (calTitle || heading) document.title = "Home HQ · " + (calTitle ? calTitle.dataset.pageTitle : heading.textContent);
    var scroller = document.querySelector(".list-scroll");
    if (scroller) scroller.scrollTop = scrollTop;
  }
  document.addEventListener("click", function (event) {
    if (performance.now() < suppressClick) {event.preventDefault();event.stopPropagation();return;}
    var jump = event.target.closest("[data-hour]");
    if (jump && timeline) {
      var targetTop = Number(jump.dataset.hour) * 72;
      var reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
      timeline.scrollTo({top:targetTop,behavior:reduce ? "auto" : "smooth"});
      document.querySelectorAll("[data-hour]").forEach(function (button) {button.classList.toggle("active",button === jump);});
      return;
    }
    var target = event.target.closest("[data-close]");
    if (target) {closeEditor();return;}
    if (event.target.closest("[data-reload]")) location.reload();
    if (event.target.closest('[hx-target="#drawer"]')) lastTrigger = event.target.closest("button,a");
  });
  document.addEventListener("pointerdown", function (event) {
    if (!event.target.closest(".calendar-body") || event.target.closest(".time-jumps") || event.button !== 0) return;
    var agenda = event.target.closest(".week-agenda");
    if (agenda && agenda.scrollWidth > agenda.clientWidth) return;
    swipeStart = {x:event.clientX,y:event.clientY,time:performance.now()};
  });
  document.addEventListener("pointerup", function (event) {
    if (!swipeStart) return;
    var dx = event.clientX - swipeStart.x, dy = event.clientY - swipeStart.y;
    var duration = performance.now() - swipeStart.time; swipeStart = null;
    if (Math.abs(dx) > 90 && Math.abs(dy) < 60 && duration < 1000) {
      suppressClick = performance.now() + 500;
      var link = document.querySelector('[data-cal-nav="' + (dx < 0 ? "next" : "previous") + '"]');
      if (link && window.htmx) htmx.ajax("GET",link.getAttribute("href"),{target:"#content",swap:"innerHTML"});
    }
  });
  document.addEventListener("pointercancel", function () {swipeStart = null;});
  document.addEventListener("keydown", function (event) {
    var dialog = document.querySelector(".sheet");
    if (!dialog) return;
    if (event.key === "Escape") {closeEditor();return;}
    if (event.key !== "Tab") return;
    var controls = dialog.querySelectorAll("button,input:not([type=hidden]),select,a[href]");
    if (!controls.length) return;
    var first = controls[0], last = controls[controls.length - 1];
    if (event.shiftKey && document.activeElement === first) {event.preventDefault();last.focus();}
    else if (!event.shiftKey && document.activeElement === last) {event.preventDefault();first.focus();}
  });
  document.addEventListener("htmx:beforeRequest", function (event) {
    var scroller = document.querySelector(".list-scroll");
    var trigger = event.detail.elt;
    var sportsNavigation = trigger && trigger.closest(".sports-team-filters,.sports-modes");
    scrollTop = sportsNavigation ? 0 : (scroller ? scroller.scrollTop : 0);
  });
  document.addEventListener("appearance",function(event){var theme=event.detail.theme;if(theme==="light"||theme==="dark"){document.body.dataset.theme=theme;var state=document.getElementById("weather-scene-state");if(state)state.dataset.theme=theme;}});
  document.addEventListener("htmx:afterSwap", update);
  document.addEventListener("htmx:historyRestore", update);
  document.addEventListener("htmx:afterRequest", function (event) {
    var connection=document.getElementById("connection"); if(connection) connection.hidden = event.detail.successful;
  });
  document.addEventListener("recipeCooked", function(event) {
    var key=event.detail.key;
    if(typeof key === "string" && /^[0-9]+-[0-9]+$/.test(key)) {
      try { localStorage.removeItem('hq-recipe-' + key); } catch (_) {}
    }
  });
  document.addEventListener("htmx:sendError", function () {var connection=document.getElementById("connection"); if(connection) connection.hidden = false;});
  document.addEventListener("htmx:responseError", function () {var connection=document.getElementById("connection"); if(connection) connection.hidden = false;});
  document.addEventListener("DOMContentLoaded", update);
  window.addEventListener("load", scrollStoreFilters);
  window.addEventListener("resize", scrollStoreFilters);
  if(document.fonts && document.fonts.ready)document.fonts.ready.then(scrollStoreFilters);
  setInterval(tick, 1000);
  setInterval(function () {
    if (document.hidden || document.querySelector(".sheet,[data-pause-refresh],.cookbook-page") || document.querySelector(".htmx-request") || !window.htmx) return;
    htmx.ajax("GET", location.pathname + location.search, {target:"#content",swap:"innerHTML"});
  }, 30000);
})();
