# Block map demo: a fictional bookshop

A small block-graph workspace for trying the block map (`/map` in
`lattice serve`, or `lattice export map`). Everything in it is invented.

- Six systems, each read from a directory of `catalogue/v1` files under
  `sources/`: storefront (web), checkout, payments, catalogue,
  notifications and accounts.
- Judgments in `lattice/judgments/`: the systems and two actors with their
  owners, three externals (a card payment gateway, an email delivery service
  and a parcel carrier), a group for the feature blocks, and the edges the
  catalogues cannot show (calls, events, webhooks, the store settings).
- Three flows over the storefront (`sources/flows/storefront.json`) and
  snapshot stamps for three sources.
- Two feature manifests, so feature blocks and their owners show.
- A proposal overlay: the `pay-sheet` initiative and its evidence in
  `lattice/records/`.

From the repository root:

```sh
go build -o dist/lattice ./cmd/lattice
dist/lattice --repo examples/block-map refresh
dist/lattice --repo examples/block-map export map /tmp/bookshop-map.html
```

The refresh writes `lattice/lattice.json`, which is not committed. Open the
exported file in a browser, or run `dist/lattice --repo examples/block-map
serve` and go to `/map`. `docs/user-guide.md` (section 4h) describes the
map's views.
