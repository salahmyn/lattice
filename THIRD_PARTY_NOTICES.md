# Third-party notices

Lattice is released under the [MIT License](LICENSE). The `lattice` binary
also carries the third-party code below, unmodified. Go module dependencies
are listed in `go.mod` and keep their own licences.

`lattice serve` serves every file listed here from the binary, licences
included (under `/vendor/`), so its pages make no network request.

## Mermaid 10.9.8

- What: `dist/mermaid.min.js` from the npm package `mermaid` 10.9.8, the
  diagram renderer. The flow (`/flows/<id>`) and journey (`/journeys/<id>`)
  pages draw their graphs with it.
- Where: `pkg/lattice/ui/assets/vendor/mermaid-10.9.8/mermaid.min.js.gz`
  (3,337,857 bytes, stored gzipped as 991,729; byte-for-byte the published
  file, SHA-384
  `N3QqR/7q+xm3BGX+CBbNI8AUmRRqcsDzToy+0z1NLDI0QmTKW8zvwLvqulJgk3dP`, which
  the pages check as the script's integrity). Served at
  `/vendor/mermaid-10.9.8/mermaid.min.js`, gzipped when the browser accepts
  it.
- Licence: MIT, Copyright (c) 2014 - 2022 Knut Sveidqvist. Full text in
  `pkg/lattice/ui/assets/vendor/mermaid-10.9.8/LICENSE`.
- Bundled dependencies: the file is a bundle that also contains Mermaid's
  dependencies, each under its own licence as its headers and package
  metadata state. Among them: d3 (ISC), dagre-d3-es, cytoscape, KaTeX,
  khroma, lodash-es, dayjs, stylis, js-yaml and @braintree/sanitize-url
  (MIT), DOMPurify (Apache License 2.0 or Mozilla Public License 2.0) and
  elkjs (Eclipse Public License 2.0).
- Source code: <https://github.com/mermaid-js/mermaid>.

## Tailwind CSS 3.4.19 (generated stylesheet)

- What: `pkg/lattice/ui/assets/static/tailwind.css`, the utility classes the
  page templates use, generated once by Tailwind CSS 3.4.19 from
  `scripts/tailwind/tailwind.config.js` (the command is in that file). It
  contains Tailwind's Preflight base styles, which build on modern-normalize
  (MIT). Nothing from Tailwind runs at build or run time.
- Licence: MIT, Copyright (c) Tailwind Labs, Inc. Full text in
  `pkg/lattice/ui/assets/vendor/tailwindcss-3.4.19/LICENSE`.
- Source code: <https://github.com/tailwindlabs/tailwindcss>.

## Fonts: Geist, JetBrains Mono, IBM Plex Serif

The web fonts of the UI's brand, as Google Fonts serves them (WOFF2, split by
Unicode range), unmodified:

| File (under `pkg/lattice/ui/assets/vendor/`) | Font | Google Fonts release | Range |
|---|---|---|---|
| `fonts/geist-v5-latin.woff2` | Geist, variable weight | v5 | Latin |
| `fonts/geist-v5-latin-ext.woff2` | Geist, variable weight | v5 | Latin Extended |
| `fonts/jetbrains-mono-v24-latin.woff2` | JetBrains Mono, variable weight | v24 | Latin |
| `fonts/jetbrains-mono-v24-latin-ext.woff2` | JetBrains Mono, variable weight | v24 | Latin Extended |
| `fonts/ibm-plex-serif-v20-400-latin.woff2` | IBM Plex Serif Regular | v20 | Latin |

- Licence: SIL Open Font License 1.1, each with its copyright notice:
  - Geist: Copyright 2024 The Geist Project Authors. Full text in
    `pkg/lattice/ui/assets/vendor/fonts/OFL-Geist.txt`.
  - JetBrains Mono: Copyright 2020 The JetBrains Mono Project Authors. Full
    text in `pkg/lattice/ui/assets/vendor/fonts/OFL-JetBrainsMono.txt`.
  - IBM Plex Serif: Copyright 2017 IBM Corp., with Reserved Font Name
    "Plex". Full text in
    `pkg/lattice/ui/assets/vendor/fonts/OFL-IBMPlexSerif.txt`.
- Source code: <https://github.com/vercel/geist-font>,
  <https://github.com/JetBrains/JetBrainsMono>,
  <https://github.com/IBM/plex>.

## elkjs 0.9.3

- What: `lib/elk.bundled.js` from the npm package `elkjs` 0.9.3, the Eclipse
  Layout Kernel compiled to JavaScript. The block map (`lattice serve` at
  `/map`, and `lattice export map`) lays its diagram out with it.
- Where: `pkg/lattice/ui/assets/map/third_party/elkjs/elk.bundled.js.gz`
  (stored gzipped, byte-for-byte the published file; SHA-384
  `KO35c6PFq7tFxAEx6FtATwL9vOBOmCzQYr7GM0iTr3oO22s7Jc9OkclOH4AV7jwK`). It is
  embedded in the binary and inlined into every exported map.
- Licence: Eclipse Public License 2.0, full text in
  `pkg/lattice/ui/assets/map/third_party/elkjs/LICENSE.md`.
- Copyright: Kiel University and others (the elkjs and Eclipse ELK
  contributors). The file also contains GWT runtime code, Copyright Google
  LLC, under the Apache License 2.0, as its own headers state.
- Source code: <https://github.com/kieler/elkjs> (the JavaScript build) and
  <https://github.com/eclipse/elk> (the layout algorithms).
