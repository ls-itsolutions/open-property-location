# AGENTS.md

# OpenPropertyLocation

This file describes the project and contains binding decisions.
If anything contradicts this file, this file wins.

---

# What it is

OpenPropertyLocation is a small, self-hostable HTTP service:

**coordinates or an address in → structured surroundings out.**

For a property it returns the nearest relevant places grouped by category
(supermarket, bakery, bus stop, train station, school, beach, …) with
distance, an estimated walking time, the public transport lines serving
each stop and, where an open GTFS timetable exists, the departures per hour.
The same lookups are available to AI assistants as an MCP server.

It is built on OpenStreetMap data and published as open source (MIT).

Use cases:

- **Vacation rentals**: "around the stay" section on a guest page
  (restaurants, cafés, beach, sights, public transport, parking).
- **Long-term rentals and properties for sale**: structured location
  information next to the listing's free-text "Lage" description
  (schools, kindergartens, doctors, supermarkets, public transport).

Ratings and reviews are welcome if a free, openly licensed source with
useful coverage exists. As of November 2026 there is none: OpenStreetMap
has no ratings, and the open review project Mangrove has only a few dozen
reviews per month worldwide. Curated tips ("our favourite restaurant") and
the free-text location description stay in the consuming application.

The service must not contain anything specific to one consumer: no
tenants, no auth, no customer logic.

---

# Why this project exists

Research (October 2026) found building blocks, but no ready product:

| Existing option | Why it does not fit |
|---|---|
| Overpass API (public) | Rate-limited, not meant for production, raw data without categories or ranking |
| openpoiservice (GIScience, Apache-2.0) | Python 3.5-era stack with PostGIS, no ranking, no address input, maintenance unclear |
| gbd-geoservices (AGPL) | Full map server (Mapnik, WMS, tiles) with PostGIS, ~6 h import for Germany, no presets or grouping, very young project |
| oscp-poi-service | Queries Overpass/Google live, fixed 200 m radius |
| Geoapify, Apify actors, Open Places API | Hosted and/or paid, not self-hosted |
| Overture Maps / Foursquare OS Places | Data files only, no service; mixing with OSM creates ODbL obligations |

Real-estate portals combine human free text ("Lage") with automatic,
structured data (Rightmove: schools and stations; Zillow/Redfin: nearby
schools, Walk Score; ImmoScout24 AT: "Infrastruktur / Entfernungen" with
distance bands). OpenPropertyLocation is that structured part.

What makes it different: **one `docker run`, prebuilt data for eight
countries, curated categories, presets for "stay" and "living", grouped
results, walking minutes, lines and departures per hour at stops, distance
to the edge of beaches and parks, an MCP server for AI assistants, MIT,
tiny resource footprint.**

Other OpenStreetMap MCP servers (November 2026) query Overpass and
Nominatim live and have no categories, ranking or timetables.

---

# Scope of V1

In scope:

- Regions: **Germany, Austria, Switzerland, the Netherlands, Denmark,
  Italy, Spain and Croatia** are built and published (the build script
  takes the Geofabrik region as a parameter, so others are possible).
- `GET /v1/surroundings`, `GET /v1/categories`, `GET /health`.
- MCP server with the tools `get_surroundings` and `list_categories`, over
  stdio (`opl mcp`) and Streamable HTTP (`POST /mcp`).
- Departures per hour at stops from open GTFS feeds: Germany (gtfs.de),
  Switzerland (opentransportdata.swiss), Netherlands (OVapi). Austria and
  Denmark need a registration, Italy, Spain and Croatia have no national
  feed.
- Input as coordinates, or as an address geocoded via Photon.
- Weekly data build in GitHub Actions, published as a Docker image.

Not in scope (do not build):

- Ratings and reviews without a free source (see above), photos.
- Real routing / isochrones (walking time is an estimate, see below).
- Auth, API keys, rate limiting, CORS.
- A publicly hosted instance.
- Overture / Foursquare data.
- Translated category labels (clients localize category ids).
- Published builds of countries other than the eight above.
- Timetables, live departures or line colours (only line refs from OSM and
  departures per hour from GTFS).
- MCP resources, prompts, sessions or server-sent events (tools only).

---

# Stack

- **Go** (current stable), standard library `net/http` with its built-in
  routing.
