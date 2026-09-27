# Design prototype

The design canvas the dashboard was built from: `ocbench Dashboard.dc.html` and
the runtime that renders it. It is kept here so the dashboard can be compared
against the canvas side by side, rather than by reading markup.

Served at `http://127.0.0.1:8787/prototype/dashboard.dc.html` by `ocbench serve`.
The route is a development reference and is not linked from the dashboard.

## What it needs that the dashboard may not have

| Need | Why | Dashboard |
|---|---|---|
| React 18.3.1 + ReactDOM from unpkg | the runtime renders through React | no CDN at runtime |
| Babel standalone from unpkg | the runtime compiles the canvas's JSX | no JavaScript at all |
| `script-src 'unsafe-eval'` | the template runtime calls `new Function` | forbidden |
| `style-src 'unsafe-inline'` | the canvas is built from `style=` attributes | forbidden |
| Google Fonts | the canvas links six families | fonts are vendored |

`prototypeContentSecurityPolicy` in `internal/web/server.go` grants exactly those,
and applies only to `/prototype/`. The dashboard's own pages keep
`default-src 'none'`.

## Provenance

`support.js` is generated from `dc-runtime/src/*.ts` and is not edited here; its
header says to rebuild with `bun run build`. The only change made to
`dashboard.dc.html` is two script tags for React and ReactDOM, which must exist
before the runtime boots.
