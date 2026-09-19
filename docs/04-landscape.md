# Landscape: macOS VMs for AI coding agents (draft v1, in progress)

Status: partial draft written early so progress survives. Sections will be expanded.

## Summary

- Apple's SLA allows at most two macOS VMs per Apple-branded host, only for development, testing, macOS Server, or personal use, and forbids "service bureau, time-sharing, terminal sharing, relay service" uses ([macOS Tahoe SLA, 2B(iii)](https://www.apple.com/legal/sla/docs/macOSTahoe.pdf)).
- Leasing macOS is allowed only for "Permitted Developer Services" with a 24-hour minimum per lease ([SLA section 3](https://www.apple.com/legal/sla/docs/macOSTahoe.pdf)). This is why AWS and Scaleway bill 24 hours minimum.
- OpenAI acquired Cirrus Labs (Tart, Orchard) on April 7, 2026 for its "Agent Infrastructure team" ([cirruslabs.org](https://cirruslabs.org/)).
- Devin now runs macOS cloud agents on Namespace M4 Pro / M5 Max Macs with Xcode, Simulator and computer use ([Namespace blog](https://namespace.so/blog/devin-outposts-devboxes)).
- Anthropic shipped a native iOS Simulator pane in Claude Code Desktop (July 2026), local sessions only, up to 4 simulators per session ([docs](https://code.claude.com/docs/en/desktop-ios-simulator)).
- Scrapybara, the YC "computer for your AI" company that offered Mac/Windows desktops, sunset its VM service on Oct 15, 2025 and pivoted to Capy ([X post](https://x.com/scrapybara/status/1971655785869726110)).
- Cua (YC S25, 3 people, ~$500K) is the only open-source project shipping macOS agent VMs (Lume) and cloud fleets ([GitHub](https://github.com/trycua/cua)).
- Microsoft launched Windows 365 for Agents (GA June 2026) ([Windows blog](https://blogs.windows.com/windowsexperience/2026/01/22/windows-365-for-agents-the-cloud-pcs-next-chapter/)).
- OpenAI reportedly bought "tens of thousands" of Mac minis for computer-use RL; Anthropic rents Mac minis via AWS ([TechRepublic on The Information](https://www.techrepublic.com/article/news-openai-mac-mini-mac-studio-ai-agents/)).
- Nobody sells a managed, per-second, ephemeral macOS agent desktop with PR evidence hand-back.

(Full report follows in later revision.)