- **SQLite** with the **R\*Tree** module as the only data store: one file,
  `places.db`.
- SQLite driver: `modernc.org/sqlite` (pure Go, so `CGO_ENABLED=0` and a
  static binary work). **Verify R\*Tree support in the first step.** If it
  is missing, use `github.com/mattn/go-sqlite3` with the `sqlite_rtree`
  build tag and stop to report the change.
- YAML parsing for `categories.yaml` (one small, well-known library).
- **osmium-tool** for preprocessing OSM data. It is used only in the data
  build, never at runtime.

No other dependencies without a concrete reason.

No PostGIS, no Postgres, no ORM, no web framework.

---

# Data pipeline

```
Geofabrik germany-latest.osm.pbf (~4.5 GB)
  │ osmium tags-filter        keep only tags used in categories.yaml
  │ osmium export -f geojsonseq
  │                           nodes → points, ways/multipolygons → areas;
  │                           osmium assembles the geometries
  ▼
places.geojsonseq             routes.osm.gz
  │                             ▲ osmium tags-filter --omit-referenced
  │                             │ (route and stop_area relations only,
  │                             │ from the same .osm.pbf)
  │ opl import ◀────────────────┘
  │                           stream line by line, map to category,
  │                           compute representative point, importance,
  │                           outline and lines
  ▼
places.db (SQLite + R*Tree + meta)
```

Optionally `opl import --gtfs` also reads a GTFS zip and stores the
departures per hour at stops (see Departures).

- `scripts/build-data.sh <geofabrik-path> <out-dir> [<gtfs-url>
  <gtfs-attribution>]`, for example `europe/germany` or
  `europe/germany/bremen` for a small test region. CI and local builds both
  use this script.
- The osmium filter expressions are **generated from `categories.yaml`**
  (`opl filter-expressions`), so the category definitions live in exactly
  one place. The route filter (`opl filter-expressions --routes`) comes from
  the transit modes in `internal/transit`.
- Representative point: points as-is, areas as the centroid of the geometry.
- Categories with `distanceToEdge` (beach, swimming, park) store the outline
  of areas (all rings, compact varint encoding, `internal/outline`), and
  their R\*Tree entry is the bounding box. The server measures the
  distance to the nearest edge, 0 inside. Other areas keep the centroid:
  storing every building outline is not worth the size.
- Lines: a stop is served by the route relations (route=train, light_rail,
  subway, tram, trolleybus, bus, ferry; with a `ref`) it is a member of as a
  node or with a platform/stop role. An element that is not a member of any
  route gets the lines of all stop areas it belongs to (stations usually are
  not route members). Stop areas do not chain.
- Drop elements with `access=private` or `access=no`.
- If an element matches several categories, the first matching category in
  `categories.yaml` order wins.
- `meta` table: `data_version` (OSM extract date), `region`, `source`,
  `attribution` (returned by the API, includes the GTFS credit) and
  `departures_date` (the day departures were counted on, with GTFS only).

## Departures

- Count, per GTFS stop and mode, the departures on a Tuesday between
  06:00 and 20:00 and divide by 14: the average per hour. The Tuesday is
  the one with the most active services within 35 days after the OSM
  extract date (or the feed start, if later), so holidays are avoided.
- A departure is a stop time with pickup, except the last stop of a trip.
  `frequencies.txt` trips count every start of the frequency. Stop times
  without a time are not counted.
- GTFS route types (basic and extended) map to the OSM route modes;
  others (cable cars, taxis, …) are ignored.
- A place of a category with `departures` gets the sum over all GTFS stops
  within `withinMeters` of the given modes, rounded to one decimal, or
  NULL when none is found. Collapsed places keep the highest value.
- The GTFS feed and the credit it requires are build parameters. Only
  openly licensed feeds without registration are used for published builds.

The build must not run on the server that serves the API. It runs in CI or
on a developer machine.

---

# Database schema

