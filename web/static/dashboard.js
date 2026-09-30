(() => {
  "use strict";

  const root = document.documentElement;

  const syncTheme = () => {
    const light = root.dataset.theme === "light";
    document.querySelectorAll("[data-theme-label]").forEach((el) => { el.textContent = light ? "Light" : "Dark"; });
    document.querySelectorAll("[data-theme-toggle]").forEach((el) => { el.setAttribute("aria-pressed", String(light)); });
  };
  syncTheme();
  document.querySelectorAll("[data-theme-toggle]").forEach((button) => {
    button.addEventListener("click", () => {
      root.dataset.theme = root.dataset.theme === "light" ? "dark" : "light";
      try { window.localStorage.setItem("ocbench-theme", root.dataset.theme); } catch (error) { /* ignore */ }
      syncTheme();
    });
  });

  document.querySelectorAll("[data-profile-filter]").forEach((select) => {
    select.addEventListener("change", () => {
      const url = new URL(window.location.href);
      if (select.value) url.searchParams.set("profile", select.value);
      else url.searchParams.delete("profile");
      url.searchParams.delete("run");
      window.location.assign(url);
    });
  });

  document.querySelectorAll("[data-sortable]").forEach((table) => {
    const body = table.tBodies[0];
    if (!body) return;
    // Direction is remembered per column, so switching columns starts fresh
    // rather than inheriting the previous column's direction.
    const direction = new Map();
    const headers = table.querySelectorAll("[data-sort-key]");
    headers.forEach((button) => {
      button.addEventListener("click", () => {
        const key = button.dataset.sortKey;
        const ascending = !direction.get(key);
        direction.set(key, ascending);
        headers.forEach((other) => other.closest("th")?.removeAttribute("aria-sort"));
        button.closest("th")?.setAttribute("aria-sort", ascending ? "ascending" : "descending");

        const rows = Array.from(body.rows);
        rows.sort((a, b) => {
          const value = (row) => Number.parseFloat(row.dataset[key]);
          const left = value(a), right = value(b);
          // A missing value ("—") is NaN: keep it last in either direction.
          const leftMissing = Number.isNaN(left), rightMissing = Number.isNaN(right);
          if (leftMissing && rightMissing) return 0;
          if (leftMissing) return 1;
          if (rightMissing) return -1;
          return ascending ? left - right : right - left;
        });
        rows.forEach((row) => body.append(row));
      });
    });
  });

  document.querySelectorAll("tr[data-href]").forEach((row) => {
    const open = () => window.location.assign(row.dataset.href);
    row.addEventListener("click", (event) => { if (!event.target.closest("a,button")) open(); });
    row.addEventListener("keydown", (event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); open(); } });
  });
})();
