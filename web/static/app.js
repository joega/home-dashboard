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
  var menuTooltipTrigger = null, menuTooltipTimer = 0, menuTouchUntil = 0;
  function hideMenuTooltip() {
    clearTimeout(menuTooltipTimer);
    var tip = document.getElementById("section-tooltip");
    if (tip) tip.hidden = true;
    if (menuTooltipTrigger) menuTooltipTrigger.removeAttribute("aria-describedby");
    menuTooltipTrigger = null;
  }
  function showMenuTooltip(link) {
    clearTimeout(menuTooltipTimer);
    if (performance.now() < menuTouchUntil || document.querySelector(".sheet")) return;
    var tip = document.getElementById("section-tooltip");
    if (!tip) return;
    if (menuTooltipTrigger && menuTooltipTrigger !== link) menuTooltipTrigger.removeAttribute("aria-describedby");
    menuTooltipTrigger = link;
    tip.textContent = link.getAttribute("aria-label");
    tip.hidden = false;
    link.setAttribute("aria-describedby", tip.id);
    var box = link.getBoundingClientRect(), width = tip.offsetWidth, height = tip.offsetHeight;
    tip.style.left = Math.max(8, Math.min(box.left + (box.width - width) / 2, window.innerWidth - width - 8)) + "px";
    var top = box.bottom + 8;
    if (top + height > window.innerHeight - 8) top = box.top - height - 8;
    tip.style.top = Math.max(8, top) + "px";
  }
  function leaveMenuTooltip(event) {
    var link = event.target.closest(".topbar .nav-item"), tip = document.getElementById("section-tooltip");
    if ((!link || link !== menuTooltipTrigger) && event.target !== tip) return;
    var next = event.relatedTarget;
    if (next && ((link && link.contains(next)) || (tip && tip.contains(next)))) return;
    // The small gap below an icon must not close the tooltip while entering it.
    clearTimeout(menuTooltipTimer);
    menuTooltipTimer = setTimeout(hideMenuTooltip, 120);
  }
  document.addEventListener("mouseover", function(event) {
    var link = event.target.closest(".topbar .nav-item");
    if (link && (!event.relatedTarget || !link.contains(event.relatedTarget))) showMenuTooltip(link);
    else if (event.target.closest("#section-tooltip")) clearTimeout(menuTooltipTimer);
  });
  document.addEventListener("mouseout", leaveMenuTooltip);
  document.addEventListener("focusin", function(event) {
    var link = event.target.closest(".topbar .nav-item");
    if (link) showMenuTooltip(link);
  });
  document.addEventListener("focusout", leaveMenuTooltip);
  function touchMenu() { menuTouchUntil = performance.now() + 1500; hideMenuTooltip(); }
  document.addEventListener("touchstart", touchMenu, {passive:true});
  document.addEventListener("pointerdown", function(event) { if (event.pointerType === "touch" || event.pointerType === "pen") touchMenu(); });
  document.addEventListener("click", hideMenuTooltip);
  document.addEventListener("keydown", function(event) { if (event.key === "Escape") hideMenuTooltip(); });
  document.addEventListener("scroll", hideMenuTooltip, true);
  document.addEventListener("htmx:beforeSwap", hideMenuTooltip);
  window.addEventListener("resize", hideMenuTooltip);
  window.addEventListener("blur", hideMenuTooltip);
  var boundMenus = new WeakSet(), menuPositions = new Map();
  function menuKey(track) {
    var current = track.querySelector('[aria-current]');
    var overflow = track.closest('.sports-team-row');
    var more = overflow && overflow.querySelector('select[name=team]');
    return track.id + ':' + Array.from(track.querySelectorAll('a')).map(function(a) {return a.getAttribute('href');}).join('|') + ':' + (current ? current.getAttribute('href') : more ? more.value : '');
  }
  function menuCues(shell) {
    var track = shell.querySelector('[data-menu-track]');
    var back = shell.querySelector('[data-menu-step="back"]'), next = shell.querySelector('[data-menu-step="next"]');
    // Buttons share the row, so settle their visibility after they resize the
    // track. Never reserve space when every item already fits.
    var links = track.querySelectorAll('a');
    for (var pass = 0; pass < 2; pass++) {
      var box = track.getBoundingClientRect();
      back.hidden = !links.length || links[0].getBoundingClientRect().left >= box.left - 2;
      next.hidden = !links.length || links[links.length - 1].getBoundingClientRect().right <= box.right + 2;
    }
  }
  function configureMenuScroll() {
    document.querySelectorAll('[data-scroll-menu]').forEach(function(shell) {
      if (!boundMenus.has(shell)) {
        boundMenus.add(shell);
        var track = shell.querySelector('[data-menu-track]'), key = menuKey(track);
        menuCues(shell);
        if (menuPositions.has(key)) track.scrollLeft = menuPositions.get(key);
        track.addEventListener('scroll',function() {
          menuCues(shell);
          if (!menuPositions.has(key) && menuPositions.size >= 64) menuPositions.delete(menuPositions.keys().next().value);
          menuPositions.set(key,track.scrollLeft);
        },{passive:true});
      }
      menuCues(shell);
    });
  }
  function layoutMenus(event) {
    var force = event && event.type === "resize";
    for (var pass = 0; pass < 2; pass++) {
      configureMenuScroll();
      scrollSectionNav(force);
      scrollStoreFilters(force);
    }
    configureMenuScroll();
  }
  function scrollSectionNav(force) {
    var nav=document.querySelector('.topbar nav'), selected=nav && nav.querySelector('[aria-current]');
    if(!selected || (!force && menuPositions.has(menuKey(nav))))return;
    var box=nav.getBoundingClientRect(), item=selected.getBoundingClientRect();
    if(item.right>box.right)nav.scrollLeft+=item.right-box.right;
    else if(item.left<box.left)nav.scrollLeft-=box.left-item.left;
  }
  window.addEventListener("resize", layoutMenus);
  function scrollStoreFilters(force) {
    document.querySelectorAll(".store-filters,.sports-team-filters").forEach(function(filters) {
      var selected=filters.querySelector("[aria-current]");if(!selected || (!force && filters.hasAttribute("data-menu-track") && menuPositions.has(menuKey(filters))))return;
      var box=filters.getBoundingClientRect(), selectedBox=selected.getBoundingClientRect();
      if(selectedBox.right>box.right)filters.scrollLeft+=selectedBox.right-box.right;
      else if(selectedBox.left<box.left)filters.scrollLeft-=box.left-selectedBox.left;
    });
  }

  function configureSportsPicker() {
    var picker = document.querySelector('[data-sports-picker]');
    if (!picker) return;
    var selected = picker.querySelector('[data-team-selected]'), results = picker.querySelector('[data-team-results]');
    var query = picker.querySelector('[data-team-search]').value.trim().toLowerCase();
    var league = picker.querySelector('[data-team-league]').value;
    var rows = Array.from(picker.querySelectorAll('[data-team-choice]'));
    // Move controls, rather than duplicate them: a search never loses checked
    // selections, and Save submits the chosen shortcut order exactly once.
    rows.forEach(function(row) {
      var box = row.querySelector('input');
      if (box.checked && row.parentElement !== selected) selected.appendChild(row);
      else if (!box.checked && row.parentElement !== results) results.appendChild(row);
    });
    var count = selected.children.length, found = 0, searching = query.length >= 2 || !!league;
    Array.from(results.children).sort(function(a,b) {return a.dataset.teamName.localeCompare(b.dataset.teamName);}).forEach(function(row) {results.appendChild(row);});
    rows.forEach(function(row) {
      var box = row.querySelector('input');
      var match = searching && (!league || row.dataset.teamLeagueName === league) && query.split(/\s+/).every(function(word) {return row.dataset.teamName.toLowerCase().indexOf(word) >= 0;});
      row.hidden = !box.checked && !match;
      box.disabled = !box.checked && count >= 24;
      if (!box.checked && match) found++;
    });
    picker.querySelector('[data-team-count]').textContent = count;
    picker.querySelector('[data-team-empty]').hidden = count > 0;
    picker.querySelector('[data-team-results-heading]').hidden = !found;
    picker.querySelector('[data-team-search-status]').textContent = count >= 24 ? 'You’re following 24 teams. Remove one to add another.' : !searching ? 'Search for a team or choose a league to browse.' : found ? found + (found === 1 ? ' team available. Select it, then save.' : ' teams available. Select one, then save.') : 'No more matching teams. Try another name or league.';
  }
  document.addEventListener('input', function(event) {
    if (event.target.matches('[data-team-search]')) configureSportsPicker();
  });
  document.addEventListener('change', function(event) {
    if (event.target.matches('[data-team-league],[data-sports-picker] input[name=teams]')) configureSportsPicker();
  });

  var forecastPositions = new Map(), forecastBindings = new WeakSet();
  function configureForecast() {
    document.querySelectorAll('[data-forecast-scroll],[data-forecast-days]').forEach(function(track) {
      if (forecastBindings.has(track)) return;
      forecastBindings.add(track);
      var days = track.hasAttribute('data-forecast-days');
      var key = (days ? 'days:' : 'hours:') + (days ? track.dataset.forecastDays : track.dataset.forecastScroll);
      if (forecastPositions.has(key)) track.scrollLeft = forecastPositions.get(key);
      if (days) {
        var selected = track.querySelector('[aria-current]');
        if (selected && (selected.offsetLeft < track.scrollLeft || selected.offsetLeft + selected.offsetWidth > track.scrollLeft + track.clientWidth)) {
          track.scrollLeft = Math.max(0, selected.offsetLeft - track.offsetLeft - 12);
        }
      }
      track.addEventListener('scroll', function() {
        if (!forecastPositions.has(key) && forecastPositions.size >= 32) forecastPositions.delete(forecastPositions.keys().next().value);
        forecastPositions.set(key, track.scrollLeft);
      }, {passive:true});
    });
  }

  function update() {
    recipeProgress();
    configureForecast();
    configureSportsPicker();
    // Swap markers belong to response fragments, not cached history snapshots.
    document.querySelectorAll("[hx-swap-oob]").forEach(function (element) {element.removeAttribute("hx-swap-oob");});
    var weatherState=document.getElementById("weather-scene-state");
    if(weatherState){document.body.dataset.weather=weatherState.dataset.scene;document.body.dataset.theme=weatherState.dataset.theme;}
    clock();
    configureCalendar();
    configureSchedule();
    layoutMenus();
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
    var step = event.target.closest('[data-menu-step]');
    if (step) {
      var track = step.closest('[data-scroll-menu]').querySelector('[data-menu-track]');
      var reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
      track.scrollBy({left:(step.dataset.menuStep === 'back' ? -1 : 1) * Math.max(100,track.clientWidth * .75),behavior:reduceMotion ? 'auto' : 'smooth'});
      return;
    }
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
  document.addEventListener("input",function(event){var form=event.target.closest(".media-add");if(form)form.setAttribute("data-pause-refresh","");if(event.target.matches('.spotify-volume-slider input')){event.target.closest('label').querySelector('output').textContent=event.target.value+'%';event.target.closest('form').setAttribute('data-pause-refresh','');}});
  document.addEventListener("change",function(event){var form=event.target.closest(".media-add");if(form)form.setAttribute("data-pause-refresh","");var deviceForm=event.target.closest(".spotify-device-form");if(deviceForm){if(event.target.value!==deviceForm.dataset.activeDevice)deviceForm.setAttribute("data-pause-refresh","");else deviceForm.removeAttribute("data-pause-refresh");}});
  document.addEventListener("htmx:beforeRequest", function (event) {
    if(event.detail.elt && event.detail.elt.hasAttribute("data-media-poll") && (document.hidden || document.querySelector(".sheet,[data-pause-refresh]"))){event.preventDefault();return;}
    var scroller = document.querySelector(".list-scroll");
    var trigger = event.detail.elt;
    var sportsNavigation = trigger && trigger.closest(".sports-team-filters,.sports-modes,.sports-overflow");
    var notesNavigation = trigger && trigger.closest(".note-open,.notes-tabs,.notes-pages,.note-reader-toolbar,.note-form");
    scrollTop = sportsNavigation || notesNavigation ? 0 : (scroller ? scroller.scrollTop : 0);
  });
  document.addEventListener("appearance",function(event){var theme=event.detail.theme;if(theme==="light"||theme==="dark"){document.body.dataset.theme=theme;var state=document.getElementById("weather-scene-state");if(state)state.dataset.theme=theme;}});
  document.addEventListener("htmx:afterSwap", update);
  document.addEventListener("htmx:historyRestore", update);
  document.addEventListener("recipeCooked", function(event) {
    var key=event.detail.key;
    if(typeof key === "string" && /^[0-9]+-[0-9]+$/.test(key)) {
      try { localStorage.removeItem('hq-recipe-' + key); } catch (_) {}
    }
  });
  document.addEventListener("DOMContentLoaded", update);
  window.addEventListener("load", layoutMenus);
  if(document.fonts && document.fonts.ready)document.fonts.ready.then(layoutMenus);
  setInterval(tick, 1000);
  setInterval(function () {
    if (document.hidden || document.querySelector(".sheet,[data-pause-refresh],.cookbook-page,[data-spotify-connected]") || document.querySelector(".htmx-request") || !window.htmx) return;
    htmx.ajax("GET", location.pathname + location.search, {target:"#content",swap:"innerHTML",headers:{"X-Home-HQ-Background":"true"}}).catch(function () {
      // The connection policy handles failed polls; keep the current screen.
    });
  }, 30000);
})();