```sql
CREATE TABLE places (
  id          INTEGER PRIMARY KEY,
  osm_type    TEXT NOT NULL,       -- node | way | relation
  osm_id      INTEGER NOT NULL,
  category    TEXT NOT NULL,       -- category id from categories.yaml
  name        TEXT,
  lat         REAL NOT NULL,
  lon         REAL NOT NULL,
  importance  REAL NOT NULL,       -- 0..1
  tags        TEXT NOT NULL,       -- JSON, only the tags the API returns
  lines       TEXT,                -- JSON [{"mode","ref"}], sorted
  outline     BLOB,                -- distanceToEdge areas only
  departures  REAL                 -- per hour, categories with departures only
);

-- The category is a third dimension (min_cat = max_cat = categories.key),
-- so a query for one category does not visit all other places in the box.
CREATE VIRTUAL TABLE places_rtree USING rtree(
  id, min_lat, max_lat, min_lon, max_lon, min_cat, max_cat
);

CREATE TABLE categories (key INTEGER PRIMARY KEY, id TEXT NOT NULL UNIQUE);

CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
```

The server reads the `categories` table at startup. A category that is in
`categories.yaml` but not in the db (data built before it was added)
returns no places. A db without the `lines`, `outline` and `departures`
columns or the `attribution` meta entry (built by an older version) is
rejected at startup with a message to rebuild it.

The importer creates the db from scratch every time (no incremental
updates). It writes into a temp file and renames it at the end.

---

# Categories

`categories.yaml` is the heart of the project. It is embedded into the
binary (`go:embed`) and used by both `import` and `serve`.

Each category has:

- `id` (English, snake_case, stable API contract)
- `match`: list of OSM tag matchers (`key=value`, values may be a list)
- `requireName`: drop unnamed elements (true for restaurants and similar,
  false for parking, playgrounds and charging stations)
- `maxDistanceMeters`: search radius for this category
- `collapseSameName` (optional): return only the best ranked of several
  places with the same name. OSM often maps one stop per direction or line,
  so this is set for `bus_tram_stop`, `train_station` and `ferry_terminal`.
  The lines of the collapsed places are merged.
- `distanceToEdge` (optional): measure the distance to the nearest edge of
  areas instead of the centroid. Set for `beach`, `swimming` and `park`.
- `departures` (optional): `modes` and `withinMeters` for the departures
  per hour. Set for `bus_tram_stop` (bus, trolleybus, tram; 50 m),
  `train_station` (train, light_rail, subway; 300 m) and `ferry_terminal`
  (ferry; 200 m).

Categories (file order, which decides when an element matches several):

| id | OSM tags | requireName | maxDistanceMeters |
|---|---|---|---|
| restaurant | amenity=restaurant | yes | 2000 |
| cafe | amenity=cafe | yes | 2000 |
| bar | amenity=bar, pub, biergarten | yes | 2000 |
| ice_cream | amenity=ice_cream | yes | 2000 |
| bakery | shop=bakery | yes | 2000 |
| supermarket | shop=supermarket | yes | 3000 |
| pharmacy | amenity=pharmacy | yes | 3000 |
| post_office | amenity=post_office | no | 3000 |
| atm | amenity=atm | no | 2000 |
| hospital | amenity=hospital | yes | 15000 |
| paediatrician | healthcare:speciality=paediatrics | yes | 5000 |
| doctor | amenity=doctors, clinic | yes | 3000 |
| kindergarten | amenity=kindergarten, childcare | yes | 3000 |
| school | amenity=school | yes | 5000 |
| bus_tram_stop | highway=bus_stop, railway=tram_stop | yes | 1000 |
| train_station | railway=station, halt | yes | 10000 |
| ferry_terminal | amenity=ferry_terminal | yes | 10000 |
| attraction | tourism=attraction, museum, viewpoint; historic=castle, monument | yes | 10000 |
| beach | natural=beach | no | 15000 |
| swimming | leisure=swimming_area, bathing_place, water_park | no | 10000 |
| park | leisure=park | no | 2000 |
| playground | leisure=playground | no | 1000 |
| fitness | leisure=fitness_centre | yes | 3000 |
| parking | amenity=parking | no | 1000 |
| ev_charging | amenity=charging_station | no | 3000 |
| bicycle_rental | amenity=bicycle_rental | no | 2000 |

Presets (the order defines the order of groups in the response):

- `stay`: restaurant, cafe, bar, ice_cream, bakery, supermarket, pharmacy,
  atm, bus_tram_stop, train_station, ferry_terminal, attraction, beach,
  swimming, park, parking, ev_charging, bicycle_rental
