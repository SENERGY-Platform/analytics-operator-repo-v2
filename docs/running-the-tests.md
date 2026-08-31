# Running the tests

## Scope

Holds for the test suite of this repository: what runs without infrastructure,
what needs a MongoDB, and the two environment variables that decide whether a
missing database is a skip or a failure.

**Not this if** you are looking for how to test the controller layer of a platform
service without containers. That is possible for logic that sits above the
database, and it is not what the tests here do — see the delimitation in the next
section for why.

## Two halves

    go test -race ./...

**Unit tests** cover what needs no infrastructure: the mapping from error
sentinels to status codes, the redaction that keeps database internals out of a
response body, identity resolution, query parameter parsing, and configuration
loading.

**Integration tests** in `pkg/db` and `pkg/service` run against a real MongoDB.
They do so on purpose. What they check — the literal `search`, the sort whitelist,
the meaning of `limit=0`, index creation — only exists in the server. An in-memory
stand-in implements the interface, not the query semantics, so a test written
against one would pass while proving nothing about the behaviour the callers
depend on.

Permission decisions are the real ones: permissions-v2 ships an in-process client
that needs no server, so only the database is external. The tokens the tests mint
carry the `user` role and never `admin` — an admin token passes every check before
the resource is looked up, which would make every refusal case vacuous.

## The two variables

| Variable | Default | Effect |
|---|---|---|
| `MONGO_TEST_URL` | `localhost:27017` | Host and port of the database to test against |
| `REQUIRE_MONGO` | unset | Set to any value: an unreachable database is a **failure** instead of a skip |

Deliberately **not** `MONGO_URL`. That is the service's own variable, and a shell
that has it pointed at a deployment would otherwise aim the tests there — and the
tests create and drop databases.

Locally, an unreachable database skips:

    go test -race ./...

In a pipeline it must not, or the run goes green having never executed those
tests, which no summary distinguishes from having passed them:

    MONGO_TEST_URL=localhost:27017 REQUIRE_MONGO=1 go test -race ./...

The strict mode is the one the pipeline opts into, never the one a developer has
to remember.

## Isolation

Each test derives its own database name from the test name and drops it in
cleanup, so two runs against the same server — two people, or a pipeline
overlapping with a local run — cannot truncate each other's data mid-test.
Reachability is probed once per package rather than per test; probing per test
costs the server-selection timeout every time, which turns skipping a dozen tests
into a minute of waiting.

## Which server version

The pipeline runs the version the service is deployed against, pinned in
`.github/workflows/build.yml`. Testing against a different one tests a
configuration nobody runs. The driver states MongoDB 4.2 as the oldest it speaks
to, so that is the floor; the ceiling is whatever the deployment has reached.

Images from 6.0 on ship `mongosh` and no longer the legacy `mongo` shell. A health
check or verification command written against one of them fails with
`executable file not found` on the other, which reads like a broken database at
the worst possible moment.
