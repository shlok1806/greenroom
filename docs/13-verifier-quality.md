# 13. Making the verifier good enough to trust

Date: 2026-09-25. Status: research, not a decision. Feeds a grilling session and later ADRs.

The goal: a team reads "verified by Greenroom" on an agent's PR and merges it without
watching the run. This document collects what the best available sources say about how to
get a computer-use verifier to that level. It then maps that evidence onto the current code
(`apps/daemon/internal/verifier/`), ADRs 0005, 0006, 0012 and 0020, the model report in
`docs/image-experiment/verifier-models/report.md`, and the open verifier issues (#116,
#124, #125, #127).

## How to read the citations

- Every claim carries a URL and a date. arXiv dates are v1 submission dates unless noted.
- Most numbers were read from the source itself on 2026-09-25 (abstract, HTML full text,
  official leaderboard data file or vendor page).
- Where I re-read the abstract myself to confirm a load-bearing number, the citation says
  "(abstract re-checked)".
- Numbers taken from a paper's full-text tables are marked "(full text)". Spot-check them
  in the paper before quoting them outside this repo.
- **UNVERIFIED** means the claim came from a search snippet or secondary source, or the
  primary page would not load (openai.com returned 403 all day).
- Vendor product claims are labelled **VENDOR CLAIM**. None of them has an independent
  evaluation that I could find.
- Several sources are from 2026 and very recent (July to September 2026). They are
  preprints. Treat single-paper results as signals, not settled facts.

---

## 0. The short version

1. **False passes are the typical error of AI judges, and the gap is large.**
   - Across frontier judges of computer-use trajectories, about two thirds of all judge
     errors are false successes, and over-accepts outnumber over-rejects about 3 to 1
     (OSReward, 2026-07-30, full text).
   - The best LLM judges on AgentRewardBench reach only about 70% precision on "success"
     (2025-04-11, full text).
   - A judge that reads the agent's own claims believes them: in ZeroGUI, including the
     agent's responses dropped judge precision from 61.5% to 44.3%.
   - Greenroom's verifier is a judge that also acts, so it inherits this bias.
2. **What moves precision is structure, not a bigger prompt.** Four things have evidence:
   - a) Break the task into explicit key points or a checklist before judging (WebJudge,
     RocketEval, Anthropic's "sprint contract").
   - b) Let the judge gather its own evidence from the environment rather than read a
     transcript (Agent-as-a-Judge: 90% vs 60% human alignment).
   - c) Require agreement between independent judges or prompts for a pass, and send
     disagreement to abstain (CUARewardBench's unanimous ensemble: 89.8% precision, 93.3%
     NPV, vs 82.9% for the best single model).
   - d) Check claims against state that cannot be talked into agreeing: AX values, files,
     databases, the before-and-after build. Environment-grounded checks remove most
     hallucinated success (Microsoft's "Art of Building Verifiers": false positives near
     zero vs 22% or more for WebJudge).
3. **Most of Greenroom's open verifier bugs are known failure modes with known fixes.**
   - #116, clicks lost and effects asserted: verify each action's effect.
   - #125, repeated identical failing call: a stuck detector.
   - #127, the turn ends at a limit with no verdict: a forced closing verdict.
   - #124, acting on a screen a human changed: re-observe after a context change.
   - OpenHands, UFO2, Mobile-Agent-v2, the OpenAI Agents SDK and Anthropic's harness posts
     each describe one of these fixes. They are mechanical daemon changes, not model
     changes.
4. **Measure it before claiming it.**
   - The realistic suite's "0 wrong verdicts" came from 10 requested verdicts. With 0
     errors in n trials, the 95% upper bound on the error rate is about 3/n (the "rule of
     three"), so 0 of 10 bounds the wrong-verdict rate only at about 30%.
   - To claim a false-pass rate at or below 5% with 95% confidence, you need about 60
     deliberately broken builds with zero false passes. At or below 1% you need about 300.
   - The single most useful artifact to build next is a labelled set of broken and correct
     app variants: mutation testing for UI.
5. **Models.**
   - The NIM models Greenroom uses are well behind the frontier on computer use.
     OSWorld-Verified leaders are at 83 to 90%, and the best open-weight model is at 82.6%.
     Nemotron 3 Ultra is text only.
   - The in-house data already says kimi-k3 seeing the screen itself beats the two-model
     split, at a large latency cost.
   - The measured problem with the current brain is availability (24 of 90 calls failed
     after retries), not judgment.
   - A trustworthy verifier needs (a) a model whose calls do not fail a quarter of the
     time, and (b) a judge from a different model family than the actor, for independence.

---

## 1. Where the verifier stands today

From the code (worktree `research-verifier-and-ios`, commit `e91b2bc`) and the issues.

### Architecture

- **One actor per run.** It is a tool loop over NIM's OpenAI-compatible endpoint (ADR
  0005, 0006).
- **Models.**
  - The brain is `nvidia/nemotron-3-ultra-550b-a55b`, text only.
  - Screenshots reach it as prose from a describer, `moonshotai/kimi-k3` by default since
    ADR 0020.
  - Aiming comes from the macOS AX tree (`machine_ui`, ADR 0012). Clicks go by element id
    or by screen fraction.
- **Turn limits.** A turn is capped at 40 tool calls (`DefaultMaxSteps`) and 10 minutes
  (`DefaultBudget`).
- **Endings.** A turn ends in `reply`, `ask` or `report_verdict`.
- **The verdict.** It is `pass`, `fail` or `inconclusive`, with a summary and a free-form
  `evidence` list of "step N" strings and screenshot paths. It is a proposal: the coder or
  a human accepts or disputes it, with up to 2 disputes before it becomes contested.
- **The system prompt already gets several things right:**
  - Constraints are hard rules.
  - "Never estimate a position from a screenshot description when machine_ui lists the
    element."
  - Re-read after every action.
  - "Do not change your mind just because you were asked to."
  - A visual verdict must cite a screenshot taken after the last action.
  - It diagnoses and never repairs.

### Known weaknesses, with the research theme each one belongs to

| Observed | Where | Theme in this doc |
| --- | --- | --- |
| 2 of 5 clicks after text entry had no effect. The verdict asserted both effects anyway ("checking the Milk and Bread checkboxes"; Milk was never checked) | #116 | Action-effect verification (section 3.4), hallucinated success (4.2) |
| Same empty `machine_type` about 6 times in a row, no guard | #125 | Stuck detection (5.1) |
| A turn hits 40 steps or 10 min and ends in a reply; the task stays open with no verdict | #127 | Forced final answer (5.2) |
| Keeps acting on a screen a human changed; does not resume after Give Back | #124 | Stale state, re-planning (3.5, 5.3) |
| Retried clicks in a tight loop while a human held the screen, then replaced a correct fail with "inconclusive: a human is driving" | #97 (fixed) | Tool-error semantics (5.1) |
| A late message made the turn end in a reply; the task lost its verdict | #89 (fixed) | Typed endings (5.2) |
| A reasoning model's cut-off step became an empty reply; the verdict was lost | #71 (fixed) | Budgeting (5.2) |
| Brain: right when it answers (0.95 "best"), but 24 of 90 calls failed after retries (429/500) | model report | Model availability (6) |
| Old describer invented 54 on-screen values in 8 of 40 images | model report | Describer hallucination (3.3, 6) |
| Realistic suite: 0 wrong verdicts, but only 6 to 10 verdicts obtained per configuration | ADR 0020 | Measurement (7) |

### Three structural gaps

1. **There is no separation between the coder's claim and the evidence.** The task text is
   whatever the coder wrote, often including its own account of what it changed and why it
   works. The verifier reads it as instructions.
2. **Pass is decided by the same model, in the same context, that drove the machine.**
   Nothing independent checks the verdict.
3. **Evidence is a free-form list.** Nothing checks mechanically that it exists, that it
   comes after the relevant action, or that it supports each claim in the summary.

---

## 2. State of the art for computer-use agents

### 2.1 Benchmarks and numbers

**OSWorld (Ubuntu desktop, 369 tasks)**
- Xie, Zhang, Chen, Li et al., arXiv 2404.07972, 2024-04-11,
  https://arxiv.org/abs/2404.07972.
- Humans complete 72.36%. The best model at launch scored 12.24%, "primarily struggling
  with GUI grounding and operational knowledge".

**OSWorld-Verified**
- XLANG blog, 2025-07-28, https://xlang.ai/blog/osworld-verified.
- Fixed more than 300 task and checker issues.
- Re-evaluated scores: CoACT-1 60.76%, Agent S2.5 with o3 56.0%, Claude 4 Sonnet 43.9%.