- `living`: supermarket, bakery, pharmacy, post_office, atm, doctor,
  paediatrician, hospital, kindergarten, school, bus_tram_stop,
  train_station, park, playground, fitness

Tuning these lists is expected. Keep the file readable and commented.

---

# Ranking

Keep it simple and deterministic:

- `importance` (0..1) is computed at import time:
  `wikidata` or `wikipedia` tag +0.5, `website` +0.2,
  `opening_hours` +0.1, `tourism=attraction` or `historic=*` +0.2,
  capped at 1.
- Within a category, sort by `distanceMeters / (1 + importance)`.
  The response still shows the real distance.
- Categories without such tags (bus stops, parking) effectively sort by
  distance.

Distance: haversine. Walking time:
`ceil(distanceMeters × 1.3 / 80)` minutes (1.3 detour factor, 80 m/min ≈
4.8 km/h). This is an estimate. Document it in the README and in the API
field description.

---

# API

## `GET /v1/surroundings`

Query parameters:

| Param | Required | Description |
|---|---|---|
| `lat`, `lon` | one of the two inputs | WGS84 coordinates |
| `address` | one of the two inputs | free text, geocoded via Photon |
| `preset` | no | `stay` or `living` |
| `categories` | no | comma-separated category ids |
| `limit` | no | places per category, default 5, max 20 |

Exactly one of `lat`/`lon` or `address` must be given, and either `preset`
or `categories` (not both).

Response `200`:

```json
{
  "location": { "lat": 54.1776, "lon": 12.0838 },
  "categories": [
    {
      "id": "bakery",
      "places": [
        {
          "id": "node/123456",
          "name": "Bäckerei Beispiel",
          "lat": 54.1781,
          "lon": 12.0851,
          "distanceMeters": 120,
          "walkingMinutes": 2,
          "address": {
            "street": "Strandstraße",
            "houseNumber": "1",
            "postcode": "18119",
            "city": "Rostock"
          },
          "openingHours": "Mo-Sa 06:00-18:00",
          "website": "https://example.org",
          "wikidata": null,
          "lines": null,
          "departuresPerHour": null,
          "osmUrl": "https://www.openstreetmap.org/node/123456"
        }
      ]
    }
  ],
  "dataVersion": "2026-10-26",
  "attribution": "© OpenStreetMap contributors, ODbL"
}
```

- Categories without results are returned with an empty `places` array (so
  clients can rely on the preset order).
- Optional fields are `null` when the tag is missing.
- `lines`: `[{"mode": "bus", "ref": "23"}, …]` or `null`.
- `departuresPerHour`: number with one decimal or `null`.
- `attribution` comes from the `meta` table: the OSM credit, plus
  `; timetables: <credit>` with GTFS.
- `distanceMeters` is to the nearest edge for `distanceToEdge` categories.
- Errors: `400` for invalid parameters, `404` when the address was not
  found, `502` when the geocoder fails. Body: `{ "error": "<message>" }`.

## `GET /v1/categories`

Returns all categories (id, maxDistanceMeters) and presets.

## MCP

- JSON-RPC 2.0, tools only: `get_surroundings` (the query parameters of
  `/v1/surroundings` as arguments, `categories` as an array; returns the
  same JSON as text content and `structuredContent`) and
  `list_categories`. Invalid arguments are tool errors (`isError`), so the
  model sees the message.
- Protocol versions 2025-11-25, 2025-06-18, 2025-03-26 and 2024-11-05; the
  server answers with the client's version if supported, else the newest.
- stdio: `opl mcp`, newline-delimited messages, logs on stderr.
- HTTP: `POST /mcp` returns one JSON response per request and `202` for
  notifications; `GET /mcp` is `405`. Requests with an `Origin` header
  whose host differs from `Host` get `403` (DNS rebinding).
- Hand-written on `net/http` and `encoding/json`: the subset is small, so
  no SDK dependency.

## `GET /health`

`200` when `places.db` is open and readable. Used for the Docker
healthcheck.

## Geocoding

- `PHOTON_URL` env, default `https://photon.komoot.io`.
- Call `/api?q=<address>&limit=1` and use the first feature.
- The public Photon instance is fair-use. Document that heavy users should
  self-host Photon.

---

# CLI

One binary, `opl`:

