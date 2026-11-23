"use strict";

const themeKey = "shengji.theme";

// preferredTheme keeps light as the default even on dark Windows installations.
function preferredTheme() {
  try { return localStorage.getItem(themeKey) === "dark" ? "dark" : "light"; }
  catch { return "light"; }
}

// applyTheme updates the document and the accessible label for the next available theme.
function applyTheme(theme) {
  const dark = theme === "dark";
  document.documentElement.dataset.theme = dark ? "dark" : "light";
  document.querySelector('meta[name="theme-color"]').content = dark ? "#141722" : "#f7f8fc";
  const button = document.getElementById("theme-toggle");
  if (!button) return;
  const label = dark ? "切换到浅色主题" : "切换到深色主题";
  button.setAttribute("aria-label", label);
  button.setAttribute("aria-pressed", String(dark));
  button.title = label;
  button.querySelector(".theme-text").textContent = dark ? "浅色" : "深色";
}

// toggleTheme stores the current choice in the browser or WebView profile.
function toggleTheme() {
  const theme = document.documentElement.dataset.theme === "dark" ? "light" : "dark";
  try { localStorage.setItem(themeKey, theme); } catch {}
  applyTheme(theme);
}

applyTheme(preferredTheme());
document.addEventListener("DOMContentLoaded", () => {
  applyTheme(preferredTheme());
  document.getElementById("theme-toggle").addEventListener("click", toggleTheme);
});
window.addEventListener("storage", (event) => {
  if (event.key === themeKey) applyTheme(preferredTheme());
});
