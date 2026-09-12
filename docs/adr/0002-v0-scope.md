# 0002. v0 scope

Date: 2026-09-11
Status: accepted

## Decisions

1. **macOS first.** Linux later, only if the macOS loop is working and people ask.
2. **Local first.** The host daemon runs on the developer's own Apple silicon Mac and
   manages VMs there. No control plane, no hosting, no accounts in v0.
3. **Shell + screenshot first, computer use second.** v0 ships exec and screenshot.
   Click, type, scroll ship as the next milestone on the same daemon.
4. **First user is a solo developer running Claude Code, via MCP.** No GitHub App in
   v0. Evidence is written to local disk; posting it to a PR comes later.

## Consequences

- The host daemon and its MCP surface are the whole product in v0.
- Everything hosted-only (auth, billing, fleet, image distribution) is out of scope.
- The developer's Mac is the VM host, so the VM image must fit alongside their own
  Xcode install. Disk budget matters from day one.