```
opl serve  --data /data/places.db --addr :8080
opl mcp    --data /data/places.db  # MCP on stdin/stdout
opl import --in places.geojsonseq --routes routes.osm.gz [--gtfs gtfs.zip --gtfs-attribution "<credit>"] --out places.db --region germany --data-version 2026-10-26
opl filter-expressions            # osmium tags-filter expressions from categories.yaml
opl filter-expressions --routes   # expressions for route and stop_area relations
```

Configuration through flags with env fallbacks (`OPL_DATA`, `OPL_ADDR`,
`PHOTON_URL`). Nothing else.

`opl serve --demo` additionally serves a demo page at `/demo/`: an address
and a preset in, the nearest three places per category with their lines
out. Keep it minimal. It is off by default, embedded into the binary, plain
HTML/CSS/JS without external resources, and only talks to the API of the
same server. The README shows it as an animated GIF (`docs/`, light and
dark), recorded from the real page with Germany data.
`opl healthcheck` exists for the Docker healthcheck (no curl in
distroless).

---

# Docker

One `Dockerfile` with two targets:

- `app`: static binary on a distroless/static base, data mounted at
  `/data/places.db`.
- `with-data`: `FROM app` + `COPY places.db /data/places.db` +
  `LICENSE-DATA` (ODbL notice). Built once per region.

Images (GHCR, public):

- `ghcr.io/ls-itsolutions/open-property-location:<version>`: binary only (from tags)
- `ghcr.io/ls-itsolutions/open-property-location:<region>` (`germany`,
  `austria`, `switzerland`, `netherlands`, `denmark`, `italy`, `spain`,
  `croatia`): binary + latest data
- `ghcr.io/ls-itsolutions/open-property-location:<region>-YYYY-MM-DD`: pinned
  data version

Usage for others:

```
docker run -p 8080:8080 ghcr.io/ls-itsolutions/open-property-location:germany
```

Target runtime footprint: **≤ 128 MB RAM**, so it runs with
`mem_limit: 128m`.

---

# CI (GitHub Actions)

`ci.yml` (push / PR):

- `go vet`, `go test ./...`
- Build the binary
- Pipeline test with a small region (`europe/germany/bremen`): build-data
  script → import → start server → smoke test query (the train station
  must have lines)

`data.yml` (weekly cron + `workflow_dispatch`), one matrix job per region,
with the GTFS feed and its credit where the region has one:

1. Install osmium-tool (apt).
2. `scripts/build-data.sh europe/<region> out/`.
3. Smoke test against the new db: a query at a central station must
   return a `train_station` with lines within 500 m, and with departures
   for regions with GTFS. Also check the `places.db` size and the row count
   per category (thresholds per region), and fail on implausible values.
4. Build and push the `<region>` image only if step 3 passes.
5. Upload `places.db` to a rolling GitHub release `data-<region>`
   (replace the asset).

Public repos get free Actions minutes. The Germany build fits the
standard runner (disk and RAM). If it does not, stop and report rather
than switching to paid runners.

---

# Repository layout

```
cmd/opl/main.go            # subcommands: serve, import, filter-expressions
internal/categories/       # categories.yaml (embedded), parsing, matching
internal/importer/         # geojsonseq → places.db
internal/outline/          # compact storage of area outlines
internal/transit/          # lines of stops from route and stop_area relations
internal/gtfs/             # departures per hour of stops from a GTFS feed
internal/server/           # HTTP handlers, MCP (mcp.go), query, ranking
internal/geocode/          # Photon client
internal/demo/             # opt-in demo page (serve --demo)
scripts/build-data.sh
docs/                      # README GIFs of the demo page
Dockerfile
openapi.yaml               # API reference; TestOpenAPI keeps it in sync
.github/workflows/ci.yml   # also merges Dependabot patch/minor updates
.github/workflows/data.yml # opens an issue while the weekly build fails
.github/workflows/release.yml # monthly draft release + reminder issue
.github/dependabot.yml
LICENSE                    # MIT (code)
LICENSE-DATA               # ODbL notice for data in images and releases
README.md
AGENTS.md                  # this file
```

Split packages only where it helps readability. Fewer is fine.

---

# Licensing

