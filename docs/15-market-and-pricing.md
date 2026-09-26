# Market, competitors and pricing for "every agent-made PR arrives with proof it works"

**Conclusion.** The demand signal is real and getting louder. Microsoft says 1 in 3 GitHub
PRs now involves an agent. Studies show that agent PRs wait about 5x longer for review and
mostly get no human review at all. GitHub's own advice to reviewers is "require concrete
evidence".

The wedge as written ("proof on the PR for iOS, then Android") is **no longer empty**:

- **Revyl** (YC F24) already ships a GitHub App that builds the PR and drives cloud
  iOS/Android simulators. It posts "Proof of changes" on the PR: captioned screenshots, a
  public recording and a 1-5 confidence score.
- **TesterArmy** (YC, 2026) and **TestMu AI KaneAI's GitHub App** do close variants of the
  same thing.
- **Cursor, Devin, Copilot and Codex** all attach screenshots or video to their own PRs,
  but for web and Linux targets only.

The gap left is narrower and still defensible:

- **Native macOS apps.** Nobody hosted does this.
- **An independent verifier**, rather than the coding agent grading its own work.
- **A full Mac with the real Xcode toolchain**, rather than a simulator farm driven from
  Linux.
- **Selling to teams whose cloud agents cannot touch a Mac.** Copilot's cloud agent
  explicitly refuses macOS runners and Codex cloud is Linux-only.

On cost, compute per verified PR is cents on owned or monthly-leased Macs and under
$1.50 even on AWS. So COGS is dominated by the verifier model and by Apple's leasing rules
(24-hour exclusive leases), not by minutes.

The riskiest parts are:

1. Whether buyers pay a third party for verification that their coding agent is starting
   to include.
2. Whether the evidence is trustworthy enough to cut review time.

Both are cheap to test before building the hosted fleet.

Collected 2026-09-25 by a lead agent plus four research sub-agents. **Every URL was
accessed 2026-09-25.**

Labels:

- **[P]** primary source: vendor page, docs, official post or filing.
- **[C]** the vendor's claim about itself. It sits on a primary page but is not
  independently checked.
- **[S]** secondary source: press or aggregator.
- **[U]** unverified: could not be confirmed at a primary source.
- **(re-checked)** the lead agent fetched the primary page again to confirm a load-bearing
  fact.

This updates `04-landscape.md`. Facts already there are not repeated: the Apple SLA basics,
Cirrus to OpenAI, Devin on Namespace, the Claude Code iOS Simulator pane, Scrapybara's
shutdown, Cua and Windows 365.

---

## 0. What changed since `04-landscape.md`

| New fact | Why it matters | Source |
| --- | --- | --- |
| Revyl's GitHub App posts "Proof of changes" on PRs for iOS/Android, with a confidence score, screenshots and an unauthenticated recording. Proof runs can be delegated to a Cursor cloud agent. | The iOS wedge has an incumbent | [P] (re-checked) https://docs.revyl.com/integrations/github |
| TesterArmy: on each PR, reads the diff, writes a plan, runs it and reports with screenshots and recordings. Web plus iOS/Android simulators. $299/mo for 1,000 runs. | A second per-PR mobile verifier, priced per run | [P] (re-checked) https://tester.army/pricing, [P] https://news.ycombinator.com/item?id=48586299 |
| TestMu AI (formerly LambdaTest) KaneAI GitHub App, 2026-04-08: "@KaneAI Validate this PR" generates tests from the diff and posts screenshots, video and logs to the PR. Native mobile only on the $179/agent/mo Max tier. | A funded device cloud ($108M raised) is doing PR evidence | [P] https://www.globenewswire.com/news-release/2026/04/08/3270331/0/en/TestMu-AI-Announces-GitHub-App-Integration-for-KaneAI-enabling-End-to-End-AI-Powered-Test-Validation-Directly-in-Pull-Requests.html |
| GitHub Copilot cloud agent: "only compatible with Ubuntu x64 Linux and Windows 64-bit runners. Runners with macOS or other operating systems are not supported." Setup is capped at 59 min. | GitHub's own agent cannot build or run a Mac/iOS app | [P] (re-checked) https://docs.github.com/en/copilot/how-tos/use-copilot-agents/coding-agent/customize-the-agent-environment |
| Cursor cloud agents support self-hosted macOS workers, including computer use (the "Cursor Computer Use" helper needs Accessibility and Screen Recording). Team Pools need Enterprise. | A distribution channel, and also a bundling path | [P] (re-checked) https://cursor.com/docs/cloud-agent/self-hosted |
| The next macOS SLA (macOS27.pdf) adds "except as otherwise provided in writing, signed, or issued by an authorized representative of Apple" to both the 2-VM clause (2B(iii)) and the leasing clause (3A). | Apple can now grant bespoke exemptions from the 2-VM and 24 h rules. Large players (OpenAI, Anthropic) may get terms we do not. | [P] (re-checked) https://www.apple.com/legal/sla/docs/macOS27.pdf |
| Xcode 27 replaces the Simulator app with "Device Hub". The Claude Desktop simulator pane needs Xcode 26.x. | Simulator-control tooling breaks each Xcode cycle, which is a moat for whoever maintains it | [P] https://developer.apple.com/videos/play/wwdc2026/259/, [P] https://code.claude.com/docs/en/desktop-ios-simulator |
| Mac mini lineup changed in Aug 2026: M6 from $899, M5 Pro from $1,699. The M4 is no longer sold. | Updates hardware cost basis | [P] https://www.apple.com/newsroom/2026/08/apple-unveils-a-more-powerful-mac-mini-featuring-the-all-new-m6-and-m5-pro/ |

---

## 1. Market size signals

### 1.1 Apple platforms

- **2,172,472 apps** on the App Store and **60,597,750 registered Apple developers** in
  2025. Apple reviewed **9,100,620 submissions** and rejected 2,093,244 (about 23%).
  Performance was the top rejection reason (about 1.35M). [P]
  https://www.apple.com/legal/app-store/transparency/2025/
  - 2024 for comparison: 1,961,596 apps and 51,766,243 developers. [P]
    https://www.apple.com/legal/more-resources/docs/2024-App-Store-Transparency-Report.pdf
  - "Registered developers" counts accounts, not working professionals. No primary count
    of active iOS/macOS engineers exists [U].
- **$1.4T** in billings and sales facilitated by the App Store ecosystem in 2025, of which
  $149B is digital goods. This is an Analysis Group study commissioned by Apple, and 850M
  weekly App Store users. [P][C]
  https://www.apple.com/newsroom/2026/06/app-store-ecosystem-reaches-1-point-4-trillion-usd-as-developers-thrive-globally/
- **Share of developers** in the Stack Overflow 2025 survey (49k+ respondents):
  - Swift 5.4% of all, 5.7% of professionals.
  - Xcode 10.0% and Android Studio 15.0% of all.
  - Kotlin 10.8%, Dart 5.9%. [P] https://survey.stackoverflow.co/2025/technology
  - The 2026 survey opened 2026-06-23 and results are not out. [P]
    https://stackoverflow.blog/2026/06/23/the-2026-developer-survey-is-now-open-for-human-developers-only/
- **macOS apps** specifically: no Apple figure separates Mac App Store apps or Mac
  developers [U]. This matters, because macOS is where Greenroom has no competitor.

### 1.2 Android

