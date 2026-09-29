# 0022. Agent text streams at a reading pace

Date: 2026-09-28
Status: accepted. Amends 0020's pace (decision 2); everything else in 0020 stands.

## Context

0020 streamed a message at 30 words a second and capped the whole reveal at 2 s. After a day
of use the person who uses the Companion every day said it is "still very fast, make it
slightly slower". The cap is most of it: a 200-word message had to arrive in 2 s, 100 words
a second, faster than anyone reads, so a long message still flashed in.

## Decision

1. **15 words a second**, about the pace of reading along, for any message up to 90 words.
2. **The catch-up cap is 6 s.** Past 90 words the rate is `words / 6`, so a long message
   still ends in 6 s plus the last word's fade rather than lagging for half a minute.
3. The 180 ms fade per word, the word cut and the rest of 0020 are unchanged.
   `StreamReveal.wordsPerSecond` and `longestReveal` hold the numbers; the stream tests in
   `AgentAnimationTests` hold the pace and the cap.

## Consequences

- A two-line reply (20 words) takes about 1.4 s instead of 0.8 s.
- A long message takes up to 6 s; a click still finishes it at once, and Reduce Motion
  shows it whole.