- Code: **MIT**.
- Data: OpenStreetMap, **ODbL 1.0**. `places.db` is a derivative database,
  so it is distributed under ODbL with attribution
  ("© OpenStreetMap contributors"). The `germany` image and the release
  asset include `LICENSE-DATA`.
- Every API response contains `attribution`. The README states that
  clients must display it.
- Timetables: each GTFS feed under its own license (gtfs.de CC BY 4.0,
  opentransportdata.swiss terms of use, OVapi CC0). The credit is part of
  `attribution` and listed in `LICENSE-DATA`.
- Do not mix in Overture or Foursquare data in V1.

---

# README must cover

- What it does and does not do (no reviews without a free source).
- MCP setup for Claude Code, Claude Desktop and other clients, near the top.
- Quick start with `docker run`.
- API reference with an example.
- Categories and presets, and how to customize them (fork, edit
  `categories.yaml`, rebuild).
- Building data for another region with `scripts/build-data.sh`.
- Walking time is an estimate.
- Licensing and attribution duties.
- Photon fair use.

---

# Expected usage

Consumers run the `germany` image next to their backend, on an internal
network without a public port. The backend calls it with stored
coordinates, once per property, and caches the result. End users never
trigger requests directly.

This means the API must stay stable, be fast for single calls, and work
without address input.

---

# Code style

- English everywhere.
- Simple, direct Go. No interfaces without a second implementation, no
  frameworks, no dependency injection containers.
- Small functions, clear names, comments only where something is not
  obvious.
- Tests: categories matching, importance, ranking, haversine/walking time,
  importer with a small geojsonseq fixture, handlers against a small fixture
  db.
- No magic numbers in code: radii live in `categories.yaml`, the walking
  constants are named constants.

---

# Simplicity first

Implement the smallest solution that satisfies this document.
Do not add features from the "not in scope" list.
If something here turns out to be wrong or impossible (for example R\*Tree
support, CI runner limits, a much larger `places.db` than expected), stop
and report instead of silently working around it.

---

# Repository

- GitHub organization: `ls-itsolutions` (the owner's IT company,
  ls-itsolutions.de).
- Repository: `ls-itsolutions/open-property-location`.
- Go module path: `github.com/ls-itsolutions/open-property-location`.

---

# Verified

1. **`places.db` size** for Germany: 286 MB with 1.69 million places
   (first build, October 2026). The full build takes about 7 minutes on
   the standard runner. Revisit baking the data into the image if it grows
   far beyond that.
2. **R\*Tree in `modernc.org/sqlite`**: supported, no cgo needed.
3. **Lines and outlines** (October 2026, Bremen and Mecklenburg-Vorpommern):
   the new categories, outlines and lines make `places.db` about 20 %
   larger (Bremen 1.79 → 2.15 MB), so Germany should land around 350 MB.
   `routes.osm.gz` is about 5 % of the `.osm.pbf`. Lines are found for 96 %
   of the bus and tram stops in Bremen and 48 % in Mecklenburg-Vorpommern.
   First data workflow runs (November 2026): Germany 324 MB in 11 minutes,
   Austria 43 MB in 13 minutes, Switzerland 34 MB in 5 minutes.
4. **Browser-only variant** (idea: query `places.db` from a static host in
   the browser, no server; checked November 2026, not built): GitHub
   release assets send no CORS headers, sql.js-httpvfs (last release 2022)
   has no R\*Tree, sqlite-wasm-http (2023) is experimental, GitHub Pages
   sites are limited to 1 GB. It would need its own static format, a
   JavaScript port of the ranking and a host; decide before building it.
5. **Departures and new regions** (November 2026, local builds in the CI
   image): share of bus and tram stops / train stations with departures:
   Bremen 95 % / 81 %, Switzerland 92 % / 87 %, Netherlands 90 % / 84 %.
   Counting takes about 30 s for the German feed (2.1 GB stop_times, 0.7
   GB RAM) and the Swiss feed (3.7 GB, 2.4 GB RAM). Sizes and build times:
   Switzerland 35 MB / 4 min, Netherlands 66 MB / 6 min, Denmark 18 MB /
   2 min, Croatia 10 MB / 1 min, Italy 129 MB / 9 min, Spain 91 MB / 9 min.
   All smoke-test stations have lines. Paediatricians are rarely tagged
   outside Germany, Austria and Switzerland (Netherlands 5, Croatia 9).
