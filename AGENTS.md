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
distance, an estimated walking time and the public transport lines serving
each stop.

It is built on OpenStreetMap data and published as open source (MIT).

Use cases:

- **Vacation rentals**: "around the stay" section on a guest page
  (restaurants, cafés, beach, sights, public transport, parking).
- **Long-term rentals and properties for sale**: structured location
  information next to the listing's free-text "Lage" description
  (schools, kindergartens, doctors, supermarkets, public transport).

The service delivers **facts about the surroundings, not quality
judgements**. OpenStreetMap has no ratings. Curated tips ("our favourite
restaurant") and the free-text location description stay in the
consuming application.

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

What makes it different: **one `docker run`, prebuilt data for Germany,
Austria and Switzerland, curated categories, presets for "stay" and
"living", grouped results, walking minutes, lines at stops, distance to the
edge of beaches and parks, MIT, tiny resource footprint.**

---

# Scope of V1

In scope:

- Regions: **Germany, Austria and Switzerland** are built and published
  (the build script takes the Geofabrik region as a parameter, so others
  are possible).
- `GET /v1/surroundings`, `GET /v1/categories`, `GET /health`.
- Input as coordinates, or as an address geocoded via Photon.
- Weekly data build in GitHub Actions, published as a Docker image.

Not in scope (do not build):

- Ratings, reviews, photos.
- Real routing / isochrones (walking time is an estimate, see below).
- Auth, API keys, rate limiting, CORS.
- A publicly hosted instance.
- Overture / Foursquare data.
- Translated category labels (clients localize category ids).
- Countries other than Germany, Austria and Switzerland as published builds.
- Timetables, departures or line colours (only line refs from OSM).

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

- `scripts/build-data.sh <geofabrik-path> <out-dir>`, for example
  `europe/germany` or `europe/germany/bremen` for a small test region. CI
  and local builds both use this script.
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
- `meta` table: `data_version` (OSM extract date), `region`, `source`.

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
  outline     BLOB                 -- distanceToEdge areas only
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
returns no places. A db without the `lines` and `outline` columns (built by
1.0) is rejected at startup with a message to rebuild it.

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
- `distanceMeters` is to the nearest edge for `distanceToEdge` categories.
- Errors: `400` for invalid parameters, `404` when the address was not
  found, `502` when the geocoder fails. Body: `{ "error": "<message>" }`.

## `GET /v1/categories`

Returns all categories (id, maxDistanceMeters) and presets.

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
opl import --in places.geojsonseq --routes routes.osm.gz --out places.db --region germany --data-version 2026-10-26
opl filter-expressions            # osmium tags-filter expressions from categories.yaml
opl filter-expressions --routes   # expressions for route and stop_area relations
```

Configuration through flags with env fallbacks (`OPL_DATA`, `OPL_ADDR`,
`PHOTON_URL`). Nothing else.

`opl serve --demo` additionally serves a demo page at `/demo/` (address or
coordinates in, preset dropdown, results and raw JSON out). It is off by
default, embedded into the binary, plain HTML/CSS/JS without external
resources, and only talks to the API of the same server.
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
  `austria`, `switzerland`): binary + latest data
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

`data.yml` (weekly cron + `workflow_dispatch`), one matrix job per region
(germany, austria, switzerland):

1. Install osmium-tool (apt).
2. `scripts/build-data.sh europe/<region> out/`.
3. Smoke test against the new db: a query at a central station (Berlin
   Alexanderplatz 52.5219, 13.4132; Wien Hauptbahnhof 48.1851, 16.3777;
   Zürich HB 47.3779, 8.5403) must return a `train_station` with lines
   within 500 m. Also check the `places.db` size and the row count per
   category (thresholds per region), and fail on implausible values.
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
internal/server/           # HTTP handlers, query, ranking
internal/geocode/          # Photon client
internal/demo/             # opt-in demo page (serve --demo)
scripts/build-data.sh
docs/                      # README screenshots of the demo page
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
- Do not mix in Overture or Foursquare data in V1.

---

# README must cover

- What it does and does not do (surroundings, not reviews).
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
   The Germany, Austria and Switzerland builds have not run yet; check
   their size and duration after the first data workflow run.
