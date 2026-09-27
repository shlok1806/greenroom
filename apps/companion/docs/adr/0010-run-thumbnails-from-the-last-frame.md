# 0010. Run thumbnails from the last frame

Date: 2026-09-25
Update 2026-09-27: Superseded by 0019.
Status: accepted. Settles what 0006 left open for the power-down still ("kept as the run's
thumbnail"; its consequences deferred where the still is kept). Nothing else in 0006
changes.

## Context

ADR 0006 moment 5 freezes a run's final screen as a dithered glyph still at power-down and
keeps it as the run's thumbnail. Layer 6 built the moment in the well only: the still lived
in the view and was gone once the recording showed. The app keeps no run on disk and never
reads a run directory (companion rules), so a thumbnail that outlives the view needs the
daemon. Two ways fit: the app uploads the still it drew, or the daemon names a picture the
app already knows how to draw from.

## Decision

1. **The thumbnail is the run's last recorded frame, drawn by the app.** The daemon stores
   nothing new. The still is the same `GlyphSampler` braille dither the power-down holds,
   sampled from that frame; a run whose machine ended while someone watched shows the same
   picture its power-down froze on (the recorder's last capture), and a run nobody watched
   gets one too.
2. **`GET /api/runs` carries `lastFrame`**, the newest line of `frames.jsonl`
   (`{at, file, step, bytes}`), explicit `null` for a run with none. Not a new route: the
   summary already reads the frame log to count `frames`, so the list of ~100 runs costs no
   more, the daemon decodes nothing, and the JPEG comes from the existing
   `/frames/{file}` route. A torn last line is skipped as every reader does.
3. **Lazy, off the main actor, bounded in bytes.** A row asks for its thumbnail as it
   appears (`RunThumbnails.request`); at most 4 fetches run at once. ImageIO decodes the
   JPEG downsampled to a few pixels a dot, off the main actor, and only the rendering (a
   few KB) is held, in an LRU bounded in bytes (2 MB, hundreds of runs). Theme changes
   need no refetch: the row draws the rendering in the theme's own colours.
4. **No request storm.** One fetch per run per frame per session: a row scrolling back, a
   list re-read or a resync naming the same frame fetches nothing; a frame the daemon
   refused, or that will not sample, is not asked for again until a newer one is named. A
   live run follows its `frame` events, but its row's `lastFrame` moves on at most every
   5 s (`RunThumbnails.liveRefresh`) and a newer frame refetches no faster; nothing polls.
5. **Calm, in the theme.** 56 x 44 pt (`tokens.json` `layout.runThumbnailWidth`,
   `runThumbnailHeight`), radius `sm`, a hairline, on the background. Dots are the dim role
   on a 2 pt lattice with 1 pt dots, so every dot lands on a pixel and a 1x display draws it
   as crisply as Retina, over a faint wash of each cell's light. A dark theme draws the lit
   dots, a light one the unlit, so both show the picture and not its negative. A run with no
   frames shows a short dotted rule in the same box; one still loading shows the empty box.
   The box is always there, so a row never moves when its thumbnail lands.
6. **Only the wide list (and the list opened over a medium window) shows them.** The strip
   keeps its glyph marks. The title sits beside the thumbnail; the time and state run under
   both at the row's full width, so a long state keeps its words at 240 pt.

## Consequences

- The thumbnail is the last capture, not the exact picture the view dissolved: the live
  stream can be a moment newer than the recorder's last frame. Close enough to read as the
  same screen; exact would need the app to upload its still, which this ADR rejects.
- A daemon before `lastFrame` decodes (the field is optional) and every row shows the
  empty mark.
- A run's frames being deleted (retention, ADR 0008's deferred work) leaves `lastFrame`
  naming a file that 404s; the row then keeps the empty box and does not ask again.
