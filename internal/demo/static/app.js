"use strict";

const form = document.getElementById("search");
const statusLine = document.getElementById("status");
const results = document.getElementById("results");

function el(tag, className, ...children) {
  const e = document.createElement(tag);
  e.className = className;
  e.append(...children);
  return e;
}

function renderPlace(place) {
  const m = place.distanceMeters;
  const distance = m < 1000 ? `${m} m` : `${(m / 1000).toFixed(1)} km`;
  const lines = (place.lines ?? []).map(({ mode, ref }) => el("span", `line ${mode}`, ref));
  return el("li", "",
    el("div", "", el("div", place.name ? "" : "unnamed", place.name ?? "unnamed"), el("div", "lines", ...lines)),
    el("span", "distance", place.walkingMinutes <= 60 ? `${place.walkingMinutes} min · ${distance}` : distance),
  );
}

function renderCategory(category) {
  return el("section", "",
    el("h2", "", category.id.replaceAll("_", " ")),
    el("ol", "", ...category.places.map(renderPlace)),
  );
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  // Relative, so the page also works behind a reverse proxy with a prefix.
  const url = new URL("../v1/surroundings", location.href);
  url.search = new URLSearchParams({ address: form.address.value, preset: form.preset.value, limit: 3 });
  form.querySelector("button").disabled = true;
  statusLine.textContent = "";
  try {
    const resp = await fetch(url);
    const data = await resp.json();
    if (!resp.ok) throw new Error(data.error ?? `HTTP ${resp.status}`);
    const found = data.categories.filter((c) => c.places.length > 0);
    results.replaceChildren(...found.map(renderCategory));
    if (found.length === 0) statusLine.textContent = "Nothing nearby.";
  } catch (err) {
    results.replaceChildren();
    statusLine.textContent = err.message;
  } finally {
    form.querySelector("button").disabled = false;
  }
});
