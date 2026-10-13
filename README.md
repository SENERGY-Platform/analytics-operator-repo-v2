# analytics-operator-repo-v2

Stores the analytics operators of the platform: their metadata, their inputs and
outputs, and who may see and change them. Authorisation is not kept here — every
operator is a resource in
[permissions-v2](https://github.com/SENERGY-Platform/permissions-v2), and this
service asks it on every request.

## Configuration

Every value has a default in the code and is overridden by an environment
variable. A JSON file with the same keys can be passed with `-config <path>`; the
environment wins over the file.

| Variable | Default | Meaning |
|---|---|---|
| `SERVER_PORT` | `8000` | Port the HTTP server listens on |
| `MONGO_URL` | `mongodb://localhost:27017` | Connection string including the scheme, passed to the driver unchanged |
| `MONGO_USER` | empty | User to authenticate as. Empty means no authentication |
| `MONGO_PASSWORD` | empty | Password of `MONGO_USER`. Masked in the logged config |
| `MONGO_AUTH_SOURCE` | `admin` | Database `MONGO_USER` is defined in |
| `MONGO_DATABASE` | `analytics_operator_repo` | Database the `operators` collection lives in |
| `PERMISSIONS_V2_URL` | `http://permv2.permissions:8080` | Address of permissions-v2. The literal `mock` selects the in-process implementation instead, which needs no server |
| `FLOW_REPO_URL` | `http://api.analytics-flow-repo:5000` | Address of the flow repository, asked which flows use an operator before it is deleted. The in-cluster service; no path prefix. Empty is refused at startup |
| `PIPELINE_REGISTRY_URL` | `http://api.analytics-pipeline-service:8000` | Address of the pipeline registry, asked which pipelines run an operator or a version before it is deleted. The in-cluster service; no path prefix. Empty is refused at startup |
| `HTTP_TIMEOUT` | `30s` | Read and write timeout of the server, as a duration |
| `URL_PREFIX` | empty | Prefix every route is mounted under, for running behind a gateway that does not strip it |
| `LOGGER_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `DEBUG` | `false` | Verbose startup output |

Keep credentials out of `MONGO_URL`. The URL is logged as it is at startup, and a
set `MONGO_USER` replaces whatever credentials the URL carries.

Startup refuses an empty `MONGO_DATABASE` and a `MONGO_USER` without
`MONGO_PASSWORD`. After connecting it pings the server, which fails on a wrong
password or auth source, and then lists the collections of `MONGO_DATABASE`. That
access check is what makes missing credentials fail at startup: MongoDB answers a
ping without authentication. It fails with `mongo startup check failed: ...`
before the indexes are created; index creation would fail too, but later and
with a less telling message.

An empty `PERMISSIONS_V2_URL` is refused at startup rather than accepted: a
client built for an empty address fails on every request instead, which is a much
later place to notice a missing value.

## Deleting an operator that flows or pipelines use

`DELETE /operator/{id}` and `DELETE /operator` first check the caller's
Administrate permission on every id; without it the answer is 403 and nothing is
asked, because the usage counts cover all users. They then ask, for every id and with
the caller's `Authorization` header, the flow repository
(`GET /operators/{id}/usage`) which flows use the operator and the pipeline
registry (`GET /pipeline/statistics/operatorusage/{id}`) which pipelines run it.
Both count those of all users and list only what the caller may read.

- Used by a flow or a pipeline: 409 with `{"error", "flows", "readable":
  [{"id", "name"}], "pipelines", "readable_pipelines": [{"id", "name"}]}`. The
  batch delete is all or nothing and answers `{"error", "operators": [{"id",
  "flows", "readable", "pipelines", "readable_pipelines"}]}`, naming each used
  operator; nothing is deleted.
- Either service cannot be asked (network error, any status but 200, an answer
  without a count or with a negative one): 502 naming the service, nothing is
  deleted.
- `?force=true` deletes anyway. It is for administrators only (role `admin` in
  `X-User-Roles`, or in the token when the header is absent); anyone else gets
  403. The usage is still read, so a forced delete is refused with 502 when either
  service is down. A forced delete of a used operator is logged at warn with
  `operator_id`, `user_id`, `flows` and `pipelines`.

The check and the delete are not atomic: a flow or pipeline saved between them is
not seen. Removing a single version follows the same rules, narrowed to the
version; see [docs/operator-versions.md](docs/operator-versions.md).

`PERMISSIONS_V2_URL=mock go run .` mocks neither the flow repository nor the
pipeline registry; deletes need a reachable `FLOW_REPO_URL` and
`PIPELINE_REGISTRY_URL` there as well.

## Running it

The service needs a MongoDB and either a permissions-v2 or the in-process
substitute:

    docker run -d --rm -p 27017:27017 mongo:8.2
    PERMISSIONS_V2_URL=mock go run .

MongoDB 4.2 is the oldest server the driver speaks to. Indexes and the collection
are created on startup, so an empty database is enough.

## Tests

    go test -race ./...

Without a reachable database the integration tests skip themselves, which is the
wanted behaviour on a workstation and a blind spot in a pipeline. Both halves,
and the two environment variables that control them, are described in
[docs/running-the-tests.md](docs/running-the-tests.md).

## Quality gates

`.claude/gates.env` holds one mode per check, with the reasoning next to each. The
same checks run as the `checks` job in `.github/workflows/build.yml`, which the
release job waits on — a failing gate stops the tag and the image, not just the
review.

## API documentation

The service serves its own OpenAPI document at `/doc`. It is generated from the
handler comments in `pkg/api` and committed under `docs/`, so it has to be
regenerated whenever a route, a parameter or a response changes:

    go install github.com/swaggo/swag/cmd/swag@v1.16.6
    swag init -g api.go -o docs -dir pkg/api --parseDependency --ot json

The version matters: a different one reformats the whole file, which buries the
actual change. `swag` self-reports `v1.16.4` even when installed from the `v1.16.6`
tag. A run that changes nothing reproduces the committed file byte for byte, so a
diff after regenerating is a real signal.

## Documentation in this repository

`docs/` holds the generated `swagger.json` next to hand-written notes on the
behaviour that is not obvious from the code:

- [docs/list-endpoint-parameters.md](docs/list-endpoint-parameters.md) — what
  `sort`, `search`, `limit` and `offset` do, including the one value of `limit`
  that means the opposite of a limit
- [docs/permission-handling.md](docs/permission-handling.md) — how operators map
  onto permissions-v2 resources, what the reconciliation on startup deletes, and
  why a caller gets 403 where 404 would seem right
- [docs/running-the-tests.md](docs/running-the-tests.md) — the database the
  integration tests need, and how a pipeline stops them from skipping silently

## Deployment

Not part of this repository. The published image is
`ghcr.io/senergy-platform/analytics-operator-repo-v2`, tagged by
`.github/workflows/build.yml` on every push to `main`.