- **"More than 2 million developers"** on Play (Google, I/O 2026). The exact sentence was
  not found in the post [S].
  https://android-developers.googleblog.com/2026/05/io-2026-whats-new-in-google-play.html
- About **2.4M Play apps** from about 739k publishers. Third-party tracker [S][U].
  https://www.businessofapps.com/data/google-play-statistics/
- **Kotlin Multiplatform** use rose from 7% to 18% of respondents between 2024 and 2025
  (JetBrains data). [P] https://kotlinlang.org/docs/multiplatform/kotlin-multiplatform-react-native.html,
  [P] https://blog.jetbrains.com/research/2025/10/state-of-developer-ecosystem-2025/
- **Bitrise build data:** 30% of builds are non-native apps, and React Native went from
  63% to 83% of cross-platform builds between 2022 and 2025. [C]
  https://bitrise.io/blog/post/bitrise-mobile-insights-report-defines-new-benchmarks-for-app-velocity-and-performance
  - Implication: a large share of "mobile" is React Native. Expo (EAS Simulator) and
    Revyl already serve those teams well.

### 1.3 Mobile CI and testing spend

- **Bitrise Mobile Insights 2025** (10M+ builds) [C]:
  - Pipelines became 23% more complex.
  - The share of teams hitting flakiness rose from 10% to 26%.
  - **73% of builds are triggered from GitHub.**
  - Top teams adopt a new Xcode in 4 weeks versus 19-21 weeks for others.
  - Same URL as above.
- **Mobile application testing services market:** $7.70B (2025) to $19.84B (2031),
  17.1% CAGR (Mordor). A different definition gives $18.33B (TechSci). These are
  low-rigour analyst numbers; do not put them in a pitch as TAM. [S]
  https://www.mordorintelligence.com/industry-reports/mobile-application-testing-services-market
- **macOS compute costs about 10x Linux** on GitHub-hosted runners: $0.062/min against
  $0.006/min. [P] https://docs.github.com/en/billing/reference/actions-runner-pricing
  - The premium is what makes teams ration macOS CI. That pushes them toward skipping
    UI verification per PR, which is the gap Greenroom fills.
- **Device cloud revenue scale:** BrowserStack expects to "cross USD 300 million in revenue
  in calendar year 2026" [C][S].
  https://www.entrepreneurindia.com/blog/en/news/browserstack-announces-usd-125-mn-esop-and-share-buyback-programme.58738
  - Buyers already pay hundreds of millions a year for "run my app on a device and show me
    it works".

### 1.4 Growth of coding agents

| Signal | Number | Source |
| --- | --- | --- |
| GitHub | 180M+ developers, **43.2M PRs merged per month** (+23% YoY) | [P] https://github.blog/news-insights/octoverse/octoverse-a-new-developer-joins-github-every-second-as-ai-leads-typescript-to-1/ |
| Copilot coding agent | 1M+ PRs authored, May-Sep 2025 | [C] same |
| Microsoft FY26 Q4 call, 2026-07-29 | "GitHub Copilot now has 50 million users"; GitHub has 225M users; **"1 in 3 pull requests on GitHub now involves an agent"** | [P][C] (re-checked) https://www.fool.com/earnings/call-transcripts/2026/08/07/microsoft-msft-q4-2026-earnings-call-transcript/ |
| GitHub, 2026-05-07 | Copilot code review has done 60M+ reviews (10x in under a year); "more than one in five code reviews on GitHub now involve an agent" | [P][C] (re-checked) https://github.blog/ai-and-ml/generative-ai/agent-pull-requests-are-everywhere-heres-how-to-review-them/ |
| Claude Code | $1B run-rate in Dec 2025; over $2.5B run-rate in Feb 2026, more than half from enterprise; "4% of all GitHub public commits" (a SemiAnalysis figure) | [P][C] https://www.anthropic.com/news/anthropic-acquires-bun-as-claude-code-reaches-usd1b-milestone, https://www.anthropic.com/news/anthropic-raises-30-billion-series-g-funding-380-billion-post-money-valuation |
| OpenAI Codex | 5M+ weekly users in June 2026 | [C] https://x.com/OpenAINewsroom/status/2061834718224777579. Later 8M/20M figures are [U] |
| Cursor | Over $4B run-rate in June 2026 (press); "more than 30% of the PRs we merge at Cursor are now created by agents operating autonomously in cloud sandboxes" (2026-02-24) | [S] https://dealroom.co/news/134107-cursor-tops-4b-annualized-revenue/, [C] (re-checked) https://cursor.com/blog/agent-computer-use |
| Cognition (Devin) | "Almost $900M" run-rate, $48B valuation (Sep 2026) | [S][C] https://techcrunch.com/2026/09/08/cognition-hits-48b-valuation-signaling-investors-believe-ai-coding-is-far-from-a-winner-take-all-market/ |
| Tool use at work, JetBrains AI Pulse (Jan 2026) | Copilot 29%, Cursor 18%, Claude Code 18% (up from about 3%), Codex 3% | [P] https://blog.jetbrains.com/research/2026/04/which-ai-coding-tools-do-developers-actually-use-at-work/ |

### 1.5 Agent PRs and the verification bottleneck

This is the core of the pitch.

- **AIDev dataset:** 932,791 agent-authored PRs across 116,211 repos. [P]
  https://arxiv.org/abs/2602.09185
  - Per-agent merge rates quoted from it (for example Codex about 64-83% and Copilot about
    35-43%, against 79% for human PRs) come from summaries [U].
- **61.38% of AI-generated PRs have no recorded review.** Of the reviewed ones, 58.77% are
  reviewed only by agents, and only 8.08% get human-only review (against 25.21% for human
  PRs). [P] https://arxiv.org/html/2605.02273v1
- **Failed agent PRs** are larger and "often do not pass the project's CI/CD pipeline
  validation". [P] https://arxiv.org/abs/2601.15195
- **LinearB, 8.1M PRs** [C]:
  - AI PRs wait more than 16 h for pickup, against about 200 min for others (about 5x).
  - 32.7% of AI PRs merge within 30 days, against 84.5%.
  - AI PRs are about 2.6x larger.
  - https://linearb.io/blog/8-million-prs-engineering-productivity
- **Trust (Stack Overflow 2025)** [P] https://survey.stackoverflow.co/2025/ai:
  - 46% distrust AI accuracy and 33% trust it; only 3% "highly trust".
  - 66%'s top frustration is output that is "almost right, but not quite".
  - 87% worry about agent accuracy.
- **GitHub's own reviewer guidance** says to require "concrete evidence" (tests that would
  have failed before, CI not weakened, critical paths traced end to end). [P] (re-checked)
  same GitHub URL as the table.
  - Greenroom's pitch is the visual version of this advice, and the platform owner is
    already telling reviewers to ask for it.

### 1.6 Rough serviceable volume

An order-of-magnitude estimate, not a forecast. Every input is an assumption.

- 43.2M merged PRs a month on GitHub. Assume 5% are in Apple-platform repos (Swift share
  about 5.5%) and 1/3 involve an agent. That gives **about 700k agent Apple-platform PRs a
  month**.
- Assume 25% change UI or behaviour worth proving visually: **about 180k PRs a month**.
- At $3 per verified PR that is **about $6.5M a month, or $78M a year** of theoretical
  spend at 100% capture.
