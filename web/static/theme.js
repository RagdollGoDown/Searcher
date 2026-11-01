// Loaded as a blocking script in <head> so the cached theme is applied
// before first paint; waiting for the server would flash the default
// palette on every navigation. The server's value is still the source of
// truth, so it's fetched afterward to pick up a change made from another
// browser. Without a store the endpoint 404s and the cache is all there is.
const themeStorageKey = "ui.theme";

function applyTheme(theme) {
  document.documentElement.dataset.theme = theme;
  localStorage.setItem(themeStorageKey, theme);
}

(() => {
  const cached = localStorage.getItem(themeStorageKey);
  if (cached) document.documentElement.dataset.theme = cached;

  fetch("/api/ui/settings")
    .then((res) => (res.ok ? res.json() : null))
    .then((data) => {
      if (data && data.theme) applyTheme(data.theme);
    })
    .catch(() => {});
})();
