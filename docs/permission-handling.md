# How this service handles permissions

## Scope

Holds for this service's use of
[permissions-v2](https://github.com/SENERGY-Platform/permissions-v2): the topic it
registers, the resource it creates per operator, the reconciliation it runs on
startup, and the status codes a caller sees. Implemented in `pkg/db/repo.go` and
`pkg/api/api.go`.

**Not this if** the question is how permissions-v2 itself decides — role
short-circuits, group resolution, the meaning of the individual permission flags.
That is the behaviour of a shared dependency and is not described here, because a
copy of it in one consumer rots out of sync with the library.

`geltung: einzelfall`

## One resource per operator

Every operator is a resource in the topic `analytics-operators`, keyed by the
operator's MongoDB id. The owner receives read, write, execute and administrate
on creation.

Insert and permission are **two** steps, not one transaction. If the second fails
after the first succeeded, the operator exists with nobody holding rights on it.
The reconciliation below is what eventually notices, not the request.

## The reconciliation on startup deletes

`ValidateOperatorPermissions` runs once per start, before the HTTP server accepts
anything. It reads every operator, grants the owner what is missing, and then does
the opposite direction:

> **every permissions-v2 resource in this topic whose id it did not find in the
> database is deleted.**

That is the intended cleanup — an operator removed outside the API leaves an entry
behind, and nothing else would remove it. It is also the reason the service must
never be started against an incomplete database. Over an empty or partially
restored collection it removes the rights of every operator it cannot see, in a
system that this service does not back up and cannot restore. Restoring the
documents afterwards does not bring the permissions back.

Consequence for any data operation: count the documents **before** the service is
allowed to start, not after. The log line to watch for is

    <id> exists only in permissions-v2, now deleted

Its absence is the confirmation that nothing was removed.

The reconciliation reads the collection directly rather than through the list
query, so the cap documented in
[list-endpoint-parameters.md](list-endpoint-parameters.md) cannot apply to it. It
must not be routed through that query: with more operators than the cap it would
see only the first page and delete the rights of the rest.

## 403 where 404 would seem right

An id that does not exist and an id the caller may not see return the **same**
answer, `403` with `requested instance nonexistent or missing rights`.
permissions-v2 cannot tell the two apart, and answering differently would let a
caller enumerate ids that are not theirs.

`404` is reserved for the case that is genuinely distinguishable: the permission
check passed and the document is gone. That happens when an operator is deleted
between the check and the read, or after a data operation that removed documents
without removing permissions.

`400` is for input the caller can correct — an unparsable id, a bad query
parameter, a malformed body. The driver's own message never reaches the client;
the service replaces it, because that text can carry database internals.

## Acting on another user's behalf

`getUserId` prefers, in order:

1. `for_user`, but only if `X-User-Roles` contains `admin`
2. `X-UserId`
3. the `sub` claim of the `Authorization` token, parsed **without** verifying the
   signature

The third point is why this service must not be reachable directly. It trusts
whatever reaches it, so something in front of it has to be the thing that
establishes identity. Which component that is, and what it strips, is a property
of the installation and not of this code.

`X-User-Roles` is split on `", "` exactly. A client sending `user,admin` without
the space is **not** recognised as an admin. The tests pin this rather than fix
it, because the same split exists in the sibling services and changing it here
alone would make this service the only one behaving differently.
