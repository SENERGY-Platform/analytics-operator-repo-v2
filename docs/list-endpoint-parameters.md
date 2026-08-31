# Parameters of GET /operator

## Scope

Holds for the query parameters of `GET /operator` in this service, as implemented
in `pkg/db/repo.go` (`All`) and verified by the integration tests in
`pkg/db/integration_test.go`.

**Not this if** you are reading it for one of the sibling services in the
platform. Several of them carry visibly copied code for the same endpoint, but
the values differ — the cap, the sortable fields and the treatment of `limit=0`
are each a local decision, and none of them can be assumed from here.

## limit, and the value that means no limit

- **Absent** — at most `MaxLimit` documents, currently **1000**. The cap applies
  when the caller passes nothing at all, so an unparameterised request is not a
  full export.
- **`0`** — **every** document, with no cap. MongoDB reads `limit: 0` as
  unlimited, and the code passes the parsed value straight through.
- **Above the cap** — refused, not silently reduced:
  `invalid request: limit exceeds maximum of 1000`.
- **Negative or not a number** — refused:
  `invalid request: limit must be a non-negative integer`.

The zero case is a contract, not an accident. A caller that has to see every row
has no other way to ask for it, and clamping zero to the cap would make such a
caller silently miss everything past the first thousand — without an error,
because a short page is indistinguishable from a short collection.

`offset` is parsed by the same rule and rejects the same way, naming `offset`
instead of `limit`. An offset past the end returns an empty list, not an error.

## sort

`field` or `field:asc` or `field:desc`. A missing direction means ascending.

Only **`name`** is sortable. Any other field is **ignored**, not rejected — the
response then comes back in whatever order the storage engine has, which MongoDB
does not promise to keep stable. Two requests without a usable sort may return
the same documents in a different order.

## search

A **case-sensitive substring** match on `name`. The value is escaped before it
reaches the query, so it matches literally: `.*` finds nothing, and `a.pha`
matches only the name `a.pha` and not `alpha`.

This is deliberate and load-bearing. The value is interpolated into a `$regex`,
so without escaping a caller could run an arbitrary expression on the server —
including one crafted to be expensive. If a future change needs pattern search,
it needs a separate parameter, not a relaxed `search`.

## What the response counts

`operators` is the page. `totalCount` is the number of documents matching the
filter, ignoring `limit` and `offset` — so a caller can tell a short page from a
short collection. That distinction is what makes the cap survivable.