- Private-repo PRs are probably under-counted, and agent share is rising, so read this
  as "a real but niche market today that scales with agent adoption". It is not a
  billion-dollar TAM on day one.
- The Android equivalent is similar or larger in developers, but Firebase, Android Studio
  Journeys and Linux emulators serve it more cheaply (section 5).

---

## 2. Competitors and adjacent players

### 2.1 Doing "agent PR verification with evidence" today (direct)

| Player | What posts on the PR | Platforms | Pricing | Funding | Source |
| --- | --- | --- | --- | --- | --- |
| **Revyl** (YC F24) | Preview build status; "Proof of changes" write-up with a 1/5-5/5 confidence score; captioned screenshots; public recording link; problem findings; workflow results. The agent checks out the PR in a sandbox and drives cloud devices. Can delegate proof to a Cursor cloud agent. | iOS sim, Android emulator. **No macOS mentioned** | Free; Starter $250/mo ($200 annual) including $250 of compute, 3 concurrent devices; Team Pro $750/mo, 10 devices; overage **iOS $0.15/min, Android $0.12/min**; unlimited seats | About $1.1-1.6M pre-seed [S] | [P] (re-checked) https://docs.revyl.com/integrations/github, https://revyl.com/pricing; [S] https://betakit.com/canadian-founded-yc-backed-revyl-closes-1-5-million-cad-to-help-companies-catch-bugs-with-ai/ |
| **TesterArmy** (YC, 2026) | Test plan from the diff, screenshots, recordings in GitHub | Web; iOS/Android simulators (no real devices) | Free 5 runs; Hobby $99/mo for 250 runs; Startup **$299/mo for 1,000 runs** (about $0.30/run, run up to 20 min); unlimited members | Not found | [P] (re-checked) https://tester.army/pricing |
| **TestMu AI KaneAI GitHub App** | Tests generated from the diff, run on HyperExecute; screenshots, video, network logs, root-cause analysis in the PR | Web on all tiers; native mobile on Max | Starter $17, Pro $89, **Max $179 per agent/mo** (annual, credits); per-PR credit cost not published | $108M total; $38M Series D Dec 2024 | [P] https://www.testmuai.com/pricing/, [P] https://www.prnewswire.com/news-releases/lambdatest-closes-38-million-to-revolutionize-qa-with-ai-native-qa-agent-as-a-service-302327301.html |
| **Cursor cloud agents** | "videos, screenshots, and logs" for the agent's own PR | Cloud VMs (Linux [S]); self-hosted macOS workers with computer use | Model usage; Bugbot is separate | $4B+ run-rate [S] | [P] (re-checked) https://cursor.com/blog/agent-computer-use |
| **Devin testing mode** | Annotated video and a screenshot report, in the Devin app and Slack rather than natively on the PR | Browser and desktop VMs; iOS/macOS not mentioned | Testing billed at 1/5 of normal usage [C] | $2B+ raised [S] | [P] https://docs.devin.ai/work-with-devin/testing-and-recordings |
| **Copilot coding agent** | Playwright screenshots in the PR | Web only; macOS runners unsupported | Premium requests | GitHub | [P] https://github.blog/changelog/2025-07-02-copilot-coding-agent-now-has-its-own-web-browser/ |
| **Codex cloud** | Browser screenshot on the task and PR | Linux only | ChatGPT plans | OpenAI | [S] openai.com page returned 403; [P] https://learn.chatgpt.com/docs/cloud |
| **Ranger** | Browser agents verify what the coding agent built; screenshots, video and traces in a PR-like dashboard | Web | Not public | $8.9M | [P] https://docs.ranger.net/, https://www.ranger.net/post/ranger-raises-8-9m-to-find-bugs-faster |
| **Expo EAS Simulator** (waitlist) | Agents get a real iOS runtime; session replays for PRs | iOS, React Native/Expo-leaning | EAS compute allowance | Expo | [P] https://expo.dev/services/simulators |

**Read-out:**

- Evidence on a PR has become table stakes for web.
- For iOS it is contested by small, seed-stage teams: Revyl, TesterArmy, Momentic.
- For **native macOS apps** nobody hosted does it. The only thing close is Callstack's
  open-source `agent-device`, which lists macOS as a local target. [P]
  https://github.com/callstack/agent-device
- None of these can put an independent verifier on a **real Mac with a full desktop
  session**, able to test menus, windows, permissions dialogs, drag and drop,
  multi-window flows, Mac Catalyst, extensions, or iOS apps together with Mac-side
  tooling.
- RedMonk's 2026-09-15 write-up of the "agents with iPhones" category confirms the iOS
  crowding. [S] https://redmonk.com/kholterhoff/2026/09/15/agents-with-iphones-simulator/

### 2.2 Mobile CI

This is where Mac build minutes are sold. The prices below are the reference point for
buyers.

| Vendor | macOS price | Plans | Funding / ownership | AI features | Source |
| --- | --- | --- | --- | --- | --- |
| GitHub Actions | $0.062/min (3-4 core), $0.077 (12-core), **$0.102 (M2 Pro 5-core)**; cut up to 39% on 2026-01-01 | Larger runners need Team/Enterprise | Microsoft | Copilot | [P] https://docs.github.com/en/billing/reference/actions-runner-pricing |
| Xcode Cloud | 25 h/mo free with the Developer Program; 100 h $49.99, 250 h $99.99, 1,000 h $399.99, 10,000 h $3,999.99 (about **$0.007-0.008/min**) | Apple-only SCM integration | Apple | None; WWDC26 added webhooks and multi-repo | [P] https://developer.apple.com/xcode-cloud/, https://developer.apple.com/videos/play/wwdc2026/261/ |
| Bitrise | Listed M2 Pro/M4 Pro per-minute rates of $0.007-0.029 look like on-top-of-plan or credit rates [U]; Starter $89-99/mo, Pro $200-218/mo | 6,000+ mobile orgs [C] | About $100M raised; $60M Series C 2021 (Insight) | AI code review at 1 credit/review, Build Fixer, MCP; Bitrise Agent (BYO Macs) in closed beta | [P] https://bitrise.io/pricing, https://docs.bitrise.io/en/bitrise-platform/ai/ai-features-on-bitrise.html |
| Codemagic | M2 $0.095/min, **M4 $0.114/min**; flat unlimited plans $3,990-9,000/yr; app preview add-on $0.095/min | 500 free M2 min/mo | Nevercode; small rounds [U] | None | [P] https://codemagic.io/pricing/ |
| CircleCI | M4 Pro Medium 200 credits/min (about **$0.12/min**), Large about $0.24/min | Performance from $15/mo | $315.5M raised; $1.7B valuation (2021) | "Autonomous validation for the AI era"; Chunk agent free; Chunk Sidecars (Linux microVMs) | [P] https://circleci.com/pricing/price-list/, [S] https://www.infoq.com/news/2026/06/circleci-chunk-sidecars/ |
| Buildkite | Hosted Mac $0.02/vCPU-min (about $0.12/min for 6 vCPU) | $30/active user | About $41-47M [U] | n/a | [P] https://buildkite.com/pricing |
| Namespace | macOS M4 Pro/M5 Max, 4-16 vCPU at **$0.04-0.16/min** | Team $100/mo, Business $250/mo | $23M seed+A (NEA), Mar 2026 | Hosts Devin's Mac devboxes | [P] https://namespace.so/pricing, https://namespace.so/blog/series-a |
| Blacksmith / Depot / WarpBuild | macOS about **$0.08/min** | Various | Blacksmith $10M A (GV); Depot $10M A (Felicis, "agent sandboxes") | Depot is pivoting toward agent sandboxes | [P] https://www.blacksmith.sh/pricing, https://depot.dev/blog/depot-raises-series-a, https://www.warpbuild.com/pricing |
| Cirrus Runners | Was $150/runner/mo, unlimited minutes | **No new customers** since the OpenAI deal; Cirrus CI shut 2026-06-01 | OpenAI | n/a | [P] https://cirruslabs.org/ |

