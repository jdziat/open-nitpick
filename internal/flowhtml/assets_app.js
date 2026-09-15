"use strict";
(function () {
  var NODES = window.__FLOW_NODES__ || {};
  var START = window.__FLOW_START__ || "";
  var state = { selected: null, changedOnly: true, hideBoundary: false, query: "", kbd: null };
  var views = [];

  function $(sel, root) { return (root || document).querySelector(sel); }
  function all(sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); }
  function nodeEls(id) {
    return all("svg.graph .node").filter(function (el) { return el.getAttribute("data-node") === id; });
  }

  // ---- zoom and pan --------------------------------------------------------

  var MIN_SCALE = 0.1, MAX_SCALE = 4;
  // A node label is drawn at 12 units; below about 11 CSS pixels it stops
  // being readable, which sets the floor the default view may not go under.
  // Setting this to 0.80 means any graph whose fitted scale is 0.80+ uses
  // scale=1 (the browser centres it naturally); a graph that requires more
  // zoom-out than that sits at the readable floor and lets the user pan.
  var MIN_READABLE = 0.80;

  function clamp(v, lo, hi) { return v < lo ? lo : v > hi ? hi : v; }

  function View(canvas) {
    this.canvas = canvas;
    this.viewport = $(".viewport", canvas);
    this.svg = $("svg.graph", canvas);
    this.g = this.svg ? $("g.viewport", this.svg) : null;
    this.level = $(".zoom .level", canvas);
    this.width = this.svg ? parseFloat(this.svg.getAttribute("data-width")) || 1 : 1;
    this.height = this.svg ? parseFloat(this.svg.getAttribute("data-height")) || 1 : 1;
    this.scale = 1; this.x = 0; this.y = 0;
  }

  View.prototype.apply = function () {
    if (!this.g) { return; }
    this.g.setAttribute("transform", "translate(" + this.x.toFixed(2) + "," + this.y.toFixed(2) + ") scale(" + this.scale.toFixed(4) + ")");
    if (this.level) { this.level.textContent = Math.round(this.scale * 100) + "%"; }
  };

  View.prototype.box = function () {
    var r = this.viewport.getBoundingClientRect();
    // The SVG maps its viewBox onto the element, so work in viewBox units.
    return { w: this.width, h: this.height, px: r.width || 1, py: r.height || 1 };
  };

  // fitted reports the scale the browser already applies mapping the viewBox
  // onto the element. Transform scale multiplies it, so a transform of
  // 1/fitted renders the drawing at its intrinsic size.
  View.prototype.fitted = function () {
    var r = this.viewport.getBoundingClientRect();
    var f = Math.min((r.width || 1) / this.width, (r.height || 1) / this.height);
    return f > 0 ? f : 1;
  };

  // fit shows the whole drawing, but never below the scale that keeps labels
  // legible. A tall graph is opened at readable size and panned instead of
  // being shrunk until its text disappears, which is the failure this view
  // exists to avoid.
  View.prototype.fit = function () {
    var f = this.fitted();
    var readable = MIN_READABLE / f;
    // If the graph fits in the viewport at or above the readability floor, fill it.
    // Otherwise zoom to the readable floor and let the user pan.
    if (readable >= 1) {
      // Graph fits: use scale=1 so the browser's own viewBox scaling centres it.
      this.scale = 1; this.x = 0; this.y = 0; this.apply(); return;
    }
    this.scale = Math.max(MIN_READABLE / f, MIN_SCALE);
    // Anchor to the left margin so the first column is always visible.
    this.x = 0; this.y = 0;
    this.apply();
  };

  View.prototype.actual = function () {
    this.scale = Math.min(1 / this.fitted(), MAX_SCALE);
    this.centre();
    this.y = 0;
    this.apply();
  };

  View.prototype.centre = function () {
    this.x = (this.width - this.width * this.scale) / 2;
    this.y = (this.height - this.height * this.scale) / 2;
    this.apply();
  };

  View.prototype.zoomBy = function (factor, cx, cy) {
    var next = clamp(this.scale * factor, MIN_SCALE, MAX_SCALE);
    if (next === this.scale) { return; }
    if (cx === undefined) { cx = this.width / 2; cy = this.height / 2; }
    // Keep the point under the cursor fixed while the scale changes.
    this.x = cx - (cx - this.x) * (next / this.scale);
    this.y = cy - (cy - this.y) * (next / this.scale);
    this.scale = next;
    this.apply();
  };

  // Pointer events arrive in client coordinates; this converts to SVG viewBox space
  // after zoom and pan, so hit-testing in the graph works correctly.
  View.prototype.toViewBox = function (clientX, clientY) {
    var r = this.viewport.getBoundingClientRect();
    var b = this.box();
    var fitted = Math.min(r.width / b.w, r.height / b.h);
    if (!(fitted > 0)) { return { x: 0, y: 0 }; }
    var offX = (r.width - b.w * fitted) / 2;
    var offY = (r.height - b.h * fitted) / 2;
    return { x: (clientX - r.left - offX) / fitted, y: (clientY - r.top - offY) / fitted };
  };

  // revealNode pans so a node sits inside the visible area.
  View.prototype.reveal = function (el) {
    var rect = $("rect", el);
    if (!rect) { return; }
    var nx = parseFloat(rect.getAttribute("x")), ny = parseFloat(rect.getAttribute("y"));
    var nw = parseFloat(rect.getAttribute("width")), nh = parseFloat(rect.getAttribute("height"));
    var b = this.box();
    // Visible window in viewBox units at the current transform.
    var viewW = b.w / this.scale, viewH = b.h / this.scale;
    var left = -this.x / this.scale, top = -this.y / this.scale;
    var cx = nx + nw / 2, cy = ny + nh / 2;
    var pad = 40;
    var changed = false;
    if (cx < left + pad) { left = cx - pad; changed = true; }
    if (cx > left + viewW - pad) { left = cx - viewW + pad; changed = true; }
    if (cy < top + pad) { top = cy - pad; changed = true; }
    if (cy > top + viewH - pad) { top = cy - viewH + pad; changed = true; }
    if (changed) {
      this.x = -left * this.scale;
      this.y = -top * this.scale;
      this.apply();
    }
  };

  function wireView(canvas) {
    var view = new View(canvas);
    if (!view.svg || !view.g) { return null; }
    view.fit();

    all(".zoom button", canvas).forEach(function (button) {
      button.addEventListener("click", function () {
        var kind = button.getAttribute("data-zoom");
        if (kind === "in") { view.zoomBy(1.25); }
        else if (kind === "out") { view.zoomBy(1 / 1.25); }
        else if (kind === "fit") { view.fit(); }
        else if (kind === "actual") { view.actual(); }
      });
    });

    view.viewport.addEventListener("wheel", function (event) {
      if (event.ctrlKey || event.metaKey || Math.abs(event.deltaY) > 0) {
        event.preventDefault();
        var p = view.toViewBox(event.clientX, event.clientY);
        view.zoomBy(event.deltaY < 0 ? 1.12 : 1 / 1.12, p.x, p.y);
      }
    }, { passive: false });

    var dragging = false, lastX = 0, lastY = 0, moved = false;
    view.viewport.addEventListener("pointerdown", function (event) {
      if (event.button !== 0) { return; }
      dragging = true; moved = false;
      lastX = event.clientX; lastY = event.clientY;
      view.viewport.classList.add("grabbing");
      view.viewport.setPointerCapture(event.pointerId);
    });
    view.viewport.addEventListener("pointermove", function (event) {
      if (!dragging) { return; }
      var dx = event.clientX - lastX, dy = event.clientY - lastY;
      if (Math.abs(dx) > 2 || Math.abs(dy) > 2) { moved = true; }
      lastX = event.clientX; lastY = event.clientY;
      var b = view.box();
      var r = view.viewport.getBoundingClientRect();
      var fitted = Math.min(r.width / b.w, r.height / b.h) || 1;
      view.x += dx / fitted; view.y += dy / fitted;
      view.apply();
    });
    function endDrag(event) {
      if (!dragging) { return; }
      dragging = false;
      view.viewport.classList.remove("grabbing");
      if (event.pointerId !== undefined && view.viewport.hasPointerCapture && view.viewport.hasPointerCapture(event.pointerId)) {
        view.viewport.releasePointerCapture(event.pointerId);
      }
    }
    view.viewport.addEventListener("pointerup", endDrag);
    view.viewport.addEventListener("pointercancel", endDrag);
    view.viewport.addEventListener("click", function (event) {
      if (moved) { event.stopPropagation(); event.preventDefault(); moved = false; }
    }, true);

    view.viewport.addEventListener("keydown", function (event) { onViewportKey(event, view); });
    views.push(view);
    return view;
  }

  function viewFor(el) {
    var canvas = el.closest ? el.closest(".canvas") : null;
    for (var i = 0; i < views.length; i++) {
      if (views[i].canvas === canvas) { return views[i]; }
    }
    return null;
  }

  // ---- keyboard navigation -------------------------------------------------

  // orderedNodes lists the drawn nodes of one diagram in reading order, which
  // makes Tab-free arrow navigation predictable.
  function orderedNodes(view) {
    return all("svg.graph .node", view.canvas).filter(function (el) {
      return !el.classList.contains("dimmed");
    }).map(function (el) {
      var rect = $("rect", el);
      return { el: el, x: parseFloat(rect.getAttribute("x")), y: parseFloat(rect.getAttribute("y")) };
    }).sort(function (a, b) { return a.y - b.y || a.x - b.x; });
  }

  function markKeyboard(el, view) {
    all("svg.graph .node.kbd").forEach(function (n) { n.classList.remove("kbd"); });
    if (!el) { state.kbd = null; return; }
    el.classList.add("kbd");
    state.kbd = el.getAttribute("data-node");
    if (view) { view.reveal(el); }
    announce(NODES[state.kbd] ? NODES[state.kbd].label : "");
  }

  function step(view, dir) {
    var list = orderedNodes(view);
    if (!list.length) { return; }
    var index = -1;
    for (var i = 0; i < list.length; i++) {
      if (list[i].el.getAttribute("data-node") === state.kbd) { index = i; break; }
    }
    if (index === -1) { markKeyboard(list[0].el, view); return; }
    var current = list[index];
    var next = null;
    if (dir === "next") { next = list[Math.min(index + 1, list.length - 1)]; }
    else if (dir === "prev") { next = list[Math.max(index - 1, 0)]; }
    else {
      // Vertical movement prefers the nearest node on an adjacent row.
      var candidates = list.filter(function (c) {
        return dir === "down" ? c.y > current.y : c.y < current.y;
      });
      candidates.sort(function (a, b) {
        var da = Math.abs(a.y - current.y), db = Math.abs(b.y - current.y);
        if (da !== db) { return da - db; }
        return Math.abs(a.x - current.x) - Math.abs(b.x - current.x);
      });
      next = candidates[0];
    }
    if (next) { markKeyboard(next.el, view); }
  }

  function onViewportKey(event, view) {
    var key = event.key;
    if (key === "ArrowRight") { event.preventDefault(); step(view, "next"); }
    else if (key === "ArrowLeft") { event.preventDefault(); step(view, "prev"); }
    else if (key === "ArrowDown") { event.preventDefault(); step(view, "down"); }
    else if (key === "ArrowUp") { event.preventDefault(); step(view, "up"); }
    else if (key === "Home") { event.preventDefault(); var f = orderedNodes(view)[0]; if (f) { markKeyboard(f.el, view); } }
    else if (key === "Enter" || key === " ") {
      if (state.kbd) { event.preventDefault(); select(state.kbd); }
    } else if (key === "+" || key === "=") { event.preventDefault(); view.zoomBy(1.25); }
    else if (key === "-" || key === "_") { event.preventDefault(); view.zoomBy(1 / 1.25); }
    else if (key === "0") { event.preventDefault(); view.fit(); }
    else if (key === "Escape") { clearSelection(); }
  }

  function announce(message) {
    var live = $("#live");
    if (live && message) { live.textContent = message; }
  }

  // ---- selection -----------------------------------------------------------

  function select(id, skipHash) {
    if (!NODES[id]) { return; }
    state.selected = id;
    all("svg.graph .node").forEach(function (el) {
      el.classList.toggle("selected", el.getAttribute("data-node") === id);
    });
    var neighbours = {};
    neighbours[id] = true;
    all("svg.graph .edge").forEach(function (el) {
      var from = el.getAttribute("data-from"), to = el.getAttribute("data-to");
      var touches = from === id || to === id;
      el.classList.toggle("active", touches);
      if (touches) { neighbours[from] = true; neighbours[to] = true; }
    });
    renderPanel(id, neighbours);
    updateChangedNav(id);
    applyFilters();
    var target = nodeEls(id)[0];
    if (!target) {
      // The node exists in this flow but the diagram did not draw it, so say
      // so rather than leaving a click that appears to do nothing.
      announce(NODES[id].label + ", not drawn on the diagram");
    }
    if (target) {
      var flow = target.closest("details.flow");
      if (flow && !flow.open) { flow.open = true; }
      var view = viewFor(target);
      if (view) { view.reveal(target); markKeyboard(target, null); }
    }
    if (!skipHash) {
      // Some browsers refuse history writes on file:// URLs; the selection
      // still works, only the shareable link does not update.
      try { history.replaceState(null, "", "#n=" + encodeURIComponent(id)); } catch (ignored) { void ignored; }
    }
  }

  function clearSelection() {
    state.selected = null;
    all("svg.graph .node").forEach(function (el) { el.classList.remove("selected"); });
    all("svg.graph .edge").forEach(function (el) { el.classList.remove("active"); });
    renderPanel(null, null);
    updateChangedNav(null);
    applyFilters();
  }

  // ---- side panel ----------------------------------------------------------

  function text(tag, value, cls) {
    var el = document.createElement(tag);
    el.textContent = value;
    if (cls) { el.className = cls; }
    return el;
  }

  function renderPanel(id, neighbours) {
    var panel = $("#panel-body");
    panel.textContent = "";
    var node = id ? NODES[id] : null;
    if (!node) {
      var emptyDiv = document.createElement("div");emptyDiv.className="panel-empty-hint";var icon=document.createElement("div");icon.className="hint-icon";icon.textContent="→";emptyDiv.appendChild(icon);var hint=document.createElement("p");hint.className="hint-text";hint.textContent="Select any node to see its source, callers, and callees.";emptyDiv.appendChild(hint);panel.appendChild(emptyDiv);
      appendChangedIndex(panel);
      return;
    }
    panel.appendChild(text("p", node.label, "panel-title"));
    if (node.drawn === false) {
      panel.appendChild(text("p", "Not drawn on the diagram: this flow was trimmed to stay readable. Its relationships are listed below.", "panel-start"));
    }

    var chips = document.createElement("div");
    chips.className = "chips";
    chips.appendChild(text("span", node.state, "chip state-" + node.state));
    if (node.kind) { chips.appendChild(text("span", node.kind, "chip")); }
    if (node.boundary) { chips.appendChild(text("span", "boundary", "chip res-unresolved")); }
    if (node.reason) { chips.appendChild(text("span", node.reason, "chip res-unresolved")); }
    panel.appendChild(chips);

    var where = document.createElement("p");
    where.className = "panel-path";
    if (node.link) {
      var a = document.createElement("a");
      a.href = node.link;
      a.rel = "noreferrer noopener";
      a.target = "_blank";
      a.textContent = node.path + ":" + node.line;
      where.appendChild(a);
    } else {
      where.textContent = node.path + ":" + node.line;
    }
    panel.appendChild(where);

    if (node.snippet) {
      panel.appendChild(text("span", "Source", "panel-section-label"));
      var codeWrap = document.createElement("div");
      codeWrap.className = "code-wrap";
      var pre = document.createElement("pre");
      pre.className = "code";
      // Snippet HTML is produced and escaped by the Go highlighter.
      pre.innerHTML = node.snippet;
      codeWrap.appendChild(pre);
      panel.appendChild(codeWrap);
    }

    appendRefs(panel, "Called by", node.callers);
    appendRefs(panel, "Calls", node.callees);

    if (neighbours) {
      var count = Object.keys(neighbours).length - 1;
      panel.appendChild(text("p", count === 1 ? "1 direct neighbour highlighted." : count + " direct neighbours highlighted.", "hint"));
    }
  }


  // buildChangedNav populates the persistent changed-declarations navigator at
  // the top of the panel. It runs once on boot so the list is always visible,
  // and select() calls updateChangedNav(id) to highlight the active item.
  function buildChangedNav() {
    var navEl = document.getElementById("panel-changed-nav");
    var listEl = document.getElementById("panel-changed-list");
    if (!navEl || !listEl) { return; }
    var changed = Object.keys(NODES).filter(function (id) { return NODES[id].changed; });
    if (!changed.length) { navEl.style.display = "none"; return; }
    changed.sort(function (a, b) {
      var na = NODES[a], nb = NODES[b];
      var da = (na.callers || []).length + (na.callees || []).length;
      var db = (nb.callers || []).length + (nb.callees || []).length;
      return db - da || na.label.localeCompare(nb.label);
    });
    var h3 = navEl.querySelector("h3");
    if (h3) { h3.textContent = "Changed (" + changed.length + ")"; }
    listEl.textContent = "";
    var shown = changed.slice(0, 40);
    shown.forEach(function (id) {
      var node = NODES[id];
      var li = document.createElement("li");
      var button = document.createElement("button");
      button.type = "button";
      button.dataset.navId = id;
      button.textContent = node.label;
      button.addEventListener("click", function () { select(id); });
      li.appendChild(button);
      var badge = document.createElement("span");
      badge.className = "rel chip state-" + node.state;
      badge.textContent = node.state.charAt(0).toUpperCase() + node.state.slice(1);
      li.appendChild(badge);
      listEl.appendChild(li);
    });
    if (changed.length > 40) {
      var more = document.createElement("li");
      more.style.cssText = "color:var(--dim);font-size:12px;padding:6px 0";
      more.textContent = "\u2026 " + (changed.length - 40) + " more (use search)";
      listEl.appendChild(more);
    }
  }

  function updateChangedNav(id) {
    var listEl = document.getElementById("panel-changed-list");
    if (!listEl) { return; }
    listEl.querySelectorAll("button[data-nav-id]").forEach(function (btn) {
      btn.classList.toggle("active-nav", btn.dataset.navId === id);
    });
    // Scroll active item into view within the nav list
    if (id) {
      var activeBtn = listEl.querySelector("button[data-nav-id=\"" + id + "\"]");
      if (activeBtn) {
        // Avoid page-level scroll when the panel is stacked (non-sticky) at narrow widths.
        var panel = document.getElementById("panel");
        if (panel && getComputedStyle(panel).position === "sticky") {
          activeBtn.scrollIntoView({ block: "nearest" });
        }
      }
    }
  }



  // appendChangedIndex gives the empty panel a way in: the declarations this
  // change touched, which is what a reviewer opened the document to find.
  function appendChangedIndex(panel) {
    var changed = Object.keys(NODES).filter(function (id) { return NODES[id].changed; });
    if (!changed.length) { return; }
    changed.sort(function (a, b) {
      var na = NODES[a], nb = NODES[b];
      var da = (na.callers || []).length + (na.callees || []).length;
      var db = (nb.callers || []).length + (nb.callees || []).length;
      return db - da || na.label.localeCompare(nb.label);
    });
    panel.appendChild(text("h3", "Changed declarations (" + changed.length + ")"));
    var list = document.createElement("ul");
    list.className = "refs";
    changed.slice(0, 30).forEach(function (id) {
      var node = NODES[id];
      var li = document.createElement("li");
      var button = document.createElement("button");
      button.type = "button";
      button.textContent = node.label;
      button.addEventListener("click", function () { select(id); });
      li.appendChild(button);
      var badge = document.createElement("span");
      badge.className = "rel chip state-" + node.state;
      badge.textContent = node.state;
      li.appendChild(badge);
      list.appendChild(li);
    });
    if (changed.length > 30) {
      var more = document.createElement("li");
      more.className = "hint";
      more.textContent = "… " + (changed.length - 30) + " more (use search to find them)";
      list.appendChild(more);
    }
    
    panel.appendChild(list);
  }

  function appendRefs(panel, title, refs) {
    if (!refs || !refs.length) { return; }
    panel.appendChild(text("span", title + " (" + refs.length + ")", "panel-section-label"));
    var list = document.createElement("ul");
    list.className = "refs";
    refs.forEach(function (ref) {
      var li = document.createElement("li");
      var button = document.createElement("button");
      button.type = "button";
      button.textContent = ref.label;
      button.addEventListener("click", function () { select(ref.id); });
      li.appendChild(button);
      var rel = ref.kind + (ref.resolution && ref.resolution !== "resolved" ? " · " + ref.resolution : "");
      li.appendChild(text("span", rel, "rel"));
      list.appendChild(li);
    });
    panel.appendChild(list);
  }

  // ---- filters -------------------------------------------------------------

  function matches(node) {
    if (!node) { return false; }
    if (state.changedOnly && !node.changed) { return false; }
    if (state.hideBoundary && node.boundary) { return false; }
    if (state.query) {
      var hay = (node.label + " " + node.path + " " + (node.reason || "")).toLowerCase();
      if (hay.indexOf(state.query) === -1) { return false; }
    }
    return true;
  }


  function applyFilters() {
    var visible = {};
    all("svg.graph .node").forEach(function (el) {
      var id = el.getAttribute("data-node");
      var ok = matches(NODES[id]);
      el.classList.toggle("dimmed", !ok);
      // A filtered-out node leaves the accessibility tree too, so a screen
      // reader and a sighted reader are told the same thing.
      if (ok) { el.removeAttribute("aria-hidden"); } else { el.setAttribute("aria-hidden", "true"); }
      if (ok) { visible[id] = true; }
    });
    all("svg.graph .edge").forEach(function (el) {
      var live = visible[el.getAttribute("data-from")] && visible[el.getAttribute("data-to")];
      el.classList.toggle("dimmed", !live);
      if (live) { el.removeAttribute("aria-hidden"); } else { el.setAttribute("aria-hidden", "true"); }
    });
    var shown = Object.keys(visible).length;
    var drawnTotal = Object.keys(NODES).filter(function (id) { return NODES[id].drawn; }).length;
    var total = Object.keys(NODES).length;
    var status = $("#filter-status");
    if (status) {
      var base = shown === drawnTotal
        ? drawnTotal + " node(s) drawn"
        : shown + " of " + drawnTotal + " drawn";
      // The count says what is on the canvas and what exists behind it, so a
      // trimmed diagram never reads as the whole flow.
      status.textContent = total > drawnTotal ? base + " · " + total + " in scope" : base;
    }
    all("details.flow").forEach(function (flow) {
      var any = all("svg.graph .node", flow).some(function (el) { return !el.classList.contains("dimmed"); });
      flow.classList.toggle("hidden", !any && (state.changedOnly || state.hideBoundary || !!state.query));
    });
  }

  // ---- wiring --------------------------------------------------------------

  document.addEventListener("click", function (event) {
    var node = event.target.closest ? event.target.closest("svg.graph .node") : null;
    if (node) {
      event.preventDefault();
      select(node.getAttribute("data-node"));
    }
  });

  document.addEventListener("keydown", function (event) {
    if (event.key === "Escape") { clearSelection(); }
  });

  var search = $("#search");
  if (search) {
    search.addEventListener("input", function () {
      state.query = search.value.trim().toLowerCase();
      applyFilters();
    });
  }
  function wireToggle(id, key) {
    var button = $("#" + id);
    if (!button) { return; }
    button.addEventListener("click", function () {
      state[key] = !state[key];
      button.setAttribute("aria-pressed", state[key] ? "true" : "false");
      applyFilters();
      // Re-fit all diagrams so the visible nodes fill their viewport after the
      // filter changes; without this the view can be left zoomed into hidden area.
      views.forEach(function (v) { v.fit(); });
    });
  }
  wireToggle("changed-only", "changedOnly");
  wireToggle("hide-boundary", "hideBoundary");

  var expand = $("#expand-all");
  if (expand) {
    expand.addEventListener("click", function () {
      var open = all("details.flow").some(function (d) { return !d.open; });
      all("details.flow").forEach(function (d) { d.open = open; });
      expand.textContent = open ? "Collapse all" : "Expand all";
      views.forEach(function (v) { v.fit(); });
    });
  }
  var reset = $("#reset");
  if (reset) {
    reset.addEventListener("click", function () {
      state.query = ""; state.changedOnly = true; state.hideBoundary = false;
      if (search) { search.value = ""; }
      var defaults = { "changed-only": "true", "hide-boundary": "false" };
      ["changed-only", "hide-boundary"].forEach(function (id) {
        var b = $("#" + id);
        if (b) { b.setAttribute("aria-pressed", defaults[id]); }
      });
      views.forEach(function (v) { v.fit(); });
      clearSelection();
    });
  }

  // ---- panel resize --------------------------------------------------------

  var PANEL_MIN = 320, PANEL_MAX = 1200, PANEL_KEY = "nitpick-flow-panel-v4";

  function wirePanelResize() {
    var handle = $("#panel-resize");
    var main = $("main");
    if (!handle || !main) { return; }

    function clampWidth(w) {
      var cap = Math.max(PANEL_MIN, Math.min(PANEL_MAX, innerWidth - 320));
      return Math.round(Math.max(PANEL_MIN, Math.min(cap, w)));
    }

    function setWidth(w) {
      w = clampWidth(w);
      document.documentElement.style.setProperty("--panel-width", w + "px");
      handle.setAttribute("aria-valuenow", String(w));
      try { localStorage.setItem(PANEL_KEY, String(w)); } catch (ignored) { void ignored; }
      return w;
    }

    var saved = null;
    try { saved = parseInt(localStorage.getItem(PANEL_KEY), 10); } catch (ignored) { void ignored; }
    if (saved) { setWidth(saved); }

    var dragging = false, startX = 0, startWidth = 0;

    handle.addEventListener("pointerdown", function (event) {
      dragging = true;
      startX = event.clientX;
      startWidth = parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--panel-width")) || 420;
      handle.classList.add("dragging");
      main.classList.add("resizing");
      handle.setPointerCapture(event.pointerId);
    });
    handle.addEventListener("pointermove", function (event) {
      if (!dragging) { return; }
      // The panel sits right of the handle, so dragging left widens it.
      setWidth(startWidth - (event.clientX - startX));
    });
    function endDrag(event) {
      if (!dragging) { return; }
      dragging = false;
      handle.classList.remove("dragging");
      main.classList.remove("resizing");
      if (event.pointerId !== undefined && handle.hasPointerCapture && handle.hasPointerCapture(event.pointerId)) {
        handle.releasePointerCapture(event.pointerId);
      }
    }
    handle.addEventListener("pointerup", endDrag);
    handle.addEventListener("pointercancel", endDrag);
    handle.addEventListener("dblclick", function () { setWidth(560); });
    handle.addEventListener("keydown", function (event) {
      var current = parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--panel-width")) || 420;
      var step = event.shiftKey ? 80 : 24;
      if (event.key === "ArrowLeft") { event.preventDefault(); setWidth(current + step); }
      else if (event.key === "ArrowRight") { event.preventDefault(); setWidth(current - step); }
      else if (event.key === "Home") { event.preventDefault(); setWidth(PANEL_MIN); }
      else if (event.key === "End") { event.preventDefault(); setWidth(PANEL_MAX); }
      else if (event.key === "Enter" || event.key === " ") { event.preventDefault(); setWidth(560); }
    });
    window.addEventListener("resize", function () { setWidth(clampWidth(parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--panel-width")) || 420)); });
  }



    wirePanelResize();

  var live = document.createElement("div");
  live.id = "live";
  live.className = "hidden";
  live.setAttribute("aria-live", "polite");
  document.body.appendChild(live);

  all(".canvas").forEach(wireView);

  // ---- node hover tooltip ---------------------------------------------------
  // Shows the full label when the truncated SVG text is hard to read.
  var tip = document.createElement("div");
  tip.id = "graph-tip";
  document.body.appendChild(tip);
  var tipTimeout = null;
  document.addEventListener("mouseover", function (event) {
    var node = event.target.closest ? event.target.closest("svg.graph .node") : null;
    if (!node) { tip.classList.remove("visible"); return; }
    var label = node.getAttribute("data-label") || "";
    if (!label) { return; }
    tip.textContent = label;
    tip.classList.add("visible");
  });
  document.addEventListener("mousemove", function (event) {
    var node = event.target.closest ? event.target.closest("svg.graph .node") : null;
    if (!node) { tip.classList.remove("visible"); return; }
    var x = event.clientX + 14, y = event.clientY + 14;
    var tw = tip.offsetWidth, th = tip.offsetHeight;
    if (x + tw > innerWidth - 8) { x = event.clientX - tw - 8; }
    if (y + th > innerHeight - 8) { y = event.clientY - th - 8; }
    tip.style.left = x + "px"; tip.style.top = y + "px";
  });
  document.addEventListener("mouseout", function (event) {
    var node = event.target.closest ? event.target.closest("svg.graph .node") : null;
    if (node) { return; }
    tip.classList.remove("visible");
  });

  window.addEventListener("resize", function () { views.forEach(function (v) { v.apply(); }); });

  renderPanel(null, null);
  buildChangedNav();
  try { applyFilters(); } catch (ignored) { void ignored; }
  // Sync toggle buttons to match the initial state values.
  var changedOnlyBtn = $("#changed-only");
  if (changedOnlyBtn) { changedOnlyBtn.setAttribute("aria-pressed", state.changedOnly ? "true" : "false"); }

  // A shared link opens on its node; otherwise the document opens on the
  // declaration the change most affects, so the panel is never dead on arrival.
  var hash = (location.hash || "").replace(/^#n=/, "");
  var initial = hash ? decodeURIComponent(hash) : START;
  if (initial && NODES[initial]) { select(initial, true); }
})();
