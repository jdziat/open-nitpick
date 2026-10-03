(() => {
  const narrow = window.matchMedia("(max-width: 76.234375em)");
  const mobileSearch = window.matchMedia("(max-width: 59.984375em)");
  const drawer = document.querySelector('[data-md-toggle="drawer"]');
  const search = document.querySelector('[data-md-toggle="search"]');
  const sidebar = document.querySelector(".md-sidebar--primary");
  const searchPanel = document.querySelector(".md-search");
  const drawerTrigger = document.querySelector('label.md-header__button[for="__drawer"]');
  const searchTrigger = document.querySelector('label.md-header__button[for="__search"]');
  const searchInput = document.querySelector('[data-md-component="search-query"]');

  if (typeof document$ !== "undefined") {
    document$.subscribe(() => requestAnimationFrame(setupScrollableTables));
  } else {
    window.addEventListener("load", setupScrollableTables, { once: true });
  }

  if (!drawer || !sidebar || !drawerTrigger) return;

  let keyboardActivation = null;

  function setupScrollableTables() {
    document.querySelectorAll(".md-typeset__scrollwrap").forEach((wrap, index) => {
      const table = wrap.querySelector("table");
      if (!table || wrap.dataset.npScrollTable) return;
      wrap.dataset.npScrollTable = "true";

      const hint = document.createElement("p");
      hint.className = "np-table-scroll-hint";
      hint.id = `np-table-scroll-hint-${index + 1}`;
      hint.textContent = "Scroll table horizontally →";
      hint.hidden = true;
      wrap.before(hint);

      function refresh() {
        const overflows = wrap.scrollWidth > wrap.clientWidth + 1;
        hint.hidden = !overflows;
        wrap.classList.toggle("np-table-scrollwrap--overflowing", overflows);

        if (overflows) {
          wrap.tabIndex = 0;
          wrap.setAttribute("role", "region");
          wrap.setAttribute("aria-label", "Scrollable table; use left and right arrow keys to see all columns");
          wrap.setAttribute("aria-describedby", hint.id);
          return;
        }

        wrap.removeAttribute("tabindex");
        wrap.removeAttribute("role");
        wrap.removeAttribute("aria-label");
        wrap.removeAttribute("aria-describedby");
      }

      wrap.addEventListener("keydown", (event) => {
        if (!wrap.classList.contains("np-table-scrollwrap--overflowing")) return;

        const amount = Math.max(40, wrap.clientWidth * 0.6);
        if (event.key === "ArrowLeft") wrap.scrollBy({ left: -amount });
        else if (event.key === "ArrowRight") wrap.scrollBy({ left: amount });
        else if (event.key === "Home") wrap.scrollTo({ left: 0 });
        else if (event.key === "End") wrap.scrollTo({ left: wrap.scrollWidth });
        else return;
        event.preventDefault();
      });

      const observer = new ResizeObserver(refresh);
      observer.observe(wrap);
      observer.observe(table);
      refresh();
    });
  }

  function setPanel(panel, open) {
    panel.inert = !open;
    if (open) panel.removeAttribute("aria-hidden");
    else panel.setAttribute("aria-hidden", "true");
  }

  function sync() {
    if (!narrow.matches) {
      setPanel(sidebar, true);
      if (searchPanel) setPanel(searchPanel, true);
      return;
    }

    setPanel(sidebar, drawer.checked);
    if (searchPanel && search) setPanel(searchPanel, !mobileSearch.matches || search.checked);
  }

  function firstFocusable(panel) {
    return panel.querySelector(
      'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
    );
  }

  function bindTrigger(trigger, toggle, panel, label, focusTarget) {
    if (!trigger || !toggle || !panel) return;

    if (!panel.id) panel.id = `np-${label.toLowerCase().replace(/\s+/g, "-")}-panel`;
    trigger.setAttribute("role", "button");
    trigger.setAttribute("tabindex", "0");
    trigger.setAttribute("aria-controls", panel.id);
    trigger.setAttribute("aria-label", label);

    let ignoreNextClick = false;

    function expanded() {
      trigger.setAttribute("aria-expanded", String(toggle.checked));
    }

    trigger.addEventListener("click", (event) => {
      if (!ignoreNextClick) return;
      event.preventDefault();
      ignoreNextClick = false;
    });

    trigger.addEventListener("keydown", (event) => {
      if (event.key !== "Enter" && event.key !== " ") return;
      event.preventDefault();
      keyboardActivation = { trigger, focusTarget };
      // Material synthesizes a click for Enter on labels. Let this handler own
      // the keyboard toggle so that the native label action cannot close it again.
      ignoreNextClick = true;
      window.setTimeout(() => { ignoreNextClick = false; }, 0);
      toggle.checked = !toggle.checked;
      toggle.dispatchEvent(new Event("change", { bubbles: true }));
    });

    trigger.addEventListener("keyup", (event) => {
      if (event.key === "Enter" || event.key === " ") event.preventDefault();
    });

    toggle.addEventListener("change", () => {
      sync();
      expanded();
      if (!toggle.checked) {
        if (keyboardActivation?.trigger === trigger) keyboardActivation = null;
        return;
      }
      if (keyboardActivation?.trigger !== trigger) return;
      const target = focusTarget?.() || firstFocusable(panel);
      requestAnimationFrame(() => target?.focus());
      keyboardActivation = null;
    });

    expanded();
  }

  bindTrigger(
    drawerTrigger,
    drawer,
    sidebar,
    "Open navigation",
    () => sidebar.querySelector(".md-nav--primary > .md-nav__list > .md-nav__item > .md-nav__link[href]") || firstFocusable(sidebar),
  );
  bindTrigger(
    searchTrigger,
    search,
    searchPanel,
    "Open search",
    () => searchInput,
  );

  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || !narrow.matches) return;

    if (search?.checked) {
      search.checked = false;
      search.dispatchEvent(new Event("change", { bubbles: true }));
      searchTrigger?.focus();
      return;
    }

    if (drawer.checked) {
      drawer.checked = false;
      drawer.dispatchEvent(new Event("change", { bubbles: true }));
      drawerTrigger.focus();
    }
  });

  narrow.addEventListener("change", sync);
  mobileSearch.addEventListener("change", sync);
  sync();
})();