### 2.3 Device clouds

| Vendor | Pricing | AI | Funding / scale | Source |
| --- | --- | --- | --- | --- |
| BrowserStack | App Live and App Automate prices load dynamically; third-party figures are App Automate about $199-249/mo per parallel [U] | BrowserStack AI (20+ agents); **Test Companion** (2026-07-29), an IDE agent that authors and heals tests on "30,000+ real devices"; MCP server | $4B valuation (2021); "cross USD 300 million" revenue in 2026 [C] | [P] https://www.browserstack.com/pricing, https://www.prnewswire.com/news-releases/browserstack-launches-test-companion-agentic-ai-that-brings-complete-test-automation-into-the-ide-302837727.html |
| Sauce Labs | Virtual devices $149-199/mo, real devices $199-249/mo per parallel | AI Test Authoring GA 2026-04-29 (enterprise); "validation is becoming the primary constraint" [C] | Thoma Bravo-owned [U] | [P] https://saucelabs.com/pricing, [S] https://www.infoq.com/news/2026/04/sauce-labs-ai-test-creation/ |
| TestMu AI / LambdaTest | Real device $39/mo; KaneAI $17-179/agent/mo | KaneAI GitHub App (2.1) | $108M raised; 18k+ customers [C] | [P] https://www.testmuai.com/pricing/ |
| AWS Device Farm | **$0.17/device-min** ($10.20/device-hour); unmetered $250/mo per slot | None | AWS | [P] https://aws.amazon.com/device-farm/pricing/ |
| Firebase Test Lab | **$1/h virtual, $5/h physical**; free daily quota | App Testing agent (below) | Google | [P] https://firebase.google.com/docs/test-lab/usage-quotas-pricing |
| Perfecto (Perforce) | Live from $83, Automate from $125/mo [U] | Perfecto AI, natural language to tests (2025-07-15) | PE-owned [U] | [P] https://www.perforce.com/press-releases/perfecto-ai |

### 2.4 AI QA

| Vendor | Model | Pricing | Funding | Source |
| --- | --- | --- | --- | --- |
| Momentic | "Verification layer for software"; mobile launched Jun 2026; MCP for coding agents | $125/mo for 10k credits; **iOS sim 15 credits/min (about $0.28/min)**, Android 8 credits/min | $15M A (Standard Capital), Nov 2025; 2,600 users [C] | [P] https://momentic.ai/pricing, https://momentic.ai/blog/series-a |
| QA Wolf | Managed E2E; mobile only in managed "Coverage as a Service" | 1¢/AI credit + 15¢/runner-min; no seats | $57M total | [P] https://www.qawolf.com/pricing |
| Maestro (mobile.dev) | Open-source mobile flows, MCP, cloud | **$250/device/mo**, unlimited runs, PR integration | n/a | [P] https://maestro.dev/pricing |
| Autify | Nexus (Playwright) and Aximo agent | Nexus from $400/mo; Aximo about $99-550/mo | $13M B (2024) | [P] https://autify.com/pricing |
| mabl | Agentic web/mobile/API | Quote only | 2021 C [U] | [P] https://www.mabl.com/pricing |
| Waldo | Acquired by Tricentis 2023-07-07 | n/a | n/a | [P] https://www.tricentis.com/news/tricentis-acquires-codeless-mobile-test-automation-platform-waldo |
| Meticulous | Record and replay; visual diffs on PRs (web) | Custom | $15M A Jul 2026 [S] | [S] https://www.vestbee.com/insights/articles/meticulous-raises-15-m |
| Drizz / Quash / Minitap | Vision-AI mobile testing agents | Per run or volume | $2.7M / $635k / $4.1M seeds | [S] business-standard, [P] https://quashbugs.com/blog/announcing-our-pre-seed-fundraise, [S] Forbes |
| Octomind | Web AI testing | n/a | $4.8M seed | Reportedly shut in May 2026; the domain did not resolve [U] |
| Limrun (YC) | **Infrastructure:** remote Xcode builds and iOS sims on real Macs for Linux-based cloud agents; PR preview links | Not public | Customers include Replit and Momentic [C] | [P] https://limrun.com/ |

Open-source tooling used by all of the above:

