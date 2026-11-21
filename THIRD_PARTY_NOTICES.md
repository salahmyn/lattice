# Third-party notices

Lattice is released under the [MIT License](LICENSE). The `lattice` binary
also carries the third-party code below, unmodified. Go module dependencies
are listed in `go.mod` and keep their own licences.

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
