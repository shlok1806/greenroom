# Acknowledgements

Code the Companion copies or ports from other projects, with each licence. Only permissive
licences (MIT, Apache-2.0, BSD, ISC) are used; never GPL, AGPL or BSL (companion ADR 0019,
point 7). Packages the app depends on are listed in `Package.swift` under companion ADR 0007.

## Ghostty

Command palette (`macos/Sources/Features/Command Palette/CommandPalette.swift`), adapted in
`Sources/Companion/UI/KeyPalette.swift`: the query field's key events, the option list
with keyboard selection, the substring-then-initials match and the ranking.
<https://github.com/ghostty-org/ghostty>

```
MIT License

Copyright (c) 2024 Mitchell Hashimoto, Ghostty contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## Beautiful UI

Task Rows (`components/primitives/TaskRows.tsx`), Thinking state (`ThinkingState.tsx`), Tool
Chips (`ToolChips.tsx`) and Shimmer (`components/atoms/Shimmer.tsx`), ported to SwiftUI in
`Sources/Companion/UI/Components/Activity.swift`: the row that expands its detail under a
rotating chevron, the status badge, the shimmer across a working label, the chips of tool
calls. Sizes, colours and durations follow the Figma file, which restyled them.
<https://github.com/slev12397/beautiful-ui>

```
MIT License

Copyright (c) 2026 Shane Levine

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## Motion rules

Durations, easing and the "only state changes move" rule follow Emil Kowalski's published
guidance (<https://emilkowal.ski/ui/7-practical-animation-tips>, "You don't need
animations") as the Figma Tokens page records it. No code is copied.

## Design sources

The Figma file "Greenroom Companion redesign" (041UmqtdMYVCufxO8g9Ius) is the source of the
tokens and components; its sources are listed on its Principles page and in
`docs/20-companion-ux-research.md` section 12.
