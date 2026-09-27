// Applies the stored colour mode before first paint so a light-mode reader does
// not see a dark flash. Loaded synchronously from the document head; the rest of
// the dashboard enhancement is deferred.
(() => {
  try {
    const stored = window.localStorage.getItem("ocbench-theme");
    if (stored === "light" || stored === "dark") {
      document.documentElement.dataset.theme = stored;
    }
  } catch (error) {
    // A blocked localStorage must not stop the page from rendering.
  }
})();