- mobile-next/mobile-mcp (about 6.7k stars [S])
- Callstack agent-device (iOS/Android/**macOS**)
- Software Mansion Argent
- XcodeBuildMCP and ios-simulator-mcp

Anyone can wire up simulator control in a weekend. The moat is not the driver.

### 2.5 Agent sandboxes and Mac infrastructure

| Player | macOS? | Price | Funding / status | Source |
| --- | --- | --- | --- | --- |
| Cua | Yes (Lume); cloud | $0.0446/vCPU-h + $0.0223/GB-h; no separate macOS rate found | YC S25 | [C] https://www.cua.ai/pricing |
| E2B | No | $0.05/vCPU-h; Pro $150/mo | $21M A (Insight), Jul 2025 | [C] https://e2b.dev/pricing |
| Daytona | No (Linux, Windows) | $0.0504/vCPU-h | $24M A (FirstMark), Feb 2026 | [C] https://www.daytona.io/pricing |
| Browserbase | No (browsers) | $20-99/mo, $0.10-0.12/browser-hour | $40M B at $300M, Jun 2025 | [C] https://www.browserbase.com/pricing |
| Scrapybara | Ended Mac VMs 2025-10-15 | n/a | Pivoted to "Capy" | see `04-landscape.md` |
| MacStadium | Yes, bare-metal and Orka | M4.S $149, **M4.M $249**, M4.L $349 per month; Orka is contact sales | Pitching Orka as the Tart/Orchard replacement [C] | [P] https://www.macstadium.com/pricing, https://macstadium.com/blog/cirrus-labs-is-joining-openai |
| Tart / Orchard | Yes | FSL-1.1, "Copyright 2022-2026 OpenAI"; v2.38.0 released 2026-09-24 | OpenAI | [P] https://github.com/cirruslabs/tart/blob/main/LICENSE; ADR 0010 |

### 2.6 AI code review

Useful mainly as the reference for how per-PR pricing is accepted.

| Product | Unit | Price | Adoption / funding | Source |
| --- | --- | --- | --- | --- |
| CodeRabbit | Per PR-opening seat | $24-30 (Essentials), $48-60 (Team), $72 (Advanced); overage $0.25/file; agent $0.40/agent-minute | $143M C at $1.5B (2026-08-12); 2M+ reviews/week, 17k+ customers [C][S] | [P] https://www.coderabbit.ai/pricing, https://www.coderabbit.ai/newsroom/coderabbit-series-c-agentic-change-management |
| Greptile | Seat + credits | $30/seat for 50 credits, $1/extra credit; a review costs 1, 3 or 10 credits | $25M A (Benchmark) | [P] https://www.greptile.com/pricing |
| Graphite (Cursor) | Seat | $20-40/user | Acquired by Cursor, Dec 2025 | [P] https://graphite.com/pricing, https://cursor.com/blog/graphite |
| Cursor Bugbot | **Per run** (was $40/seat) | About $1.00-1.50/run average, from 2026-06-08 | 80% of flagged bugs resolved [C] | [P] https://cursor.com/blog/may-2026-bugbot-changes |
| Claude Code Review | **Per review** (tokens) | About **$15-25 per review**; never blocks merge (neutral check) | Anthropic; Team/Enterprise | [P] https://code.claude.com/docs/en/code-review |
| Copilot code review | Usage (AI credits) | 13 premium requests per review plus Actions minutes [S] | 60M+ reviews [C] | [P] https://github.blog/news-insights/company-news/github-copilot-is-moving-to-usage-based-billing/ |
| Qodo | Base + credits | About $30 + credits [S] | $70M B, Mar 2026 ("code verification") | [S] https://techcrunch.com/2026/03/30/qodo-bets-on-code-verification-as-ai-coding-scales-raises-70m/ |
| Codex review | Included in ChatGPT plans | n/a | OpenAI | [P] https://learn.chatgpt.com/docs/pricing |

**Pricing trend:** in 2026 the market moved from flat seats toward per-run and usage
pricing. Bugbot dropped its seat price, and GitHub Copilot, Greptile and Qodo moved to
credits. Buyers now accept roughly **$1 per PR for a light review and $15-25 per PR for a
deep one**. Nobody sells an outcome-priced "verified PR".

---

## 3. Pricing models and unit economics

### 3.1 Price points buyers already see

| Unit | Range | Examples |
| --- | --- | --- |
| macOS CI minute | $0.007 (Xcode Cloud) to $0.24 (CircleCI Large); typical $0.06-0.12 | 2.2 |
| Simulator/device minute for an agent | $0.12-0.28/min | Revyl $0.15 iOS, Momentic about $0.28 iOS, Device Farm $0.17 real device |
| Device-hour | $1-5 (Firebase), $10.20 (Device Farm) | 2.3 |
| Concurrent device per month | $149-250 | Maestro $250, Sauce $149-249, Device Farm $250 |
| Per test run | About $0.30 | TesterArmy $299 for 1,000 |
| Per PR review | $1-1.50 (Bugbot), $15-25 (Claude Code Review) | 2.6 |
| Per seat | $17-72/mo | KaneAI, CodeRabbit, Greptile |
| Mac host per month | €75-€335 (Scaleway), $149-349 (MacStadium), about $898 (AWS mac-m4 always on) | 3.2 |

### 3.2 Mac supply costs

- **Buying:** Mac mini M6 from $899; **M5 Pro from $1,699**; Mac Studio M5 Max $2,499.
  [P] https://www.apple.com/newsroom/2026/09/the-new-mac-mini-and-mac-studio-are-available-today/
- **AWS EC2 Mac:** "per second with a 24-hour minimum allocation period to comply with the
  Apple macOS Software License Agreement".
  - Prices: mac2 (M1) $0.650/h, mac2-m2 $0.878/h, mac2-m2pro $1.56/h. [P]
    https://aws.amazon.com/ec2/instance-types/mac/ and the AWS pricing JSON
    https://b0.p.awsstatic.com/pricing/2.0/meteredUnitMaps/ec2/USD/current/dedicatedhost-ondemand.json
  - mac-m4 $1.23/h and mac-m4pro $1.97/h come from Vantage [S].
    https://instances.vantage.sh/aws/ec2/mac-m4.metal
- **Scaleway** (excluding VAT): M4-S €0.22/h or €149/mo; **M4-M (32 GB) €199/mo**; M4 Pro
  64 GB €335/mo. [P] https://www.scaleway.com/en/pricing/apple-silicon/
- **MacStadium:** M4.M (24 GB) $249/mo, billed monthly. List prices reportedly rose 25-47%
  in 2026 [U]. [P] https://www.macstadium.com/pricing
- **Flow Swiss:** M5 Pro 64 GB CHF 749/mo, 24 h minimum, billed even when powered off. [P]
  https://doc.flow.swiss/platform/pricing/mac-bare-metal
- **OakHost:** M4.M €135/mo, mostly out of stock. [C] https://www.oakhost.com/mac-mini-hosting

**Apple's rules that shape the economics** [P] (re-checked):

- Tahoe SLA https://www.apple.com/legal/sla/docs/macOSTahoe.pdf
- macOS 27 SLA https://www.apple.com/legal/sla/docs/macOS27.pdf

The rules:

1. **At most 2 macOS VMs per Apple host (2B(iii)).** They must be for development,
   testing, macOS Server or personal use.
2. **Leasing is for "Permitted Developer Services"** only, and those include "automated
   testing during software development". A verifier fits.
3. **Minimum lease is 24 consecutive hours.** The **End User Lessee must have "sole and
   exclusive use and control"** of the software and the hardware.
4. **The Lessor must give Apple advance notice** before leasing, through
   developer.apple.com/contact/macos-license/.
5. **Either the lessor or the lessee runs the 2 VMs, not both** (3D). A lessor may
   virtualize only a single instance, as a provisioning tool.
6. **New in macOS 27:** both limits apply "except as otherwise provided in writing ... by
   an authorized representative of Apple".

**Consequence:** a hosted Greenroom cannot pack PRs from customer A and customer B onto the
same Mac on the same day. Each paying customer needs at least one host dedicated to them
for 24-hour blocks. A per-PR price therefore needs a **per-customer daily floor**. Whether
Greenroom (as lessor) or the customer (as lessee) is the party running VMs under 3D is a
legal question to put to counsel together with the Tart question in ADR 0010 [U].

### 3.3 Estimated cost per verified PR

**Assumptions.** These are ours; measure them with the first ten design partners.

- Boot from a warm APFS clone: about 30 s measured (`02-spike.md`, `09-image-strategy.md`).
  Budgeted at 1-2 min including checkout.
- Three PR shapes:
  - **A light:** 1 min boot, 5 min incremental build, 8 min verify = 14 min.
  - **B typical:** 1 + 12 + 15 = 28 min.
  - **C heavy:** 2 min, 25 min cold build, 30 min verify = 57 min.
- **Density: 2 VMs per host** (licence cap).
- **Utilisation of slot-hours:** 40% realistic, 100% best case.
- Owned host: M5 Pro $1,699 over 36 months plus $50/mo for colo, power and bandwidth
  (assumption) = $97/host/mo.
- EUR to USD at 1.08.

**Compute cost per verified PR** (host month / 2 slots / 730 h, divided by utilisation):

| Supply | Host/mo | Slot-hour at 40% | A (14 min) | B (28 min) | C (57 min) | 24 h host floor |
| --- | --- | --- | --- | --- | --- | --- |
| Owned M5 Pro mini | $97 | $0.17 | **$0.04** | **$0.08** | **$0.16** | $3.20/day |
| Scaleway M4-M | $215 | $0.37 | $0.09 | $0.17 | $0.35 | $7.07/day |
| MacStadium M4.M | $249 | $0.43 | $0.10 | $0.20 | $0.41 | $8.19/day |
| AWS mac-m4 on demand | $898 | $1.54 | $0.36 | $0.72 | $1.46 | $29.54/day |

The same PR bought as someone else's minutes:

| Bought as | A | B | C |
| --- | --- | --- | --- |
| GitHub macOS std runner ($0.062/min) | $0.87 | $1.74 | $3.53 |
| GitHub M2 Pro xl ($0.102/min) | $1.43 | $2.86 | $5.81 |
| Codemagic M4 ($0.114/min) | $1.60 | $3.19 | $6.50 |
| Revyl iOS minutes, verify phase only ($0.15/min) | $1.20 | $2.25 | $4.50 |
| Momentic iOS minutes, verify phase only (about $0.28/min) | $2.25 | $4.22 | $8.44 |

Other COGS:

- **The verifier model is the unknown that dominates.** A 15-minute verification with a
  screenshot every step is roughly 30-80 vision turns.
  - Assume **$0.25-3.00 per PR**. The low end is an open model on NIM (ADR 0005, ADR
    0020); the high end is a frontier model.
  - Reference points: Bugbot averages $1.00-1.50 per run (code only), and Claude Code
    Review $15-25 per review (multi-agent, code only).
  - **Measure this first.** It decides gross margin more than Macs do.
- **Evidence storage and egress** (screenshots plus a 15-minute recording at 1-5 MB per
  minute): under $0.02 per PR at object-storage prices (assumption).
- **Re-runs and flake:** assume 1.3 runs per PR delivered.

**Resulting COGS per verified PR:**

- Owned or leased Macs: about **$0.40-4.00**, mostly model.
- AWS: about **$0.75-6.00**.
- Per-customer floor: owned $3.20 a day, leased $7-8 a day. A customer below about 6-16
  PRs a day on its own host pays more in idle Mac than in work, so small customers need a
  plan minimum.

### 3.4 Implications for the pricing model

- Pure per-minute pricing puts Greenroom head to head with CI vendors whose Mac minutes
  cost $0.04-0.12. Customers will benchmark against Xcode Cloud ($0.007/min).
  **Do not sell minutes.**
- Pure per-seat pricing fits badly when agents author the PRs: who is the seat? Revyl and
  TesterArmy both offer unlimited seats. CodeRabbit's "seat = developer who opens PRs"
  is becoming awkward, which is exactly why Bugbot and Copilot moved to usage.
- **Per verified PR with a platform minimum** matches the value unit (a PR a human
  can merge without pulling it down), carries the 24-hour floor, and sits between Bugbot
  ($1-1.50) and Claude Code Review ($15-25). The value side is a reviewer not spending
  15-30 minutes pulling, building and clicking. At an assumed $100/h loaded cost that is
  $25-50 per PR.

---

## 4. Buyer and distribution

### 4.1 Who buys

This is an inference from how the comparables sell. Validate it in section 7.

| Persona | Pain | Budget line | How they buy |
| --- | --- | --- | --- |
| **Mobile/Mac lead or staff engineer** on a team of 5-50 | Agent PRs pile up; nobody can review UI changes without pulling and building | CI/tools budget, a card under about $500/mo | PLG: GitHub App install, free tier, then upgrade. How Revyl, TesterArmy, CodeRabbit and Bitrise start |
| **Eng manager / head of mobile** | Review latency (5x for agent PRs), regressions escaping to TestFlight | Tools budget $10-50k/yr | Trial, then a short sales assist; security review |
| **QA lead** | Coverage, flaky suites, manual regression passes | QA vendor budget: BrowserStack, Sauce, QA Wolf | Enterprise sales; wants test management, not PR evidence |
| **Platform/AI-enablement lead** at a larger company rolling out Cursor, Codex or Copilot agents | Agents cannot build Mac/iOS in their clouds (Copilot excludes macOS; Codex cloud is Linux) | AI tools budget, growing fast | Top-down; buys whatever unblocks agent adoption on Apple platforms |

The first two personas are the wedge. QA leads buy test suites, not PR proof, and the
device clouds own that relationship.

### 4.2 Distribution mechanics on GitHub

- **Check runs** can be created only by GitHub Apps. The output has title, summary and text
  (Markdown, **up to 65,535 characters**), plus these fields:
  - `annotations`: up to **50 per request**; more can be appended with updates.
  - `images[]`: `alt`, `image_url` (a full URL Greenroom hosts) and `caption`.
  - Up to 3 `actions` buttons, for example "Re-verify" or "Take over".
  - `details_url`, a link to the full run and recording.
  - Conclusion `action_required`, useful for "verifier needs a human".
  - [P] https://docs.github.com/en/rest/checks/runs; [S] limit discussion
    https://github.com/github/docs/issues/35252
- **PR comments** can embed images by URL. The attachment upload that produces GitHub-hosted
  URLs is UI-only (10 MB images, video up to 100 MB on paid plans), and no documented REST
  upload exists [U, absence]. Greenroom must host evidence behind signed URLs. For private
  repos that means an auth story for the images, which Revyl sidesteps with a public
  "unauthenticated" recording link. That is a security trade-off to discuss with buyers.
  [P] https://docs.github.com/en/get-started/writing-on-github/working-with-advanced-formatting/attaching-files
- **Merge gating:** a ruleset's "Require status checks to pass" can pin the **source app**,
  so a Greenroom check can be required and cannot be spoofed by another app. Claude Code
  Review deliberately stays neutral and never blocks. Being the blocking check is a
  positioning choice. [P]
  https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets
- **Deployments API:** each VM run can be a transient environment ("greenroom-preview")
  with `environment_url` pointing at the live or recorded session. [P]
  https://docs.github.com/en/rest/deployments/deployments
- **Marketplace:** [P]
  https://docs.github.com/en/apps/github-marketplace/selling-your-app-on-github-marketplace/pricing-plans-for-github-marketplace-apps,
  https://docs.github.com/en/apps/github-marketplace/creating-apps-for-github-marketplace/requirements-for-listing-an-app,
  https://docs.github.com/en/apps/github-marketplace/selling-your-app-on-github-marketplace/receiving-payment-for-app-purchases
  - Plans can be free, flat-rate or per-unit, up to 10 plans, and a trial is fixed at 14
    days.
  - GitHub takes **5%**.
  - A paid listing needs a verified publisher and **at least 100 installations**.
  - The documented plan types (free, flat-rate, per-unit) do not include metered usage.
    Per-unit appears to mean seats or units chosen at purchase [U]. Per-verified-PR
    billing therefore probably runs through our own Stripe, with the Marketplace used for
    discovery and a flat starter tier.
- **Artifact attestations** (Sigstore) are produced from Actions, not Apps. A "signed
  evidence bundle" would need our own signing, or an Actions step (`actions/attest`) [P]
  https://docs.github.com/en/actions/concepts/security/artifact-attestations. Private-repo
  requirements are [U].

### 4.3 Surfaces for coding agents

| Agent | Surface | Mac reality | Opportunity | Source |
| --- | --- | --- | --- | --- |
| Claude Code | Hooks (about 30 events): `Stop`/`SubagentStop` can block with a reason, forcing the agent to keep working; an `http` hook type POSTs to an API; MCP; `claude-code-action@v1` runs on any runner, macOS included | Local Mac and the Desktop simulator pane; no cloud Mac | A Stop hook that calls the verifier gives a "not done until Greenroom passes" loop. Already the v0 plan (ADR 0004) | [P] https://code.claude.com/docs/en/hooks, https://code.claude.com/docs/en/github-actions |
| OpenAI Codex | `openai/codex-action@v1` on Linux and macOS runners; AGENTS.md; MCP | Codex cloud is Linux only. OpenAI owns Tart and reportedly bought many Macs [U] | Verifier as an MCP tool or a PR check. **Highest bundling risk** | [P] https://github.com/openai/codex-action, https://learn.chatgpt.com/docs/cloud |
| GitHub Copilot cloud agent | `copilot-setup-steps.yml`, MCP servers, custom agents; 59-min cap | **macOS runners not supported** | Greenroom as an MCP server or required check gives Copilot agents a Mac they otherwise cannot get | [P] (re-checked) docs URL in section 0 |
| Cursor cloud agents | API (POST /v1/agents, artifacts), 50 MCP servers; v1 webhooks "coming soon"; self-hosted workers | macOS self-hosted workers with computer use; Team Pools need Enterprise | **Greenroom as a hosted macOS worker pool for Cursor.** Cursor's changelog lists Namespace, Daytona, E2B and others as integrations [C] | [P] https://cursor.com/docs/cloud-agent/api/endpoints, https://cursor.com/changelog |
| Devin | API v3 sessions | Runs Mac devboxes on Namespace | Partner or competitor | [P] https://docs.devin.ai/api-reference/overview |
| Jules | Alpha API; GitHub App required | Unknown | Low priority | [P] https://developers.google.com/jules/api |

---

## 5. Risks from platform owners

| Owner | Recent move | Bundling likelihood | Source |
| --- | --- | --- | --- |
| **Apple** | Xcode 26.3 (2026-02-03): Claude Agent and Codex in Xcode; agents "verify their work visually by capturing Xcode Previews and iterating through builds and fixes"; MCP. Xcode 27 (WWDC26): agents with any model, plan mode, parallel subagents, Device Hub. Xcode Cloud got webhooks, **no AI**. The macOS 27 SLA allows written exemptions. | **Medium.** Apple does local visual self-checks. Xcode Cloud plus an agent that posts proof to GitHub would be the kill shot, but Xcode Cloud has moved slowly and sits outside GitHub checks | [P] (re-checked) https://www.apple.com/newsroom/2026/02/xcode-26-point-3-unlocks-the-power-of-agentic-coding/, https://developer.apple.com/xcode/whats-new/ |
| **Google** | Android Studio Agent Mode GA (deploys to emulator, screenshots, adb). **Journeys** runs natural-language UI tests with Gemini vision and a screenshot per step, and runs in CI via Gradle. **Firebase App Testing agent** is free in preview at 200 tests/month/project, Android only | **High for Android.** Google already gives this away. Android should come later or not at all | [P] https://developer.android.com/studio/gemini/journeys, https://firebase.google.com/docs/app-distribution/android/app-testing-agent |
| **GitHub** | Agent HQ (Oct 2025); automatic CodeQL, secret scanning and Copilot review on agent PRs; Playwright screenshots in agent PRs; Copilot usage billing (Jun 2026) | **Medium-high for web, low for Mac today.** GitHub's Mac runners exist but its agent excludes macOS. One changelog could flip this | [P] https://github.blog/news-insights/company-news/welcome-home-agents/, https://github.blog/changelog/2025-10-28-copilot-coding-agent-now-automatically-validates-code-security-and-quality/ |
| **OpenAI** | Owns Tart/Orchard/Cirrus (Apr 2026); Cirrus Runners closed to new customers; Tart relicensed under OpenAI copyright (FSL, "Competing Use"); reportedly bought tens of thousands of Macs [U] | **High.** The most likely vendor to ship "Codex on a cloud Mac with proof". Also controls our current VM engine (ADR 0010) | [P] https://cirruslabs.org/, https://github.com/cirruslabs/tart/blob/main/LICENSE |
| **Anthropic** | Claude Code Desktop iOS Simulator pane (local), autoVerify with browser screenshots, Claude Code Review ($15-25/review, code only); rents Macs via AWS [S] | **Medium.** Evidence is local today; a cloud Mac is plausible | [P] https://code.claude.com/docs/en/desktop-ios-simulator, https://code.claude.com/docs/en/code-review |
| **Cursor** | Cloud agents attach video and screenshots (Feb 2026); self-hosted Mac workers with computer use (Sep 2026); owns Graphite | **High for evidence as a feature.** Cursor lacks Mac supply, which is why it lets customers bring Macs. A partner or a buyer | [P] https://cursor.com/blog/agent-computer-use, https://cursor.com/docs/cloud-agent/self-hosted |
| **Expo** | EAS Simulator (waitlist) with PR session replays | **High for React Native teams** | [P] https://expo.dev/services/simulators |

Structural protection that survives bundling:

1. Apple's 2-VM and 24-hour rules make a Mac fleet slow and costly to build, and nobody
   can skip that with money alone unless Apple grants an exemption (the new macOS 27
   wording).
2. An **independent** verifier is a different trust claim from "the agent says it worked".
   That distinction matters for regulated or careful buyers.
3. Being **multi-agent neutral** (Claude, Codex, Copilot and Cursor PRs verified the same
   way) is something none of the model labs will offer.

None of these is strong on its own.

---

## 6. Synthesis

### 6.1 The most promising wedge customer

**A product company of 10-80 engineers shipping a native Swift app on macOS, or on macOS
plus iOS, with GitHub and private repos, where agents already open a meaningful share of
PRs** (Cursor, Codex, Claude Code or Copilot agents).

Why this profile:

- **Their agents are blind.** Copilot's cloud agent refuses macOS runners, Codex cloud is
  Linux, and Cursor's cloud is Linux unless they run their own Mac workers. The agent
  cannot run the app. Today a human pulls every branch.
- **No direct competitor on macOS.** Revyl, TesterArmy, KaneAI, Momentic and Expo cover iOS
  simulators; none covers a Mac desktop app.
- **Full-Mac verification is genuinely hard.** Windows, menus, TCC permission prompts,
  multiple displays, extensions, drag and drop. Greenroom's image work (`09-image-strategy.md`)
  and human takeover (ADR 0009) are the moat here.
- **They already pay for Mac CI** ($0.06-0.12/min) and so have a budget line.
- **iOS comes along for free:** the same Mac runs the simulator. Greenroom can match Revyl
  on iOS for teams that ship both, instead of leading with iOS against Revyl.

Anti-wedge:

- React Native/Expo teams: Expo and Revyl serve them.
- Android-first teams: Google gives this away.
- Web: every coding agent already does it.
- Large QA organisations: the device clouds own them.

**The honest downside:** Mac-native product teams are a small population (no primary count
exists; Swift is 5.4% of developers, and most of them are iOS). This wedge proves the
product and earns references. It is not the whole business. The expansion path is iOS
teams on the same machines (contested) and "Mac capacity for other people's agents"
(the Cursor self-hosted pool, a Copilot MCP server). That is infrastructure, where
economics and licensing decide the winner.

### 6.2 Pricing hypothesis

Use two delivery modes, both with the same price unit.

**BYO Mac, on the customer's hardware.** No Apple leasing or Tart FSL exposure (a
Permitted Purpose, ADR 0010). This is how v0 already works.

- Free: 25 verified PRs a month, 1 Mac.
- **Team: $149/mo including 100 verified PRs, then $1.50 per verified PR.**
- COGS is only the verifier model, $0.25-3.00.

**Hosted Macs.** Gated on the legal opinion.

- **Pro: $499/mo including 150 verified PRs and 1 dedicated host (2 concurrent VMs),
  then $3 per verified PR. Extra dedicated hosts $249/mo.**
  - The dedicated host covers Apple's exclusive 24-hour lease.
  - $249 per extra host is MacStadium's M4.M retail, so owned hardware at about $97 a
    month leaves margin.
- Enterprise: annual commit, SSO, private networking, custom images and signing.

**Definition of a "verified PR":** a run that ends in a verdict (pass, fail or needs
human) with evidence posted. Infrastructure failures are not billed. Re-verifying the
same head SHA is free.

**Target gross margin:** above 70% at list price. In the typical case B on owned hosts,
COGS is about $0.10 compute plus $0.25-3.00 model, against $1.50-3.00 of revenue. **The
margin fails if the verifier uses a frontier model for every step.** That is the number to
pin down first.

**Sanity checks against the market:**

- $1.50-3 per PR is above Bugbot ($1-1.50) and TesterArmy ($0.30/run).
- It is far below Claude Code Review ($15-25).
- It is about equal to what Revyl charges for 10-20 minutes of iOS simulator.

A team merging 400 agent PRs a month, 60% of them UI-relevant, pays about $149 + 140 x
$1.50 = **$359 a month** in BYO mode, or about $499 + 90 x $3 = **$769 a month** hosted.
That is the price of one CodeRabbit Team seat or three, sitting inside a Mac CI budget.

### 6.3 The five riskiest assumptions

1. **Teams will pay a third party to verify PRs, separately from the agent that wrote
   them.** Cursor, Devin, Copilot and Codex already attach evidence. If buyers see proof
   as a free feature of their coding agent, a standalone verifier becomes a feature, not
   a company. Revyl's small raise and TesterArmy's newness mean the market has not yet
   proven willingness to pay.
2. **The evidence is trustworthy enough to change reviewer behaviour.** If the verifier's
   false-pass rate is visible, reviewers pull the branch anyway and the value is zero.
   The evidence has to cut time-to-merge measurably, not just decorate the PR.
3. **Native Mac (plus iOS) teams are numerous enough and in enough pain.** The wedge is
   picked for being uncontested, which may mean it is small. Mac apps also change UI less
   often per PR than consumer iOS apps.
4. **Hosted Mac economics and licensing work at per-PR prices.** The binding constraints
   are Apple's 24-hour exclusive lease, the 2-VM cap, the Lessor/Lessee split, the need
   to notify Apple, and Tart's FSL under OpenAI. If counsel reads these strictly, hosted
   becomes dedicated-host rental with a verifier on top, and that competes on MacStadium's
   terms.
5. **Setup friction is low enough for PLG.** Xcode projects need signing, secrets,
   simulators, sometimes backend fixtures and test accounts, and a way to tell the
   verifier what "works" means. If onboarding needs a solutions engineer, GitHub
   Marketplace PLG will not work and CAC jumps.

Also watch:

- OpenAI shipping Codex on cloud Macs within 12 months.
- Apple exempting big players from the 2-VM rule.

### 6.4 Validation experiments

Run these before building the hosted fleet. Each has a pass signal fixed in advance.

| # | Tests assumption | Experiment | Who | What to show | Signal that counts |
| --- | --- | --- | --- | --- | --- |
| 1 | 1, 3 | 15 discovery calls. Ask for their last 10 agent PRs touching UI and how each was verified | Mac-native or Mac+iOS product engineers and leads at 10-80 person companies using Cursor, Codex or Claude Code agents. Source from GitHub repos with Swift + `.xcodeproj` + agent-authored PRs (the AIDev dataset gives a list of repos with agent PRs), plus Mac developer communities | Nothing at first, then a 60-second recording of a Greenroom verdict on one of *their* open-source PRs | At least 8 of 15 describe pulling branches to click through as a weekly pain, **and** at least 5 agree to a paid pilot at $149+/mo. Polite interest does not count |
| 2 | 2 | **Shadow verification:** run Greenroom on 50 real historical PRs from 3 open-source Mac/iOS apps with known outcomes (merged, reverted, bug-fix follow-ups). Blind-score the verdicts | Us, then 3 maintainers as judges | Verdict and evidence per PR next to what actually happened | False-pass rate under 5% on PRs later reverted or fixed; maintainers say the evidence would have let them skip pulling the branch on 60% or more of UI PRs |
| 3 | 1, 2 | **Concierge pilot:** 3 design partners, BYO Mac, 4 weeks, Greenroom check on every agent PR | Partners from #1 | A live check run with images, recording and verdict; required-check option | Median time-to-merge on agent UI PRs drops 30% or more against their prior 4 weeks (GitHub API data); at least 2 of 3 convert to paid, and at least 1 turns on the required check |
| 4 | 1 | **Price test:** show three price pages (per seat $30; per verified PR $1.50 with $149 minimum; flat $499) in calls and on a landing page with a waitlist | Same audience plus Cursor/Copilot enterprise AI-enablement leads | Pricing page mock | Which unit they defend to their manager; share choosing per-PR; the maximum acceptable per-PR price cited unprompted |
| 5 | 4 | **Legal and supply check:** counsel reading of SLA section 3 (24 h, exclusivity, 3D Lessor/Lessee split, notice to Apple) and Tart FSL clause 2; ask MacStadium and Scaleway how they comply | Counsel; MacStadium and Scaleway sales | The hosted architecture diagram | A written opinion allowing per-customer dedicated hosts with our VMs. If it says no, hosted pivots to BYO-only or a Virtualization.framework driver |
| 6 | 5 | **Onboarding stopwatch:** 5 teams install the GitHub App and get their first verdict on their own repo without our help | Pilot candidates | Install flow | Median under 30 minutes to first evidence on a real PR. Every step where they got stuck is logged |
| 7 | 1, distribution | **Channel probe:** ask Cursor about listing as a macOS self-hosted pool integration; ask Revyl whether they would resell Mac verification (they delegate proof to Cursor, so they may buy it) | Cursor partnerships; Revyl founders | Demo of a Cursor cloud agent handing off to a Greenroom Mac | A named integration path or a referral agreement. A "we're building it ourselves" answer is also a signal (see risk 1) |

**Kill or pivot criteria to agree before the grilling:**

- If #1 yields fewer than 5 paid-pilot commitments, and #2 shows a false-pass rate above
  10%, the "proof on the PR" SaaS thesis is weak.
- Greenroom would then fall back to its original position (`00-idea.md`): the local
  multi-agent Mac machine for individual developers, or Mac infrastructure sold to other
  agent vendors.

---

## Gaps in this research

- No primary count of active macOS or iOS professional developers, or of Mac App Store
  apps.
- The official BrowserStack App Automate and Perfecto prices could not be fetched.
- KaneAI's credit cost per PR is not published.
- Revyl's revenue and customer count are not public beyond named logos [C].
- Verifier model cost per PR has not been measured. It is the biggest unknown in 3.3.
- Whether the Marketplace can bill metered usage beyond per-unit plans needs a check with
  GitHub partner support [U].
- The macOS 27 SLA exemption wording is recent. It is not known whether any exemptions
  have been granted [U].
