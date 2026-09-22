# Landscape: macOS VMs for AI agents

**Conclusion: nobody sells a managed, per-second, ephemeral macOS desktop for agents with
evidence handed back to the PR.** Apple's licence keeps the field small, and the closest
players are either CI, local-only, or tied to one agent. Partial notes; not a full report.

- **Apple's SLA** allows at most two macOS VMs per Apple-branded host, for development,
  testing, macOS Server or personal use, and forbids "service bureau, time-sharing,
  terminal sharing, relay service" ([macOS Tahoe SLA 2B(iii)](https://www.apple.com/legal/sla/docs/macOSTahoe.pdf)).
- **Leasing** macOS is allowed only for "Permitted Developer Services" with a 24-hour
  minimum (SLA section 3). That is why AWS and Scaleway bill 24 hours minimum.
- **OpenAI acquired Cirrus Labs** (Tart, Orchard) on 2026-04-07 ([cirruslabs.org](https://cirruslabs.org/)).
  See ADR 0010.
- **Devin** runs macOS agents on Namespace M4 Pro / M5 Max Macs with Xcode, Simulator and
  computer use ([Namespace](https://namespace.so/blog/devin-outposts-devboxes)).
- **Claude Code Desktop** has a native iOS Simulator pane (July 2026), local only, up to 4
  simulators ([docs](https://code.claude.com/docs/en/desktop-ios-simulator)).
- **Scrapybara** ended its Mac/Windows VM service on 2025-10-15 ([X](https://x.com/scrapybara/status/1971655785869726110)).
- **Cua** (YC S25) is the only open-source project shipping macOS agent VMs (Lume) and a
  cloud fleet ([GitHub](https://github.com/trycua/cua)).
- **Windows 365 for Agents** went GA in June 2026 ([blog](https://blogs.windows.com/windowsexperience/2026/01/22/windows-365-for-agents-the-cloud-pcs-next-chapter/)).
- **OpenAI** reportedly bought tens of thousands of Mac minis for computer-use RL;
  Anthropic rents Macs via AWS ([TechRepublic](https://www.techrepublic.com/article/news-openai-mac-mini-mac-studio-ai-agents/)).
