# 0007. Tool output schemas are open, so a result can grow without breaking a client

Date: 2026-09-28
Status: accepted.

## Context

Issues #251 and #247. Every `machine_sync` call came back to Claude Code as
`Structured content does not match the tool's output schema: data must NOT have additional
properties`, although the copy succeeded. The daemon's own result matched the schema it serves
now: the go SDK validates every typed tool's output against its schema before it answers, and
`tools/list` on 127.0.0.1:7777 lists `mirror`, `strays` and `strayPaths`.

The client was checking against an older schema. MCP clients fetch `tools/list` once per
session and keep it. `/mcp` is stateless, so a daemon that restarts on a new build (update.sh,
install.sh, a swap for a test) never ends a client's session and never tells it to fetch the
list again. A Claude Code session started before #188 still held `machine_sync`'s schema with
`dest`, `summary` and `seconds` only. The SDK derives schemas with `additionalProperties: false`
on every object, so the two fields #188 added (`mirror`, `strays`) each failed that client's
check: the error names "additional properties" twice. Any field added to any tool's result, at
any depth (`toolchain`, `desktop`, `models` inside a machine), does the same to every client
that connected before the update.

## Decision

1. **Every declared output schema is open.** `mcpserver.addTool` wraps `mcp.AddTool`: for a
   typed result it derives the schema exactly as the SDK would (`jsonschema.ForType`), then
   drops `additionalProperties: false` from every object in it (properties, items, `$defs`,
   combinators). A property the schema does not list is then allowed, while every listed
   property keeps its type and `required` stays. Tools with no typed result (the desktop
   toolkit's, `any`) declare no output schema, as before.
2. **Adding a field to a result is compatible; removing, renaming or retyping one is not.** A
   client holding an older schema accepts a newer result that only adds. A change of the second
   kind breaks such clients until they reconnect, so it needs its own decision.
3. **Tests pin both halves.** `TestEveryToolsOutputMatchesItsSchema` calls every tool on a fake
   machine and validates its structured content against the schema `tools/list` declares, as a
   strict client does. `TestOutputSchemasAreOpen` walks every declared schema and fails on a
   closed object, and checks that a result with a field the schema lacks still validates (the
   #251 case: a client holding the schema from before the field).
4. Input schemas stay closed: an unknown argument is still an error (a misspelt `runId` must not
   pass silently), and it is the daemon, not a stale client, that checks them.

## Consequences

- A client that started before an update keeps working for every tool whose result only grew.
  It does not see a new field in its tool descriptions until it reconnects, but it gets the
  field in results.
- The SDK still validates each result against the open schema on the server, so a wrong type or
  a missing required field is still the daemon's error, not the client's.
- Registering a tool with `mcp.AddTool` directly would bring a closed schema back;
  `TestOutputSchemasAreOpen` fails on it.
