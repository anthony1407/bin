// Applies the saved (or system) colour theme before the first paint, so the
// page never flashes the wrong theme. Kept as a file: the console's CSP allows
// no inline scripts.
(function () {
  var saved = null;
  try { saved = localStorage.getItem("bin-theme"); } catch (e) {}
  var dark = saved ? saved === "dark" : window.matchMedia("(prefers-color-scheme: dark)").matches;
  document.documentElement.classList.toggle("dark", dark);
})();
