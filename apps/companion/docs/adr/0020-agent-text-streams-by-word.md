# 0020. Agent text streams in by the word

Date: 2026-09-28
Status: accepted. Amends 0019's port of Beautiful UI's StreamText (docs/22 C12) for the
conversation's messages; the caret stays.

## Context

A message that arrives while the conversation shows streams in once (redesign 7). The port
kept StreamText's demo cadence: two characters every 9 ms, about 220 characters a second,
the last six characters fading. The person who uses the Companion every day asked for it to
be "slightly slower, follow the convention followed by ChatGPT and Claude". Those apps:

- reveal whole words, never part of one, and never show Markdown syntax mid-token (a
  half-open `**` reads as two stars until it closes);
- fade each word in over a short time as it lands, so the edge of the text is soft
  rather than a character-by-character typewriter;
- keep a steady pace a reader can follow just ahead of their eyes (a few tens of words a
  second), and speed up rather than fall far behind when a lot is waiting.

The daemon sends each message whole, so the whole text, and its word count, is known when the
reveal starts. The old reveal also re-parsed the Markdown on every frame, and it announced
its end only when the revealed count changed, so a message whose reveal was already over when
it first drew (its conversation opened late) never ended and kept its `TimelineView` ticking.

## Decision

1. **Reveal by word over the parsed Markdown.** The message is parsed once; the reveal cuts
   the parsed blocks at a word boundary (`StreamReveal.cut`), so what shows is always styled
   text, never raw syntax. A word is a run of non-space characters and the spaces after it;
   words that span styles (`**bold**ly`) are one word, a cited step's chip is one word. A code
   block reveals a line at a time, a table a row at a time, a rule at once.
2. **The pace: 30 words a second, and never more than 2 s in all.** The rate is
   `max(30, words / 2)` words a second, so a short reply reads at a comfortable pace and a
   long one catches up rather than lagging: the whole reveal is at most 2 s plus the last
   word's fade. The first word shows at once.
3. **Each word fades up as it lands**: opacity 0 to 1 and a 3 pt rise over 180 ms on the
   design's curve (`AgentMotion.curve`, Beautiful UI's `fade-up` scaled down to a word). A
   `TextRenderer` draws each arriving word from its own clock, so the words fade inside one
   wrapped `Text` without re-laying the line out.
4. **A click finishes it, Reduce Motion shows the text at once, and the reveal always ends**:
   its end is checked on the first frame too, so a reveal already over when it first draws
   hands back at once and nothing keeps ticking.
5. All of it lives in `StreamReveal` with named constants; `StreamRevealTests` holds the pace,
   the cap, the cut and the fade.

## Consequences

- The source's 1.6 px blur on the tail and its six-character mask go; the fade carries the
  soft edge.
- A long message still arrives in 2 s; a two-line reply takes about a second.
- `docs/22` C12 still describes the source; this record is what the Companion draws.
