// Sets the theme class before the first paint, so a page load does not flash the other theme.
// next-themes (src/app/app.tsx) takes control after the app starts. Keep the storage key the same.
// This is a file and not an inline script, so a Content-Security-Policy with script-src 'self' allows it.
(function () {
  try {
    var choice = localStorage.getItem("care-pi.theme") || "system";
    var dark = choice === "dark" || (choice === "system" && matchMedia("(prefers-color-scheme: dark)").matches);
    var root = document.documentElement;
    root.classList.add(dark ? "dark" : "light");
    root.style.colorScheme = dark ? "dark" : "light";
  } catch {
    // No storage (private mode): next-themes sets the theme after the app starts.
  }
})();
