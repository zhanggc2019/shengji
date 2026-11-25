"use strict";

const themeKey = "shengji.theme";
const windowsAccent = new URLSearchParams(location.search).get("windows-accent");
const desktopTheme = windowsAccent !== null;
const systemDark = matchMedia("(prefers-color-scheme: dark)");

function defaultTheme() {
  return desktopTheme && systemDark.matches ? "dark" : "light";
}

function preferredTheme() {
  try { return localStorage.getItem(themeKey) || defaultTheme(); }
  catch { return defaultTheme(); }
}

function applyWindowsAccent(color) {
  if (!desktopTheme) return;
  const root = document.documentElement;
  const {style} = root;
  const hasAccent = /^#[\da-f]{6}$/i.test(color);
  root.toggleAttribute("data-system-accent", hasAccent);
  if (!hasAccent) {
    style.removeProperty("--system-accent");
    style.removeProperty("--system-on-accent");
    style.removeProperty("--system-hover-toward");
    return;
  }
  const channels = color.slice(1).match(/../g).map((hex) => {
    const value = parseInt(hex, 16) / 255;
    return value <= .04045 ? value / 12.92 : ((value + .055) / 1.055) ** 2.4;
  });
  const luminance = channels[0] * .2126 + channels[1] * .7152 + channels[2] * .0722;
  const useDarkText = luminance > .179;
  style.setProperty("--system-accent", color);
  style.setProperty("--system-on-accent", useDarkText ? "#000" : "#fff");
  style.setProperty("--system-hover-toward", useDarkText ? "#fff" : "#000");
}

// applyTheme updates the document and the accessible label for the next available theme.
function applyTheme(theme) {
  const dark = theme === "dark";
  document.documentElement.dataset.theme = dark ? "dark" : "light";
  document.querySelector('meta[name="theme-color"]').content = dark ? "#202020" : "#f9f9f9";
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
  try {
    if (theme === defaultTheme()) localStorage.removeItem(themeKey);
    else localStorage.setItem(themeKey, theme);
  } catch {}
  applyTheme(theme);
}

if (desktopTheme) {
  document.documentElement.dataset.platform = "windows";
  applyWindowsAccent(windowsAccent);
  systemDark.addEventListener("change", () => applyTheme(preferredTheme()));
}
applyTheme(preferredTheme());
document.addEventListener("DOMContentLoaded", () => {
  applyTheme(preferredTheme());
  document.getElementById("theme-toggle").addEventListener("click", toggleTheme);
});
window.addEventListener("storage", (event) => {
  if (event.key === themeKey) applyTheme(preferredTheme());
});