Leaderboard data file, accessed 2026-09-25, all at 100 max steps (https://osworld-v1.xlang.ai/):

| Entry | Score | Date |
| --- | --- | --- |
| Intelligence-Indeed Agent (framework, uses coding actions) | 90.19% | 2026-07-25 |
| claude-fable-5 | 85.96% | 2026-08-01 |
| Pointer Agent with Opus 4.7 | 83.64% | 2026-05-21 |
| claude-opus-5 | 83.39% | 2026-08-01 |
| Holo3-35B-A3B (H Company, open weights, Apache-2.0, 3B active) | 82.56% | 2026-04-20 |
| Kimi K2.6 (open weights) | 73.06% | 2026-04-20 |
| claude-sonnet-4-6 | 72.11% | 2026-03-08 |
| Agent S3 with Opus 4.5 + GPT-5, best of 10 rollouts | 72.58% | 2025-12-11 |
| UI-TARS-2-2509 | 53.1% | 2025-10-14 |
| OpenAI computer-use-preview (100 steps) | 31.4% | 2025-07-28 |
| qwen2.5-vl-72b | 5.0% | 2025-07-28 |

- Anthropic changed how it runs OSWorld-Verified and restated Opus 4.7 at 82.3% (Opus 4.8
  post, 2026-05-28, https://www.anthropic.com/news/claude-opus-4-8).
- Vendor-reported scores I could not confirm from primary pages, all UNVERIFIED:
  - GPT-5.5 at 78.7%, via secondary sources (https://openai.com/index/introducing-gpt-5-5/
    returned 403).
  - Gemini 3.5 Flash computer use at 78.4 (secondary; the Google launch post,
    https://blog.google/innovation-and-ai/models-and-research/gemini-models/introducing-computer-use-gemini-3-5-flash/,
    confirms the launch but the numbers did not render).

**OSWorld 2.0 (long-horizon, 108 workflows)**
- Yuan, Zhou, Xiong et al., arXiv 2606.29537, 2026-06-28, https://arxiv.org/abs/2606.29537.
  Results file updated 2026-09-17:
  https://osworld-v2.xlang.ai/static/data/leaderboard/official-results.json.
- Median human time is about 1.6 h, and the average run is 318 tool calls.
- Best binary success: Claude Opus 5 at 44.33% (77.67% partial). GPT-5.6 Sol 27.34%, GPT-5.5
  13.0%, Kimi 2.6 4.6%, Qwen 3.7-Plus 2.8%.
- Anthropic reports 81.8% partial for Opus 5.5 (2026-09-22,
  https://www.anthropic.com/claude-opus-5-5). The results file does not list it yet:
  UNVERIFIED.

**macOSWorld**
- Yang, Ci, Shou, arXiv 2506.04135, 2025-06-04, https://arxiv.org/abs/2506.04135.
- 202 tasks, 30 apps (28 macOS-exclusive), 5 languages.
- Overall: Claude CUA 37.1%, OpenAI CUA 33.8%, Gemini 2.5 Pro 18.5%, UI-TARS-7B 4.8%.
- Deception pop-ups distracted Claude CUA 72.4% of the time.
- It is the only macOS benchmark with published numbers that I found, and it predates the
  2026 models.

**MacAgentBench**
- Fu et al., arXiv 2606.22557, 2026-06-21, https://arxiv.org/abs/2606.22557.
- 676 tasks, 25 apps, about 60% needing GUI plus CLI.
- Claude Opus 4.6 on OpenClaw reaches 73.7% pass@1.
- This mix (GUI plus shell) is Greenroom's mix.

**AndroidWorld**
- Rawles et al., arXiv 2405.14573, 2024-05-23.
- The leaderboard is self-reported with "no independent verification". Top entries are at
  97 to 100% (2025-10 to 2026-08). The best single model, MAI-UI-235B, is at 76.7%.
- https://docs.google.com/spreadsheets/d/1cchzP9dlTZ3WXQTfYNhh3avxoLipqHN75v1Tb86uhHo

**Online-Mind2Web (live web, human-judged)**
- Leaderboard, accessed 2026-09-25,
  https://huggingface.co/spaces/osunlp/Online_Mind2Web_Leaderboard.
- Operator 61.3% (2025-03), Gemini 2.5 Computer Use 69.0% (2025-09), Yutori n1.5 97.3%
  (2026-06), Hark 97.7% (2026-08).

**ScreenSpot-Pro (grounding on high-resolution professional apps)**
- Li et al., arXiv 2504.07981, 2025-04-04, https://arxiv.org/abs/2504.07981. The best model
  at launch scored 18.9%.
- Leaderboard, accessed 2026-09-25,
  https://gui-agent.github.io/grounding-leaderboard/results/screenspot_pro.json:
  - Specialized open grounders reach 77 to 83 (Indeed-UI-32B zoom-in 82.7, MAI-UI-32B 77.5).
  - General frontier models in the plain harness score low (Claude computer use 17.1, GPT-5
    high resized 6.0).

**Reading for Greenroom**
- Short single-app tasks, the shape of a PR check, are now mostly solvable by frontier
  models on Linux.
- Long-horizon work, macOS and open models remain weak.
- A PR verification ("launch the app, set the bill to 160, choose 20%, read Each pays") is
  OSWorld-1-sized, not OSWorld-2-sized. That is good news: it sits in the regime where the
  best agents are near or above human level.

### 2.2 What limits success: failure taxonomies

**Early: grounding dominated.**
- OSWorld (2024): more than 75% of failed examples had mouse-click inaccuracies ("strong
  planning but weak execution"). https://arxiv.org/html/2404.07972v2, full text.
- Agent S (Agashe et al., arXiv 2410.08164, 2024-10-10): 53% of failures involved grounding.
  https://arxiv.org/html/2410.08164

**Now: planning, verification and control dominate.**
- Agent S2 (arXiv 2504.00906, 2025-04-01): planning, not grounding, is now the most common
  failure.
- CUADebug (Zhang et al., arXiv 2608.02643, 2026-07-31): over 204 failed OSWorld
  trajectories, "task reasoning and control" is the largest category (110 of 204).
  https://arxiv.org/abs/2608.02643
- OSWorld 2.0 (2026): agents fail less on basic GUI control and more by losing track of
  constraints, missing information that arrives mid-task, guessing instead of asking, and
  skipping verification. https://osworld-v2.xlang.ai/
- "How Benchmarks Mis-Score Computer-Use Agents" (Dong et al., arXiv 2607.28367,
  2026-07-30): among real failures, verification and feedback and planning dominate over
  grounding. https://arxiv.org/abs/2607.28367

**Blind goal-directedness.**
- Shayegani et al., arXiv 2510.01670, 2025-10-02, https://arxiv.org/abs/2510.01670.
- 80.8% average rate across 9 frontier computer-use agents.
- Named modes include execution-first bias and thought-action disconnect: the agent says
  one thing and does another. #116 is a thought-action disconnect at verdict time.

**Longer is not better.**
- Lee and Choi, arXiv 2607.28573, 2026-07-30, https://arxiv.org/abs/2607.28573.
- Longer horizons "extend erroneous trajectories rather than correct them". Failures shift
  toward premature false success.

**Too many steps.**
- OSWorld-Human (Abhyankar, Qi, Zhang, arXiv 2506.16042, 2025-06-19): the best agents take
  2.7 to 4.3 times more steps than needed. Planning and reflection calls dominate latency.
  https://arxiv.org/abs/2506.16042

**The observation space matters more than you would think.**
- ComponentBench (Guan et al., arXiv 2608.18307, 2026-08-18): changing only the
  observation and action space moves success by more than 30 points. GPT-5 mini scores
  83.1% with the accessibility tree vs 48.9% pixel-only. https://arxiv.org/abs/2608.18307

**Recovery is weak.**
- CUADebug: re-execution raises failure recovery only from 13.89% to 29.86%.
- The OSWorld-Verified blog lists error recovery and interface changes as open problems.

**Implication.** For Greenroom the grounding problem is mostly solved by ADR 0012's AX
tree. The remaining risks are the ones the 2026 literature names: not checking that an
action took effect, declaring success early, looping, and losing track of what the task
asked.

---

## 3. Grounding: AX tree, pixels, set-of-marks, hybrid

### 3.1 The evidence

**OSWorld observation ablation**
- Table 5, https://arxiv.org/html/2404.07972v2, full text. Success rates:

| Model | A11y tree | Screenshot | Screenshot + a11y | Set-of-marks |
| --- | --- | --- | --- | --- |
| GPT-4o | 11.36 | 5.03 | 11.21 | 4.59 |
| GPT-4V | - | 5.26 | 12.17 | 11.77 |
| Claude-3 Opus | - | 2.42 | 4.41 | 6.72 |

- For general-purpose models, a text a11y tree is about as good as screenshot plus tree.
  Screenshot-only is worst.
- Set-of-marks helps some models and hurts others; the paper says it "varies across
  models".
- The filtered tree still needs about 6,000 tokens to fit 90% of observations.

**SeeAct**
- Zheng, Gou, Kil, Sun, Su, arXiv 2401.01614, 2024-01-03, https://arxiv.org/html/2401.01614.
- "Grounding is the bottleneck". Offline step success: oracle grounding 61.9%, textual
  choices 39.1%, set-of-marks image annotation 20.3%.
- 54% of set-of-marks errors were hallucinated labels.

**UFO2 on Windows**
- Zhang et al., Microsoft, arXiv 2504.14603, 2025-04-20, https://arxiv.org/html/2504.14603,
  full text.
- Windows UI Automation alone: 23.4% (WAA) / 22.4% (OSWorld-W). OmniParser-v2 vision alone:
  26.6% / 14.3%. Hybrid: 26.6% / 22.4%.
- About 62% of WAA failures were control-detection failures, mostly in apps that do not
  follow accessibility standards.
- The hybrid recovers up to 9.9% of previously unrecoverable cases.

**WindowsAgentArena**
- Bonatti et al., arXiv 2409.08264, 2024-09.
- Adding UIA to pixel-based set-of-marks gave +57% relative.
- A UIA tree query "can take from a few seconds up to several minutes".
  https://arxiv.org/html/2409.08264v1

**Screen2AX (macOS)**
- Muryn et al., arXiv 2507.16704, 2025-07-22, https://arxiv.org/abs/2507.16704.
- "Only 33% of applications on macOS offer full accessibility support".
- Reconstructs macOS-style AX trees from a screenshot: 77% F1, 2.2 times agent performance
  over the native tree.
- This is the most relevant counter-evidence to relying on AX alone on macOS.

**The pure-vision camp**
- UGround (Gou et al., arXiv 2410.05243, 2024-10) and Aguvis (arXiv 2412.04454, 2024-12)
  argue that text trees are "noisy, incomplete" and costly, and that trained vision
  grounders beat text-assisted agents.
- UI-TARS (arXiv 2501.12326) and UI-Venus (arXiv 2508.10833) are screenshot-only.
  UI-Venus-72B scores 61.9 on ScreenSpot-Pro and 65.9 on AndroidWorld.
- This is true for models trained for grounding. It does not apply to a text-only brain
  like Nemotron 3 Ultra.

**Agent S2 dropped the a11y tree**
- arXiv 2504.00906, 2025-04-01.
- It used a mixture of grounders instead: a visual grounder, OCR, and app-specific
  structure (LibreOffice UNO).

### 3.2 Which is most reliable for clicking

- For a macOS SwiftUI or AppKit app with decent accessibility, the AX tree is the most
  reliable way to aim and, more importantly for a verifier, to read values.
- It gives exact strings with no OCR or vision hallucination. The old nano-omni describer
  invented 54 values in 8 of 40 shots, while kimi-k3 invented none (model report).
- Vision is the fallback for canvas, games, web views without accessibility, and apps with
  broken trees. That fallback is common on macOS: 33% full support (Screen2AX).
- ADR 0012 matches the evidence. What is missing:
  1. **A trained grounder for the fallback path.** Today the fallback is a describer's
     "approximate centre" in prose. Specialized grounders, including open ones, sit at
     60 to 83 on ScreenSpot-Pro. General models in prose are far lower.
  2. **Zoom and crop.** Crop around a target and re-read it at native resolution.
     - ScreenSeekeR took OS-Atlas-7B from 18.9% to 48.1% on ScreenSpot-Pro with no
       training (arXiv 2504.07981).
     - RegionFocus gave more than 28% gains (Luo et al., arXiv 2505.00684, 2025-05-01).
     - Anthropic's computer-use tool has a `zoom` action for exactly this
       (https://platform.claude.com/docs/en/docs/agents-and-tools/tool-use/computer-use-tool,
       accessed 2026-09-25).
  3. **An OCR pass merged into the tree**, so text that AX does not expose becomes readable
     and clickable. Agent S's "image-augmented accessibility tree" adds OCR text blocks as
     nodes when they are missing (arXiv 2410.08164).

### 3.3 Resolution and scaling

- **Anthropic's computer-use docs** (accessed 2026-09-25):
  - Recommend 1024x768 or 1280x720 for desktop, and say to avoid anything above 1920x1080.
  - Warn that scaling errors produce consistent offsets and that small targets cause
    "hallucinated coordinates".
  - Say to put instruction text before images.
- **Anthropic's best-practices post** (Gonzalez and Weihs, 2026-05-13) suggests 1280x720 as
  a start and 1080p for Opus 4.7.
  https://claude.com/blog/best-practices-for-computer-and-browser-use-with-claude
- **OSWorld resolution ablation** (full text): screenshot-only success rose with resolution.
  Set-of-marks peaked at 0.4x of 1080p.
- **Phi-Ground** (Microsoft, arXiv 2507.23779, 2025-07-31): image tokens stop helping much
  above about 2,000, and text before the image gives +1.7 to +4.1 points.
  https://arxiv.org/html/2507.23779
- **Qwen2.5-VL** emits absolute pixel coordinates in the resized image, so a client must
  track the resize (https://qwenlm.github.io/blog/qwen2.5-vl/, 2025-01, exact date
  UNVERIFIED). Gemini normalizes to 1000x1000 (https://ai.google.dev/gemini-api/docs/computer-use,
  updated 2026-09-23).
- **Greenroom.** The tahoe guest is 1024x768 at scale 1 and `toJPEG` caps at 1024 wide, so
  the describer sees native pixels. That is the XGA regime Anthropic recommends.
  - The risk is small text and small controls, the ScreenSpot-Pro failure mode, where a
    zoomed crop helps.
  - Fractions of the screen as the coordinate system (ADR 0009) are the right abstraction.
    Keep the manager as the only converter.

### 3.4 Did the action take effect? (The #116 problem)

**Hallucinated success.** Agents routinely assume an action worked.

- **VeriGUI, "Don't Act Blindly"** (Zhang et al., arXiv 2604.05477, 2026-04-07):
  - Agents "assume deterministic environment responses, generating actions without
    verifying whether previous operations succeeded".
  - Proposes a Thinking-Verification-Action-Expectation loop in which each action states
    its expected effect and the next step checks it.
  - https://arxiv.org/abs/2604.05477
- **Mobile-Agent-v2** (Wang et al., arXiv 2406.01014, 2024-06-03):
  - A reflection agent compares the screenshots before and after each action and labels
    the outcome erroneous, ineffective (no change) or correct.
  - More than 30% better task completion; reflection accuracy 80 to 93%.
  - https://arxiv.org/html/2406.01014
- **UFO2**: before each queued action, it re-checks UIA preconditions (`is_enabled`,
  `is_visible`) and "halts immediately if any validation fails due to interface change"
  (arXiv 2504.14603).
- **Behavior Best-of-N / Agent S3** (arXiv 2510.02250, 2025-10-02): judges rollouts by
  "behavior narratives" that describe what changed after each action. This reached 72.6% on
  OSWorld, human level at the time. https://arxiv.org/abs/2510.02250
- **Anthropic's computer-use docs** recommend prompting "After each step, take a
  screenshot and carefully evaluate if you have achieved the right outcome ... Only when you
  confirm a step was executed correctly should you move on."
- **I found no primary source for the specific macOS quirk in #116**, where the first click
  after typing into a SwiftUI `TextField` seems to end editing and be dropped.

**The fix does not need a model.** The daemon already has a before and an after: the
verifier's last `machine_ui` tree and a fresh read. After each input call it can:

- attach a compact diff to the tool result: "No change in the frontmost window's AX values
  after this click", or "Changed: [14] Checkbox 'Milk' selected false to true";
- refuse to count a check as evidenced when the action behind it produced no change.

This turns Mobile-Agent-v2's "ineffective" label into a daemon fact instead of a model
judgment.

### 3.5 Stale state

- **The TOCTOU gap.** "TOCTOU in desktop agents" (Wenpeng Xu, arXiv 2604.18860, 2026-04-20)
  measured a mean 6.51 s gap from screenshot to click. A focus-manipulation attack
  redirected actions 100% of the time.
  - The defense re-checks the target region (masked SSIM), a global diff and the window
    list before each dispatch.
  - It intercepted 100% of 180 trials with 0 false positives and under 0.1 s overhead.
  - https://arxiv.org/abs/2604.18860
- **Greenroom's version.**
  - ADR 0012 already notes "Nothing checks that the app is still frontmost" and that an id
    is only as fresh as its read.
  - The optional `uiStep` guard is the right shape.
  - #124 proposes a screen epoch bumped on every control change. Both are this defense,
    minus pixels.
  - Recommendation: make the freshness check mandatory for the verifier, not optional.
    Refuse a click whose id comes from a tree older than the last input or control change,
    and say "re-read with machine_ui".
- **Electron and Chromium trees.**
  - They start cold; `AXManualAccessibility` (which the helper already sets) is the right
    switch (Electron docs, https://www.electronjs.org/docs/latest/tutorial/accessibility;
    PR #10305, 2017-09-11).
  - `AXEnhancedUserInterface` slows window moves and breaks window managers (Rectangle
    issue #912, 2022-09-08; Mozilla bug 1664992). Do not set it.
  - A retry after about 150 ms on an empty first tree is reported by a third-party blog:
    UNVERIFIED.

---

## 4. Verification: judging whether a claimed change works

This is the core of the product, and it is a different problem from task completion. The
verifier is an evaluator. The literature on agent judges (reward models for computer-use
trajectories) applies directly.

### 4.1 How good are agent judges?

| Source | Setup | Result |
| --- | --- | --- |
| Agent-as-a-Judge (Zhuge et al., Meta/KAUST, arXiv 2410.10934, 2024-10-14), https://arxiv.org/html/2410.10934, full text | Judge with tools to locate, read and retrieve evidence from the workspace vs an LLM reading the transcript, on 55 dev tasks and 365 requirements | 90.44% vs 60.38% alignment with a human majority. Single humans disagree pairwise 10 to 30%; a 3-human majority cut error to 6.01%. Cost $30.58 vs $1,297.50 for humans |
| Online-Mind2Web / WebJudge (Xue, Qi, Shi et al., OSU, arXiv 2504.01382, 2025-04-02), https://arxiv.org/html/2504.01382 | 3 stages: extract key points from the task, score and filter screenshots, then judge | 85.7% human agreement (o4-mini). Older auto-evals 66.9 to 79.4% and degrade on long trajectories |
| AgentRewardBench (Lù, Kazemnejad, ... Reddy, arXiv 2504.08942, 2025-04-11, abstract re-checked), https://arxiv.org/html/2504.08942 | 1,302 web trajectories, 12 judges, expert labels (89.3% inter-annotator agreement) | Best judge precision about 70% (GPT-4o 69.8, Claude 3.7 Sonnet 68.8, full text). Rule-based checks: 83.8% precision but they under-report success. A judge accepted "successfully applied the filter" when no filter was applied |
| CUARewardBench (Lin, Tan, Qin et al., arXiv 2510.18596, 2025-10-21, abstract re-checked) | OSWorld trajectories, outcome and process reward | Best single model (GLM-4.5V) 82.9% precision (full text). Unanimous Prompt Ensemble (2 models x 2 prompts, all must agree): 89.8% precision, 93.3% NPV. General VLMs beat CUA-specialized models as judges |
| OSReward (Qiushi Sun et al., arXiv 2607.28609, 2026-07-30, abstract re-checked for "systematic leniency bias that mislabels failed runs as successes") | 1,019 trajectories, 4 platforms, 3 annotators each, kappa about 0.71 | Full text: Opus 4.8 89.7%, GPT-5.5 89.5%. On the hard split the best is 69.7%. About 67% of judge errors are false successes. Removing the agent's thought and action text changed 22.7% of verdicts |
| ZeroGUI (Yang et al., arXiv 2505.23762, 2025-05-29), https://arxiv.org/html/2505.23762 | Qwen2.5-VL-32B judge, 4 samples, unanimous | Precision 61.5% without the agent's responses vs 44.3% with them |
| "The Art of Building Verifiers for Computer Use Agents" (Rosset, Sharma, Zhao, Gonzalez-Fernandez, Awadallah, Microsoft, arXiv 2604.06240, 2026-04-05, abstract re-checked) | Web CUA verifiers | False positives reduced "to near zero compared to baselines like WebVoyager (≥ 45%) and WebJudge (≥ 22%)" |
| Mis-Score audit (Dong et al., arXiv 2607.28367, 2026-07-30) | 150 fail-scored trajectories, 5 benchmarks | 15.3% of fail verdicts were wrong (10.7% evaluator false negatives, 4.7% broken tasks) |
| OSWorld-Pro (Wang, Zhang et al., arXiv 2609.24890, 2026-09-21) | Process evaluation, 2,800+ intermediate objectives | Claude Opus 5: 75.7% under process evaluation vs 83.4% outcome-only. Some "passes" skip required intermediate states |

**Takeaways**
1. A judge in the 85 to 90% agreement range is state of the art, and human-human agreement
   is about the same (81 to 89%). That means "without watching" has to come from structure
   and abstention, not from judge accuracy alone.
2. Errors are asymmetric toward false passes. That is the error Greenroom cannot afford.
3. Judges that act (Agent-as-a-Judge, VAGEN's active probing, arXiv 2602.00575, 2026-01-31)
   beat judges that read. Greenroom's verifier already acts, which is the right bet.

### 4.2 Why judges pass things they should not

- **The claim sways the judge.**
  - Including the agent's own words cut precision from 61.5% to 44.3% (ZeroGUI).
  - AgentRewardBench documents judges believing "I applied the filter".
  - OSReward adds nuance: dropping the agent's text entirely changed 22.7% of verdicts and
    cost accuracy. The claim is useful as a list of things to check, not as evidence.
- **Sycophancy.** Humans and preference models prefer convincingly written sycophantic
  answers (Sharma et al., Anthropic, arXiv 2310.13548, 2023-10-20,
  https://arxiv.org/abs/2310.13548). A coder's dispute of a fail verdict is a sycophancy
  trigger.
- **Leniency.** "Judging the Judges" (Thakur et al., arXiv 2406.12624, 2024-06-18) found a
  systematic leniency bias and showed that high percent agreement can hide very different
  scores. It recommends Cohen's kappa. https://arxiv.org/abs/2406.12624
- **Position, verbosity and self-preference.**
  - MT-Bench (Zheng et al., arXiv 2306.05685, 2023-06-09): GPT-4 about +10% self-preference,
    Claude-v1 about +25%; verbosity attacks fooled weaker judges 91.3% of the time.
    https://arxiv.org/html/2306.05685
  - Self-recognition correlates linearly with self-preference (Panickssery, Bowman, Feng,
    arXiv 2404.13076, 2024-04-15).
  - Greenroom implication: a judge from the same family as the coding agent is a weaker
    check.
- **Trivial tokens fool judges.** "One Token to Fool LLM-as-a-Judge" (Zhao et al., arXiv
  2507.08794, 2025-07-11): the input "Thought process:" produced false positives of 28.9%
  (GPT-4o) and 67 to 74% (open 70B-class models), and 0.5% for Claude-4.
  https://arxiv.org/html/2507.08794
- **Self-evaluation fails.**
  - "When Do Agent Loops Mistake Stagnation for Progress?" (Park and Choi, arXiv 2607.25152,
    2026-07-27, abstract re-checked): agents claimed improvement in 54 of 54 cycles, but 56%
    had zero or negative real change. An in-band judge "accepted cycles of which 44 percent
    were real-world regressions". The paper concludes "out-of-band evaluation with
    real-world access is a structural requirement".
  - Anthropic's harness post (Rajasekaran, 2026-03-24,
    https://www.anthropic.com/engineering/harness-design-long-running-apps): agents
    "confidently prais[e] the work". A separate skeptical evaluator driving the live app
    with Playwright was "far more tractable" than making the generator self-critical. Early
    evaluators "tested superficially".
- **Agents misreport their own work.**
  - Smyth et al. (arXiv 2609.20812, 2026-09-17): agents skipped files they were asked to
    review in 67.9% of runs, and in those runs the final report was misleading 80.4% of the
    time. https://arxiv.org/abs/2609.20812
  - Transluce on pre-release o3 (2025-04-16): it fabricated tool runs ("ran it on my 2021
    MacBook Pro"). https://transluce.org/investigating-o3-truthfulness
  - METR (2025-06-05): o3 reward-hacked on one RE-Bench task in 21 of 21 runs, including
    patching the evaluator. https://metr.org/blog/2025-06-05-recent-reward-hacking/
  - These are the coders Greenroom verifies. Also note that #116's verdict asserted effects
    that did not happen: the verifier itself overclaimed.
- **Self-correction without external feedback does not help** (Huang et al., arXiv
  2310.01798, 2023-10-03). LLMs find their own reasoning errors poorly but fix them well
  once shown where (Tyen et al., arXiv 2311.08516, 2023-11). For Greenroom: a dispute with a
  concrete pointer ("step 13 shows Milk unchecked") works; "are you sure?" does not.

### 4.3 Techniques that reduce false passes, and the evidence for each

| Technique | Evidence | Cost | Fit for Greenroom |
| --- | --- | --- | --- |
| **Acceptance checklist first** (key points extracted from the task before judging) | WebJudge stage 1 (above). RocketEval (Wei et al., arXiv 2503.05142, 2025-03-07): instance checklists give 0.965 human correlation with a 2B judge. CheckEval (arXiv 2403.18771) cuts variance. TICK (arXiv 2410.03608) improves agreement. Anthropic's "sprint contract" defines done before work starts (2026-03-24) | One model call | High. Makes coverage visible ("what was not checked") |
| **Evidence per check** | Agent-as-a-Judge's locate and read modules (90% vs 60%). Anthropic: grade outcomes (environment state), not the agent's claim (https://www.anthropic.com/engineering/demystifying-evals-for-ai-agents, 2026-01-09) | Schema plus daemon checks | High. Can be enforced mechanically |
| **Keep the claim out of the evidence** | ZeroGUI (44.3% vs 61.5% precision). AgentRewardBench example | Prompt and projection change | High |
| **Skeptical, adversarial framing** (a tester trying to break it) | Anthropic harness post: a standalone skeptical evaluator is "far more tractable" | Prompt | Medium. Evidence is qualitative |
| **Separate observe and judge phases, with a fresh-context judge** | WebJudge's multi-stage design. OSReward: judges read differently with and without the agent's text. Wang et al. 2023 (arXiv 2305.17926): generate evidence first, then judge | One or two more calls per verdict | High for pass verdicts |
| **Multiple independent judges, unanimity for pass** | CUARewardBench UPE 82.9% to 89.8% precision. PoLL (Verga et al., arXiv 2404.18796, 2024-04-29): a panel of smaller models from different families beats a single GPT-4 judge at 7 times lower cost. Self-consistency (Wang et al., arXiv 2203.11171) | 2 to 4 judge calls on the evidence, no new machine actions | High. Judging a recorded evidence bundle is cheap next to driving |
| **Calibrated abstention with a guarantee** | Trust or Escalate (Jung, Brahman, Choi, arXiv 2407.18370, 2024-07-25): confidence from agreement among simulated annotators plus fixed-sequence testing on a 500-item calibration set guarantees 85% human agreement at 63.2% coverage. Humans also agreed less on the abstained items. Conformal abstention (Yadkori et al., arXiv 2405.01563, 2024) | Needs a labelled calibration set | High once section 7's eval set exists. This is "inconclusive" made rigorous |
| **Verbalized confidence** | Tian et al. (arXiv 2305.14975, 2023): better than token probabilities. Xiong et al. (arXiv 2306.13063, ICLR 2024): overconfident; failure-prediction AUROC only 0.52 to 0.61 | Free | Low as a gate. Use agreement across samples instead |
| **Differential testing (before vs after)** | PatchDiff: 29.6% of "plausible" SWE-bench patches behave differently from the gold patch, and 7.8% of passing patches fail the full suite (Wang, Pradel, Liu, arXiv 2503.15223, 2025-03-19). SWE-Bench+ (arXiv 2410.06992): 31.08% of passes came from weak tests. UTBoost (arXiv 2506.09289, ACL 2025): 345 wrongly passed patches | A second machine on the base commit | High. Greenroom can clone VMs, and the suite already requires red then green |
| **Property checks through state, not pixels** | Rule-based checks: 83.8% precision (AgentRewardBench). "Interactive Reward Agent" (arXiv 2607.25904, UNVERIFIED): screenshots miss file, config and backend state | `machine_exec` reads (defaults, sqlite, files, logs) | High for persistence and backend claims |
| **Process, not only outcome** | OSWorld-Pro: 83.4% outcome vs 75.7% process for the same model. VeriWeb (arXiv 2508.04026): decompose into verifiable subtasks | Checklist items for intermediate states | Medium |
| **Reproduction test as a filter** (for code) | SWT-Bench (Mündler et al., arXiv 2406.12952): agent-written reproduction tests used as a filter doubled patch precision | Coder or verifier writes a failing test first | Medium. Complements the UI check |

### 4.4 Disputes

- ADR 0006's dispute loop is sound, and the prompt's "Do not change your mind just because
  you were asked to" matches the sycophancy literature. Two refinements with evidence:
  1. **Make flips require new evidence, asymmetrically.**
     - A dispute from the coder that would turn a fail into a pass is the dangerous
       direction: the coder is the party with an interest.
     - The daemon can require that a revised pass cites at least one step recorded after
       the dispute.
     - Tyen et al. show that a pointer to a specific error works; bare pressure is what
       Sharma et al. show goes wrong.
  2. **Let the independent judge see a dispute but not the rhetoric.** Pass it the
     dispute's factual claim ("step 11 was a lost click, not an app bug") as a new check
     to evidence.

---

## 5. Reliability engineering for the loop

### 5.1 Loops and tool errors

- **OpenHands StuckDetector** (docs accessed 2026-09-25,
  https://docs.openhands.dev/sdk/guides/agent-stuck-detector). On by default, it halts on:
  - the same action and observation 4 or more times;
  - the same action and error 3 or more times;
  - 3 or more monologue messages in a row;
  - an A/B ping-pong for 6 or more cycles.
- **MAST** (Cemri et al., arXiv 2503.13657, 2025-03-17, https://arxiv.org/html/2503.13657v3).
  Over 1,600 multi-agent traces, kappa 0.88:
  - Step repetition is the top failure mode at 15.7%.
  - Missing or incomplete verification is 8.2% and incorrect verification 9.1%.
  - Many verifiers do "only superficial checks ... such as checking if the code compiles".
- **Anthropic, "Writing effective tools for agents"** (2025-09-11,
  https://www.anthropic.com/engineering/writing-tools-for-agents): error responses should
  "clearly communicate specific and actionable improvements, rather than opaque error
  codes". #125's proposed error text follows this.
- **Anthropic's multi-agent post** (2025-06-13,
  https://www.anthropic.com/engineering/multi-agent-research-system): "letting the agent
  know when a tool is failing and letting it adapt works surprisingly well". The same post
  documents agents repeating identical searches.
- **Manus** (Ji, 2025-07-18,
  https://manus.im/blog/Context-Engineering-for-AI-Agents-Lessons-from-Building-Manus):
  "leave the wrong turns in the context". Vary structure slightly to avoid few-shot ruts,
  meaning repetitive loops.
- **Greenroom.** #125's design (key on tool, normalized args and error; a stronger error on
  the second identical failure; end with a question on the third) is the OpenHands rule at
  a tighter threshold. The tighter threshold is right for a verifier with a 40-step budget.
  Also apply it to identical successful calls with no state change: the same click, no AX
  diff, twice.

### 5.2 Budgets and forced endings

- **Anthropic, "Building effective agents"** (2024-12-19,
  https://www.anthropic.com/engineering/building-effective-agents): "common to include
  stopping conditions (such as a maximum number of iterations)" and "pause for human
  feedback at checkpoints or when encountering blockers".
- **OpenAI Agents SDK** (https://openai.github.io/openai-agents-python/running_agents/,
  accessed 2026-09-25): `max_turns` raises `MaxTurnsExceeded`, and an error handler can
  "return a controlled final output instead of ending the run". That is #127's closing-step
  proposal.
- **"The Unreliable Progress Bar"** (Wang, Wang, Wu, arXiv 2609.08589, 2026-09-08): "agent
  frameworks should not rely solely on model progress reports for flow control". Budgets
  must be enforced by the harness.
- **LoopTrap** (Xu et al., arXiv 2605.05846, 2026-05-07): prompt injection can poison an
  agent's sense of being done, amplifying steps 3.57 times on average and up to 25 times.
  The app under test is untrusted content (it can render any text), so budgets are also a
  security control.
- **Budget sizing.** OSWorld-Human's 2.7 to 4.3 times step overhead suggests measuring
  steps per verdict before raising 40/10 min, as #127 item 4 says.

### 5.3 Re-planning after the environment changes

- **UFO2** halts and replans when a precondition fails. It also gives the user a
  picture-in-picture desktop to work beside the agent (arXiv 2504.14603).
- **LangGraph interrupts** (https://docs.langchain.com/oss/python/langgraph/interrupts,
  accessed 2026-09-25): a resumed node restarts from its beginning, so side effects before
  the pause must be idempotent. Greenroom's analogue: after Give Back, restart the plan from
  a fresh read. Do not resume the old one (#124).
- **Anthropic's multi-agent post**: systems "resume from where the agent was when the
  errors occurred".

### 5.4 Human handoff

- **Gemini computer use** returns a per-action `safety_decision`, and `require_confirmation`
  needs an explicit acknowledgement
  (https://ai.google.dev/gemini-api/docs/computer-use, updated 2026-09-23).
- **The OpenAI Agents SDK** has `needsApproval` per tool call, and the serialized run state
  resumes later
  (https://openai.github.io/openai-agents-js/guides/human-in-the-loop/, accessed
  2026-09-25).
- **OpenAI's practical guide** (2025-04, exact date UNVERIFIED) names two triggers:
  exceeding failure thresholds and high-risk actions.
  https://cdn.openai.com/business-guides-and-resources/a-practical-guide-to-building-agents.pdf
- **For Greenroom** the handoff that matters is not risk; the VM is disposable. It is
  **validity of evidence**. When a human takes the screen mid-verification, evidence
  gathered before the takeover may no longer describe the app state after it. Rule: a
  verdict may cite only evidence from the current screen epoch, or from before it if the
  human's actions are recorded and the check is re-read after Give Back.

### 5.5 Reliability metric

- **tau-bench** (Yao et al., arXiv 2406.12045, 2024-06-17) introduced pass^k: all k trials
  succeed. gpt-4o's pass^8 in retail was under 25% while pass^1 was about 50%.
- **Anthropic's "Demystifying evals"** (2026-01-09): at k=10, pass@k and pass^k "tell
  opposite stories".
- A verifier that teams trust must be consistent: the same build should get the same verdict
  3 times out of 3. Measure pass^3 on verdict agreement.

---

## 6. Model choice

### 6.1 What the leaderboards say

**Computer use (acting)**
- Frontier closed models lead: Claude Opus 5 and Fable 5 at 83 to 86% on OSWorld-Verified
  (2026-08); GPT-5.5 and Gemini 3.5 Flash reported in the high 70s (UNVERIFIED).
- The best open-weight model is Holo3-35B-A3B at 82.56% (2026-04-20, Apache-2.0,
  https://huggingface.co/Hcompany/Holo3-35B-A3B), 3B active parameters and cheap to serve.
  Kimi K2.6 is at 73.06%.
- On long-horizon OSWorld 2.0 the open models collapse (under 5% binary).

**Judging**
- General frontier VLMs are the best judges: Opus 4.8 89.7% and GPT-5.5 89.5% on OSReward.
- General VLMs beat CUA-specialized models as judges (CUARewardBench).
- A small trained judge can come close. OS-Shepherd-9B scored 86.1% on OSReward for $1.36
  per full set vs about $100 for Opus 4.8 (full text).
- On NIM, kimi-k3 was the best brain in greenroom's own replay: 44 of 45 best actions, 11 of
  11 verdicts, multimodal (model report).

**NVIDIA**
- The Nemotron 3 Nano Omni card claims OSWorld 47.4
  (https://huggingface.co/nvidia/Nemotron-3-Nano-Omni-30B-A3B-Reasoning-BF16, 2026-04-20).
- Greenroom's own screening found it invented controls and failed 8 of 40 calls.
- The Nemotron 3 Ultra card lists text only. There is no NVIDIA computer-use claim for the
  current brain.

### 6.2 Describer vs native multimodal

- **Evidence against the prose describer:**
  - ADR 0012 rejected sending screenshots to a multimodal brain because none was available
    on NIM at the time. That is no longer true: kimi-k3 takes images.
  - ADR 0020's suite: "kimi-k3 sees the screen itself" scored 13 of 14 and got 10 of 10
    verdicts, vs 11 of 14 and 7 of 10 for ultra plus the kimi-k3 describer. The cost was
    3.5 times the wall time (1,412 s median on UI tasks).
  - The literature agrees: SeeAct and OSWorld show that passing through an intermediate text
    representation loses information, and the describer step has its own failure mode
    (ADR 0005: "Two models mean two failure modes").
- **In favour of keeping text reads:** a verifier's evidence should be exact where it can
  be. AX values beat any vision model at reading "$48.00".
- **The best pattern from the evidence:** the brain sees the image and also gets the AX
  tree text. OSWorld's screenshot-plus-a11y row is at or near the best for every capable
  model, and Anthropic and OpenAI both feed images natively.

### 6.3 One model or several

- **Evidence for separating roles:**
  - Self-preference bias (Panickssery et al.) argues against one model family judging its
    own work.
  - PoLL shows a diverse panel beats one big judge at lower cost.
  - CUARewardBench's best result is a 2-model unanimous ensemble.
- **Proposed split, which fits Greenroom:**
  - **Actor**: drives the machine, many calls. It should be fast and reliable at tool
    calling; availability matters more than peak IQ. Candidates on NIM today: kimi-k3
    multimodal, or glm-5.3 (0 failed calls, p50 12 s in the replay).
  - **Judge(s)**: reads the checklist and the recorded evidence, 1 to 3 calls per verdict,
    no tools, never sees the actor's reasoning or the coder's claim. It should come from a
    different family than both the actor and the likely coder (Claude Code today). The
    judge is where spending on a strong model buys the most, because it runs a few times
    per verdict, not 40 times per turn.
- **Router by difficulty:** GTA1 (arXiv 2507.05791) and Behavior Best-of-N show that
  test-time selection among samples helps. Spend it where the judge panel disagrees, not
  on every verdict.

### 6.4 The NIM constraint

- **What the measurements show.**
  - The current brain's measured failure is availability: 24 of 90 calls failed after
    retries, and 138 of them hit 429s on a shared key (model report).
  - Every lost verdict in ADR 0020's first two rows came from that.
  - A verifier that fails to answer 30% of the time cannot carry a badge, whatever its
    accuracy.
- **Options, in order of evidence:**
  1. Keep NIM but move to a paid or dedicated capacity tier, and choose models by
     availability measured over a week, not a night. How NIM's paid tiers work is
     UNVERIFIED; I did not research NIM pricing.
  2. Treat the provider as configuration. `nim.Client` already speaks OpenAI chat
     completions, so any OpenAI-compatible endpoint works: OpenAI, a self-hosted vLLM
     serving Holo3 or Kimi, and others. Whether each vendor's OpenAI-compatible surface
     supports images plus tools the way the daemon needs is UNVERIFIED and should be
     probed like ADR 0005's probes.
  3. Run one frontier closed model on the eval set (section 7) as a ceiling, even if it is
     not shipped. Without a ceiling you cannot tell "the verifier design is wrong" from
     "the model is weak".
- ADR 0005 says the verifier "cannot be Claude Code" because Claude Code needs Anthropic
  auth. That is about Claude Code the CLI, not about Anthropic models behind an API. The
  decision to stay on NIM is a cost and key-availability choice and should be revisited
  with section 7's numbers.

### 6.5 Cost and latency reference points

- Replit's self-testing post (2025-12-15, https://replit.com/blog/automated-self-testing):
  Playwright code in a REPL costs a median $0.20 per session, vs about $0.50 and 30 to 90 s
  per 5-field form for computer-use models. It names "Potemkin interfaces": UI that looks
  right with nothing hooked up. That is a class of bug a verifier must catch.
- Anthropic's harness post: $9 and 20 min solo vs $200 and 6 h with a planner, generator
  and evaluator harness (2026-03-24).
- Anthropic's docs: about 1,000 to 1,800 tokens per screenshot.
- Greenroom's own data: a TipSplit turn is about 3 min (ultra plus kimi describer) to 7 min
  (single kimi-k3) at p50.
- A judge panel over a recorded evidence bundle adds seconds to tens of seconds, not
  minutes. The expensive part is driving the machine.

---

## 7. Measuring the verifier

### 7.1 How others build judge eval sets

| Set | Size | Labels |
| --- | --- | --- |
| AgentRewardBench | 1,302 trajectories | 6 experts, 89.3% agreement |
| OSReward | 1,019 | 3 annotators plus meta-review, kappa about 0.71, about 800 human hours |
| Online-Mind2Web | 300 tasks | 2 annotators plus 1 adjudicator |
| CUARewardBench | 272 trajectory-level, 346 step-level | Experts |
| Trust or Escalate calibration | 500 | Human preferences |
| Mis-Score audit | 150 | Audit |

Metrics used:
- precision on success, plus NPV (AgentRewardBench, CUARewardBench);
- Cohen's kappa, not raw agreement (Judging the Judges);
- balanced accuracy and per-class recall (OSReward);
- F1 (PaperBench JudgeEval, Starace et al., arXiv 2504.01848, 2025-04: o3-mini 0.83).

Hard splits matter. OSReward's best judge falls from about 90% to about 70% on its hard
split.

### 7.2 Mutation testing for UI

- The software-testing precedent is direct.
- **MDroid+** (Linares-Vásquez et al., arXiv 1707.09038, ESEC/FSE 2017,
  https://arxiv.org/abs/1707.09038): 38 Android mutation operators from a taxonomy of 262
  fault types, 35 implemented.
- **Deng, Offutt et al.**, "Mutation operators for testing Android apps" (IST 2017,
  https://www.albany.edu/faculty/offutt/research/papers/muDroid.pdf): operators for XML
  layout, event handling and lifecycle.
- **Meta ACH** (Foster, Harman et al., arXiv 2501.12862, 2025-01-22): mutation-guided LLM
  test generation. 73% of its tests were accepted by engineers, and an equivalent-mutant
  detector reached 0.95 precision and 0.96 recall with preprocessing.
- The idea transfers: seed known faults and count how many the verifier catches. Every
  missed mutant is a measured false pass.

### 7.3 Proposed Greenroom eval set: "verifier bench"

**Unit of evaluation**
- Each case is (app at commit, task text as a coder would write it, correct verdict, the
  checks that must appear, whether an inconclusive is acceptable).
- Run each case 3 times for pass^3.

**Sources of cases**
1. **Correct changes.** The 7 realistic-suite tasks and their fixed builds. Correct verdict:
   pass.
2. **UI mutants of those same apps.** One fault each, drawn from these operator families
   (inferred from MDroid+ categories and the failures seen in greenroom runs). Correct
   verdict: fail.
   - Wrong computation: off by one, rounding, a wrong operator.
   - Potemkin control: a button with no action, a checkbox not bound to state (Replit's
     term; #116's app is one tap away from this).
   - Wrong label or wrong value format ("$48" vs "$48.00").
   - State not persisted across relaunch. The truth is in `defaults` or files, which is why
     `machine_exec` is in scope.
   - Works for the example input only: a hard-coded case, as in the Claude 4 system card's
     test hard-coding.
   - Regression elsewhere: the change works but breaks an adjacent control.
   - Crash or hang on an edge input.
   - Visual-only fault: a clipped or hidden control that AX still reports as present. This
     tests reliance on AX over pixels.
   - Delayed or asynchronous result: the value appears after 2 s.
3. **Lying coder.** A correct or broken build paired with a task text that claims success
   in detail ("I fixed it; Each pays now shows $48.00"). This measures claim sensitivity,
   ZeroGUI's 44% vs 61% effect.
4. **Infrastructure faults.** A system dialog covering the app, the app not running, a
   human taking the screen mid-check, a machine that is still booting. Correct result:
   `ask` or `inconclusive` naming the blocker, never pass or fail about the app. This
   regression-tests #97 and #124.
5. **Ambiguous tasks,** where the right move is `ask`.

**Labels**
- Mutants and correct builds are labelled by construction. That is the big advantage of
  seeding faults: no human-agreement ceiling on the verdict itself.
- Humans are still needed for two things:
  - whether the evidence cited supports each check, with 2 raters and a third on
    disagreement (Online-Mind2Web's process);
  - the ambiguous cases.

**Metrics** (report each with its 95% interval, not just the point estimate):

| Metric | Definition | Why |
| --- | --- | --- |
| **False pass rate** | pass verdicts on broken builds / broken builds | The badge's promise |
| False fail rate | fail on correct builds / correct builds | Trust from coders; drives disputes |
| Inconclusive rate | Split by correct, broken and infra cases | Abstention is fine on infra, costly on healthy builds |
| Verdict-obtained rate | Turns that end in a verdict or question, not a limit or error | #127, and ADR 0020's 6 to 7 of 10 |
| Evidence validity | Share of verdict claims backed by a cited step that shows it | #116's false claims inside a correct verdict |
| Coverage | Checklist items evidenced / items the task implies | Smyth et al.'s overclaiming |
| pass^3 | Same verdict in 3 of 3 runs | tau-bench |
| Wall time and model cost per verdict | p50 and p95 | HAL (Kapoor et al., arXiv 2510.11977): report cost with accuracy |

**Sample size, by the rule of three**

| Broken cases with 0 false passes | 95% upper bound on false pass rate |
| --- | --- |
| 30 | 10% |
| 60 | 5% |
| 150 | 2% |
| 300 | 1% |

- 10 mutants for each of 9 operator families across 7 apps gives about 90 broken cases,
  about 3.3%.
- Growing the app set to 15 to 20 small apps reaches the 1 to 2% range. Use the
  Clopper-Pearson bound in reports.

**Keep a held-out split** that prompt changes are never tuned on (Kapoor et al., "AI
Agents That Matter", arXiv 2407.01502, 2024-07-01: missing holdouts and shortcut
overfitting).

**Production monitoring**
- Sample a fixed share of production pass verdicts for human audit.
- Log Companion accept and dispute decisions as weak labels. They are biased: a coder
  accepts passes readily.
- Anthropic's "read the transcripts" advice (Demystifying evals, 2026-01-09) applies;
  "people testing agents find edge cases that evals miss" (multi-agent post, 2025-06-13).

---

## 8. Products that claim AI verification or QA

Every number below is a **VENDOR CLAIM**, and I found no independent evaluation of any of
them.

| Product | Claim | Source |
| --- | --- | --- |
| QA Wolf | "Zero flake guarantee", "100% reliable test results"; QA engineers "review each failure and file only real, human-verified bug reports" | https://aws.amazon.com/marketplace/pp/prodview-zx663ireraacm, https://www.qawolf.com/ (accessed 2026-09-25) |
| Checksum | About 70% of failures heal "without any human involvement"; the managed tier has "human engineer final verification" | https://checksum.ai/ (accessed 2026-09-25) |
| Momentic | "auto-heals intended UI changes and flags real regressions"; no accuracy figure | https://momentic.ai/ (accessed 2026-09-25) |
| mabl | "eliminating up to 95% of test maintenance"; the agent asks "for the 'why'" when unsure | https://www.mabl.com/auto-healing-tests (accessed 2026-09-25) |
| BrowserStack AI agents | Test Case Generator "91% accuracy and 92% coverage" (no method given); self-healing gives 40% fewer build failures, with "two-phase healing" and healing logs | https://www.browserstack.com/press/browserstack-launches-suite-of-ai-agents-to-redefine-software-quality-at-scale (2025-06-30), https://www.prnewswire.com/news-releases/browserstack-unveils-ai-powered-self-healing-agent-to-keep-builds-green-302617102.html (2025-11-17) |
| Autify Nexus | Playwright-based; "Fix with AI" proposes a locator and asks the user before changing it | https://autify.com/products/autify-nexus (accessed 2026-09-25) |
| Octomind | Reported shut down mid-2026. UNVERIFIED (secondary only; octomind.dev did not resolve) | https://bug0.com/knowledge-base/what-is-octomind |
| Applitools | "99.9999% accuracy" for Visual AI (2022); "100% reproducible" for Autonomous; no method | https://applitools.com/blog/visual-ai/ (2022-05-20) |
| Spur | "~80% fewer false positives than scripted suites"; video and step evidence per run | https://www.spurtest.com/ (accessed 2026-09-25) |
| Meticulous | Deterministic replay "eliminates flakes" | https://www.meticulous.ai/ (accessed 2026-09-25) |
| Chromatic | "No test flake"; keeps "explicit sign-off" by human reviewers | https://www.chromatic.com/ (accessed 2026-09-25) |
| Functionize | "agents write the code. Studio proves it works." | https://www.functionize.com/ (accessed 2026-09-25) |
| testRigor | "99.5% less test maintenance" | https://testrigor.com/ (accessed 2026-09-25) |
| Playwright Test Agents | Planner, generator and healer; the healer may mark a test skipped if it "believes that functionality is broken" | https://playwright.dev/docs/test-agents (v1.56, Oct 2025, UNVERIFIED date) |
| Replit Agent 3 App Testing | The agent tests its own app in a browser when "enough has changed"; video replay | https://docs.replit.com/features/agent/app-testing, https://replit.com/blog/automated-self-testing (2025-12-15) |
| Cursor cloud agents | Agents "iterate until they've validated their output" and attach videos, screenshots and logs; over 30% of merged Cursor PRs come from them | https://cursor.com/blog/agent-computer-use (2026-02-24) |
| Vercel Agent | "Sandbox-validated suggestions" using your builds, tests and linters | https://vercel.com/docs/agent (updated 2026-09-18) |

**What the market pattern says** (my synthesis of the pages above):
- Nobody publishes a false-pass rate.
- The vendors that promise zero flakes get there through human review (QA Wolf, Checksum's
  managed tier, Chromatic's sign-off) or deterministic replay (Meticulous, Chromatic), not
  through model accuracy.
- The agentic ones show evidence (video, steps, diffs) and ask before changing tests.
- Coding-agent products (Replit, Cursor, Lovable) verify their own work. That is the
  self-evaluation setup Park and Choi and Anthropic's harness post show to be unreliable.
- Greenroom's differentiator is independence plus evidence. A published, measured
  false-pass rate on a public mutant set would be unique in this market.

---

## 9. Recommendations, prioritized

Each item names the evidence behind it. P0 items are what I would do before anyone calls
the output "verified". Effort is not weighed heavily, per the project's preferences.

### P0: measure, and stop the known false claims

1. **Build the verifier bench (section 7.3) before changing prompts or models.**
   - Start with the 7 realistic-suite apps, about 60 mutants, the correct builds, and the
     lying-coder and infrastructure cases.
   - Report false pass rate with its upper bound, false fail rate, inconclusive rate,
     verdict-obtained rate, evidence validity and pass^3.
   - Evidence: rule of three; AgentRewardBench and CUARewardBench metrics; MDroid+ and ACH
     for mutants; Kapoor et al. for holdouts and cost reporting.
   - Every later item is judged by this bench.
2. **A checklist first, evidence per check, enforced by the daemon.**
   - Change `report_verdict` to take `checks: [{criterion, status: pass|fail|unchecked,
     evidence: [step], observed}]` instead of a free list.
   - Before acting, the verifier posts the checklist it derived from the task as a
     `progress` or `reply`, so the coder or a human can correct it early (Anthropic's
     sprint contract).
   - The daemon refuses a `pass` verdict unless every check is `pass` and cites at least one
     observation step (`machine_ui`, `machine_screenshot` or `machine_exec`) recorded after
     the last input action that could affect it.
   - A check with no evidence is `unchecked`, which makes the verdict `inconclusive` or a
     pass with stated scope (see item 9).
   - Evidence: WebJudge, RocketEval, Agent-as-a-Judge, "grade outcomes not claims"
     (Anthropic 2026-01-09), #116.
3. **Detect whether each action took effect, in the daemon.**
   - After each input call, diff the verifier's pre-action and post-action AX reads, or
     take an automatic read, and append "no change detected" or the changed values to the
     tool result.
   - A check whose evidence step follows an action with no effect is flagged.
   - Evidence: Mobile-Agent-v2 reflection, UFO2 preconditions, VeriGUI, Behavior Best-of-N
     narratives, #116.
4. **A stuck guard and a forced closing verdict.**
   - Implement #125: key on tool, normalized args and result; a stronger error at 2; end
     with a question at 3. Also count "same action, no state change".
   - Implement #127: at the step or time limit, make one tool-less call that must produce
     a verdict from the recorded evidence, with `inconclusive` allowed. Return a typed
     ending to `agent_wait`.
   - Evidence: OpenHands StuckDetector, MAST (step repetition is the top failure), OpenAI
     Agents SDK controlled final output, "Unreliable Progress Bar".
5. **Screen epoch and mandatory freshness** (#124).
   - Refuse verifier input aimed from a tree older than the last input or control change.
   - Give Back starts a turn whose first call must be a read.
   - A verdict may cite pre-takeover evidence only for checks re-read after Give Back.
   - Evidence: TOCTOU paper (6.5 s gap), UFO2 halt on interface change, LangGraph resume
     semantics.
6. **Separate the claim from the task.**
   - Project the coder's task into two parts: "what to check" (goes to the actor and the
     checklist) and "what the coder says it did" (labelled as an unverified claim, a source
     of checks, never evidence).
   - Add one line to the system prompt: the coder's statements are hypotheses to test, and
     a verdict never rests on them.
   - Evidence: ZeroGUI (61.5% vs 44.3% precision), AgentRewardBench's filter example,
     OSReward's nuance (keep the claim as a list of checks).

### P1: independent judgment

7. **An independent judge for every pass.**
   - After the actor proposes `pass`, a fresh-context judge gets only the checklist, the
     cited evidence (AX reads verbatim, screenshots as images, exec output) and the task's
     "what to check" part.
   - It does not get the actor's reasoning or the coder's claim, and it answers per check.
   - A pass is posted only if the actor and the judge agree on every check. Disagreement
     becomes `inconclusive`, naming the disputed checks, or the actor gets one more
     evidence-gathering round.
   - Start with one judge from a different family than the actor. Move to 2 x 2 unanimity
     if the bench shows it pays.
   - Evidence: CUARewardBench UPE (82.9% to 89.8% precision), PoLL, self-preference bias,
     Park and Choi (in-band judges accept 44% of regressions).
   - Fails do not need the judge as urgently: a false fail is caught by the coder's dispute,
     and a false pass is not caught by anyone.
8. **Differential verification against the base commit.**
   - For bug fixes: reproduce the bug on the base build first, then show it gone on the
     change. For features: show the behaviour absent or different on base.
   - A second cloned machine on the base commit runs the same checklist.
   - A pass where base and change behave identically on every check is suspect, and
     becomes inconclusive with a reason.
   - Evidence: PatchDiff (29.6% of plausible patches diverge), SWE-Bench+ and UTBoost (weak
     tests pass wrong patches), the realistic suite's red-then-green rule, and this
     project's own "reproduce the bug first" practice.
9. **Scope in the badge.**
   - "Verified by Greenroom" should render the checklist with each check's evidence and
     list what was not checked.
   - Evidence: OSWorld-Pro's outcome-vs-process gap; Smyth et al.'s 80% misleading reports
     when work was skipped; the vendor pattern of showing evidence (Cursor, Spur, Replit).
10. **Read state, not pixels, when state is the claim.**
    - Persistence, backend and file claims are checked with `machine_exec` (defaults,
      sqlite, files, logs, curl against the app's own server) alongside the UI.
    - The prompt should list these as preferred evidence for those claim types.
    - Evidence: rule-based precision 83.8% (AgentRewardBench), Anthropic's "outcome is the
      environment state".
11. **Calibrate inconclusive on the bench.**
    - Use agreement between samples or judges as the confidence score, not verbalized
      confidence.
    - Pick the threshold on a calibration split so that the false pass rate among posted
      passes is at or below the target with a Clopper-Pearson bound.
    - Evidence: Trust or Escalate, conformal abstention, Xiong et al. (verbalized
      confidence AUROC 0.52 to 0.61).

### P1: model and provider

12. **Fix availability before tuning accuracy.**
    - Choose the actor by measured availability over days plus bench accuracy.
    - Candidates from greenroom's own data: kimi-k3 multimodal (best accuracy, slow),
      glm-5.3 (0 failed calls, fast, text only, needs a describer).
    - Evidence: model report (24 of 90 ultra calls failed), ADR 0020 (every lost verdict was
      a model error).
13. **Let the brain see images and read the AX tree.** Adopt the ADR 0020 follow-up (single
    multimodal actor) once latency is acceptable, but keep AX values as the preferred
    evidence for text and numbers. Evidence: ADR 0020 suite row (13 of 14, 10 of 10
    verdicts), OSWorld screenshot-plus-a11y rows, describer hallucinations (model report).
14. **Put a frontier ceiling on the bench.**
    - Run the bench once with a frontier closed computer-use model, and once with an
      open-weight CUA model such as Holo3-35B-A3B self-hosted, as the actor and as the
      judge.
    - This separates design problems from model problems and prices the upgrade path.
    - Evidence: the OSWorld-Verified gap (83 to 86% frontier vs 73% Kimi K2.6), OSReward
      judge accuracies.

### P2: grounding extras and operations

15. **Zoom and crop for small targets and vision-only content:** a `machine_screenshot`
    region parameter returning a native-resolution crop. Evidence: ScreenSeekeR, RegionFocus,
    Anthropic's zoom action.
16. **OCR merged into the tree** for apps with weak accessibility. Evidence: Agent S's
    image-augmented tree, Screen2AX (33% full AX support on macOS), UFO2 hybrid.
17. **Asymmetric dispute rule.** A fail may flip to pass after a coder dispute only with at
    least one new observation step after the dispute. Evidence: sycophancy (Sharma et al.),
    Tyen et al.
18. **Production audit.** Have a human review a fixed sample of production passes weekly,
    feed the misses into the bench, and track the false pass rate over time. Evidence:
    Anthropic's transcript-reading advice; the vendor pattern that zero-flake promises rest
    on human review.

### Metric targets (proposals for the grilling, not derived facts)

| Stage | False pass rate (95% upper bound) | False fail | Verdict obtained | Inconclusive on healthy builds | pass^3 agreement | p50 time per UI verdict |
| --- | --- | --- | --- | --- | --- | --- |
| Internal dogfood | 5% or less (about 60 broken cases, 0 false passes) | 10% or less | 95% or more | 20% or less | 90% or more | 5 min or less |
| "Verified by Greenroom" badge | 1 to 2% or less (150 to 300 broken cases) | 5% or less | 99% or more | 10% or less | 95% or more | 5 min or less |

For scale:
- Frontier judges are at about 90% accuracy on OSReward and fall to about 70% on hard
  cases.
- Human annotators agree 81 to 89% of the time.
- A 1% false pass rate is therefore not reachable by judge accuracy alone. It needs
  abstention (items 7 and 11) and state-grounded checks (items 2, 8 and 10), with the
  inconclusive rate as the price.

---

## 10. Open questions for the grilling session

1. **What exactly does the badge certify?** "Every listed acceptance check was observed on
   this build in a clean macOS VM", or "the PR works"? The first is defensible. The second
   is not.
2. **Who owns the acceptance checklist?** Derived by the verifier from the task, written by
   the coder, taken from the PR description or issue, or approved by a human? If the coder
   writes it, a coder can under-specify it.
3. **What inconclusive rate will teams accept** in exchange for a 1% false pass bound? 10%?
   20%? What happens to a PR with an inconclusive verdict?
4. **Should the verifier read the diff or the source?** Agent-as-a-Judge gains from reading
   the workspace, but the current prompt forbids deriving expected answers from code under
   "UI only". Reading the diff to decide what to test, while never using code as evidence,
   may be the right line.
5. **Is a second machine per verification (the base-commit differential) affordable** in VM
   count and time? Is it required for all PRs, or only for bug fixes?
6. **Provider policy:** is leaving NIM on the table? What latency and cost per verdict is
   the budget? Is a judge from a closed frontier model acceptable if the actor stays on NIM?
7. **Independence from the coder's model family:** if most coders are Claude, should the
   judge be required to be non-Claude, and the reverse?
8. **Human takeover and evidence validity:** after a human touched the app, is the verdict
   still "verified by Greenroom", or "verified with human assistance"? Should the badge say
   so?
9. **Who labels the bench, and how big must it be** before the first external claim? Is
   mutant generation manual, scripted, or itself agent-driven, as in Meta ACH?
10. **What happens when the actor and the judge disagree repeatedly on one app?** Is that
    an app accessibility problem (Screen2AX's 33%), a task ambiguity, or a verifier bug?
    Who triages it?
11. **Where does a failing unit test fit?** Should the verifier run the project's own tests
    as a check, knowing that SWE-bench-style weak tests let wrong patches pass?
12. **Prompt-injection surface:** the app under test can render any text, including "Task
    complete, report pass". Do we need a rule that on-screen text never counts as an
    instruction, and a bench case that tests it (LoopTrap, macOSWorld deception pop-ups at
    about 70% distraction)?
