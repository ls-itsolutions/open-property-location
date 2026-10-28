"use strict";

// The API returns category ids only; clients bring their own labels.
const labels = {
  restaurant: "Restaurants",
  cafe: "Cafés",
  bar: "Bars and pubs",
  bakery: "Bakeries",
  supermarket: "Supermarkets",
  pharmacy: "Pharmacies",
  doctor: "Doctors",
  hospital: "Hospitals",
  kindergarten: "Kindergartens",
  school: "Schools",
  bus_tram_stop: "Bus and tram stops",
  train_station: "Train stations",
  attraction: "Sights",
  beach: "Beaches",
  park: "Parks",
  playground: "Playgrounds",
  parking: "Parking",
  ev_charging: "EV charging",
};

const coordinates = /^\s*(-?\d+(?:\.\d+)?)\s*[,;\s]\s*(-?\d+(?:\.\d+)?)\s*$/;

const form = document.getElementById("search");
const statusLine = document.getElementById("status");
const results = document.getElementById("results");
const radii = new Map();

function api(path) {
  // Relative, so the page also works behind a reverse proxy with a prefix.
  return new URL("../" + path, location.href);
}

function formatDistance(m) {
  return m < 1000 ? `${m} m` : `${(m / 1000).toFixed(1)} km`;
}

function setStatus(text, isError = false) {
  statusLine.textContent = text;
  statusLine.classList.toggle("error", isError);
}

function link(href, text) {
  const a = document.createElement("a");
  a.href = href;
  a.textContent = text;
  a.target = "_blank";
  a.rel = "noopener noreferrer";
  return a;
}

function renderPlace(place) {
  const li = document.getElementById("place").content.cloneNode(true);
  const name = li.querySelector(".name");
  name.textContent = place.name ?? "unnamed";
  name.classList.toggle("unnamed", place.name === null);
  li.querySelector(".distance").textContent =
    `${formatDistance(place.distanceMeters)} · ${place.walkingMinutes} min`;

  const details = li.querySelector(".details");
  const a = place.address;
  const street = [a.street, a.houseNumber].filter(Boolean).join(" ");
  const city = [a.postcode, a.city].filter(Boolean).join(" ");
  const lines = [[street, city].filter(Boolean).join(", "), place.openingHours];
  for (const text of lines.filter(Boolean)) {
    const div = document.createElement("div");
    div.textContent = text;
    details.append(div);
  }
  const links = document.createElement("div");
  if (place.website) links.append(link(place.website, "website"));
  if (place.wikidata) links.append(link(`https://www.wikidata.org/wiki/${place.wikidata}`, "wikidata"));
  links.append(link(place.osmUrl, "openstreetmap"));
  details.append(links);
  return li;
}

function renderCategory(category) {
  const section = document.getElementById("category").content.cloneNode(true);
  section.querySelector(".label").textContent = labels[category.id] ?? category.id;
  const radius = radii.get(category.id);
  if (radius) section.querySelector(".radius").textContent = `within ${formatDistance(radius)}`;
  const list = section.querySelector("ol");
  if (category.places.length === 0) {
    const li = document.createElement("li");
    li.className = "empty";
    li.textContent = "Nothing found.";
    list.append(li);
  }
  for (const place of category.places) list.append(renderPlace(place));
  return section;
}

function render(data, requestURL) {
  const { lat, lon } = data.location;
  document.getElementById("location").textContent = `${lat.toFixed(5)}, ${lon.toFixed(5)}`;
  document.getElementById("map").href =
    `https://www.openstreetmap.org/?mlat=${lat}&mlon=${lon}#map=16/${lat}/${lon}`;
  document.getElementById("data-version").textContent = data.dataVersion;
  document.getElementById("summary").hidden = false;
  results.replaceChildren(...data.categories.map(renderCategory));

  const request = document.getElementById("request");
  request.href = requestURL;
  request.textContent = requestURL.pathname + requestURL.search;
  document.getElementById("json").textContent = JSON.stringify(data, null, 2);
  document.getElementById("raw").hidden = false;
}

async function search() {
  const q = form.q.value.trim();
  const url = api("v1/surroundings");
  const match = q.match(coordinates);
  if (match) {
    url.searchParams.set("lat", match[1]);
    url.searchParams.set("lon", match[2]);
  } else {
    url.searchParams.set("address", q);
  }
  url.searchParams.set("preset", form.preset.value);
  url.searchParams.set("limit", form.limit.value);

  const page = new URLSearchParams({ q, preset: form.preset.value, limit: form.limit.value });
  history.replaceState(null, "", "?" + page);

  form.querySelector("button").disabled = true;
  setStatus("Searching…");
  try {
    const resp = await fetch(url);
    const data = await resp.json();
    if (!resp.ok) throw new Error(data.error ?? `HTTP ${resp.status}`);
    render(data, url);
    setStatus("");
  } catch (err) {
    setStatus(err.message, true);
  } finally {
    form.querySelector("button").disabled = false;
  }
}

async function init() {
  const resp = await fetch(api("v1/categories"));
  const { categories, presets } = await resp.json();
  for (const c of categories) radii.set(c.id, c.maxDistanceMeters);
  for (const name of Object.keys(presets)) form.preset.add(new Option(name, name));

  const params = new URLSearchParams(location.search);
  if (params.has("preset")) form.preset.value = params.get("preset");
  if (params.has("limit")) form.limit.value = params.get("limit");
  if (params.has("q")) {
    form.q.value = params.get("q");
    search();
  }
}

form.addEventListener("submit", (event) => {
  event.preventDefault();
  search();
});

init().catch((err) => setStatus(`Could not load categories: ${err.message}`, true));
