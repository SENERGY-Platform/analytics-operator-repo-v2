# Operator versions

## Scope

Holds for the version list of an operator as written and read by this service since
v1.2.0: the endpoints that write and remove a version, how the top-level fields
follow the newest version, and which writes are refused. Implemented in
`lib/versions.go`, `pkg/db/repo.go`, `pkg/service/service.go` and
`pkg/api/handler.go`.

**Not this if** the question is which version a flow node uses or which flows and
pipelines use a version. Flows pin a version in the flow repository; usage is
answered there and by the pipeline registry, not here.

## Two different things called version

- `version` on the operator is the **revision counter** of the document. It is an
  integer and moves on every write. Clients compare it to detect a changed
  operator, so it keeps that meaning.
- `versions[].version` is the **label** of one release of the operator, written as
  `vMAJOR.MINOR.PATCH` (no pre-release or build suffix), like the image tags.

## Writing a version

`PUT /operator/{id}/versions/{version}` with a body of `image`, `inputs`,
`outputs`, `config_values` and `deprecated`. It needs write permission on the
operator.

- A new label is added (201), an existing label is replaced (200). Deprecating is
  the same call with `deprecated: true`.
- The label in the path wins. A different non-empty `version` in the body is a 400.
- `image` is required and may belong to only one version of the operator, because
  pipelines are matched to versions by image.
- Absent port lists are stored as `[]`.
- The list is stored sorted by number, so `v1.10.0` comes after `v1.9.0`.
- The first version turns an operator without versions into one with versions.

## Removing a version

`DELETE /operator/{id}/versions/{version}` needs write permission on the operator,
like the PUT. The checks run in this order; the first that fails answers:

1. `?force` that is not a boolean is a 400; `force=true` without the admin role a 403.
2. A malformed label is a 400.
3. Missing write permission is a 403, an operator that does not exist a 404.
4. A label the operator does not have is a 404.
5. The only version of an operator is not removed (400). Delete the operator
   instead.
6. Usage: the flow repository is asked for the flows that pin the label
   (`?version=`), the pipeline registry for the pipelines that run the version's
   image (`?image=`), both with the caller's `Authorization` header. If either
   cannot be asked, the answer is 502. If either counts more than zero, the answer
   is 409 with `{"error": "version is still used by flows or pipelines", "flows",
   "readable", "pipelines", "readable_pipelines"}`; the counts cover all users,
   the lists only what the caller may read.
7. The write. Like the other version writes it goes through only while the
   operator carries the revision that was read, otherwise 409 without a body.

Nothing is removed when any step refuses. Success is 204, and the top level moves to
the newest version left. `?force=true` skips the refusal in step 6, not the
question: an administrator's forced removal of a used version is logged at warn
with `operator_id`, `version`, `user_id`, `flows` and `pipelines`.

The usage is read before the write and not together with it: a flow or pipeline
created in between is not seen.

## The top level follows the newest version

The newest version is the highest one that is not deprecated, or the highest
overall when all are deprecated. A hotfix on an older major therefore does not
take over. After every version write and every update, the operator's `image`,
`inputs`, `outputs` and `config_values` equal those of the newest version.

## Updating an operator that has versions

`POST /operator/{id}` keeps working for metadata (name, description, deployment
type, cost, pub).

- `versions` in the body is ignored, on create and on update. Clients that send
  the whole GET result back therefore do not change the list.
- `image`, `inputs`, `outputs` and `config_values` in the body must be absent or
  equal to the newest version, otherwise 400. An explicit `[]` counts as a value,
  so emptying the ports is refused instead of being dropped silently.
- An operator without versions behaves exactly as before: the body's values are
  written.

## Concurrent writes

Updates and version writes read the stored document first.

- An operator **with** versions is written only while it still carries the
  revision that was read; otherwise the answer is 409 and the request can be
  repeated.
- An operator **without** versions keeps last-write-wins, unless a version was
  added since the read, which is a 409 as well.
- If the operator was deleted between the read and the write, the answer is 404.
