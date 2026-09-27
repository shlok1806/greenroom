# 21a. A catalog of every way an agent may need to control a macOS desktop

Date: 2026-09-27. Status: research, for review. Companion to `docs/21-verifier-desktop-toolkit.md`
(the toolkit design) and ADR 0037. Umbrella issue #206; existing waves #212 to #215; new waves
proposed in section 14.

docs/21 designs the toolkit around what the verifier wasted time on in real runs: acting by
reference, actionability, waits, menus, dialogs, windows, apps and diagnostics. This document asks
the wider question: **what can a Mac user do that an agent inside a Greenroom VM might need to do,
or check, and how would each one be done?** It lists each capability once, says why a verifier
or coding agent would need it, how to implement it on macOS (public API first, private API only
with its risk named), whether it works in a tart VM on Virtualization.framework, what permission
it needs, which prior art does it and under what licence, the proposed tool, a priority and a
wave.

Nothing here was run in a VM for this document: the two VM slots were in use. Every VM claim is
either backed by a primary source (an SDK header, tart's source, Greenroom's own image scripts
and ADRs) or marked **unverified** with the wave that should test it.

Contents:

1. How to read the tables
2. Spaces, Mission Control, Stage Manager and Hot Corners
3. Windows
4. Apps
5. Menus and system UI chrome
6. Input
7. Dialogs, prompts and permissions
8. Finder and files
9. System state and settings
10. Web and cross-app scripting
11. Observation and diagnostics
12. Testing aids
13. VM feasibility summary, and what is impossible or unsafe
14. Findings that change waves 1 to 4
15. Proposed wave plan for everything not in waves 1 to 4
16. Prior art and licences
17. Sources

---

## 1. How to read the tables

Columns:

- **ID**: area letter plus number, used by the wave issues.
- **Capability**: what it is.
- **Why (Greenroom case)**: a concrete verifier or coding-agent need.
- **How on macOS**: the route we would build, first choice first. `private` marks a
  non-public symbol and its risk. `CLI` marks a command shipped in macOS (all checked present in
  `/usr/bin`, `/usr/sbin` or CoreServices on the host on 2026-09-27).
- **VM**: whether it works in a Greenroom machine (tart 2.32.1, `tart run --no-graphics`,
  Virtualization.framework, guest macOS 26 as measured in docs/09, SIP off). `yes` needs a
  source; `expected` is reasoning without a measurement; `unverified: Wn` names the wave that
  must test it; `no` is backed by a source.
- **TCC**: the permission the guest agent needs. The agent's responsible process is
  tart-guest-agent, which the image already grants Accessibility (AX), PostEvent, ScreenCapture,
  Microphone and AppleEvents rows (docs/02, docs/09, `base.sh`). `none` means no TCC gate.
- **Prior art**: projects that do it, with licence (section 16). Ideas may be taken from all of
  them; code only from MIT or Apache-2.0 ones, with attribution.
- **Tool**: the proposed tool and arguments in docs/21's style. Every tool keeps docs/21's
  contract: actionability before input, a settle and an **effect** after it (`changed`, `no
  change`, `unverifiable`, `refused`), a recorded step with evidence, and a deadline (5 s default
  for actions, 40 s max for waits, 45 s absolute). "setup" marks a tool whose steps the verifier
  may not cite as the action a check is about (docs/21 5.3 rule for `machine_set_value`).
- **P**: P0 blocks verifying common tasks today; P1 is frequent in tasks or a known waste; P2
  serves a specific class of task; P3 is rare, research, or a guard.
- **Wave**: `W1` to `W4` are #212 to #215 (docs/21 section 8); `W5` to `W9` are proposed in
  section 15. "W2 (covered)" means docs/21 already has it; the row is here for completeness and
  sometimes adds a detail.

Abbreviations for evidence: **[VZ]** a Virtualization.framework header in the installed SDK;
**[tart]** tart's source at `cirruslabs/tart` HEAD (read 2026-09-27); **[img]** Greenroom's image
scripts (`apps/daemon/internal/machine/guest/base.sh`, `lean.sh`, `desktopprefs.go`,
`base.go`, `timezone.go`); **[HIS]** HIServices AX headers; **[AK]** AppKit headers; **[CG]**
CoreGraphics headers; **[SCK]** ScreenCaptureKit headers.

---

## 2. Spaces, Mission Control, Stage Manager and Hot Corners

Spaces have **no public API**. Every window manager that touches them uses SkyLight (the
private CoreGraphics Services, `SLS*`/`CGS*` symbols) to read, and either keyboard shortcuts,
Mission Control's accessibility tree in the Dock process, or code injected into the Dock to
change. yabai's create, destroy, focus and window-to-space operations now all go through its
Dock "scripting addition" (`src/sa.m`: `SA_OPCODE_SPACE_CREATE`, `_DESTROY`, `_FOCUS`,
`WINDOW_TO_SPACE`), which needs SIP's filesystem and debugging protections off and, on Apple
Silicon, the `-arm64e_preview_abi` boot-arg. Greenroom's image has SIP off (docs/09), so it
would be possible, but injecting code into the Dock changes the system under test and breaks on
every macOS update. **Rejected.** The routes below use SkyLight for reading only and the same
events and AX a user's actions produce for changing.

| ID | Capability | Why (Greenroom case) | How on macOS | VM | TCC | Prior art | Tool | P | Wave |
|---|---|---|---|---|---|---|---|---|---|
| S1 | List Spaces per display: id, uuid, type (user, full screen), current, windows on each | An app that opens its window on another desktop looks like "launched with no window" (the case 5 shape in docs/21); the verifier must see where the window went | `private` `SLSCopyManagedDisplaySpaces(cid)` (per display: `Spaces[]` with `ManagedSpaceID`, `uuid`, `type`, and `Current Space`), `SLSCopySpacesForWindows(cid, 0x7, [wid])`, `SLSSpaceGetType`, window ids from `private` `_AXUIElementGetWindow`. Read-only; the symbols have been stable for years (yabai, Hammerspoon, Peekaboo) but may vanish in a release, so resolve with `dlsym` and report `unsupported` instead of crashing. Public cross-check: a window listed by `CGWindowListCopyWindowInfo(.optionAll)` but not `.optionOnScreenOnly` is on another Space or minimized | expected: one display, so one managed display [VZ]; unverified: W5 | AX (titles) | yabai `space_manager.c` (MIT); Hammerspoon `hs.spaces` (MIT); Peekaboo `space list` (MIT) | `machine_space {action:"list"}` returns spaces with index, current, and window refs on each | P1 | W5 |
| S2 | Switch to Space N, next, previous | Bring the app's desktop forward; test an app's reaction to `NSWorkspace.activeSpaceDidChangeNotification` | (a) keyboard: ctrl+left/right (symbolic hotkeys 79 and 81, on by default) and ctrl+N ("Switch to Desktop N", ids 118 and up, off by default: enable in `com.apple.symbolichotkeys` then run `activateSettings -u` from SystemAdministration.framework); (b) Mission Control: open it, `AXPress` the space button under the Dock's `mc.spaces` group (Hammerspoon `gotoSpace`). Read back with S1 after `private` `SLSManagedDisplayIsAnimating` goes false. Not `SLSManagedDisplaySetCurrentSpace`: it desynchronizes the Dock | expected (keyboard events reach the Dock like any shortcut); unverified: W5 | PostEvent, AX | Hammerspoon (MIT); Peekaboo `space switch` (MIT); Silica (MIT) | `machine_space {action:"switch", to: n \| "next" \| "previous"}` effect "space 1 -> 2; visible windows now e1, e7" | P1 | W5 |
| S3 | Create a Space | Multi-desktop tests: "the palette follows me to a new desktop" | Mission Control, then `AXPress` the "add desktop" button in the Dock's Mission Control tree (Hammerspoon `addSpaceToScreen`). `private` `CGSSpaceCreate` makes an unmanaged space that Mission Control does not show: not equivalent. yabai needs the Dock scripting addition: rejected above | unverified: W5 | AX, PostEvent | Hammerspoon (MIT) | `machine_space {action:"create"}` | P2 | W5 |
| S4 | Remove a Space | Clean up after S3; test windows moving to the neighbour Space | Mission Control, then the custom AX action `AXRemoveDesktop` on the space button (Hammerspoon `removeSpace`) | unverified: W5 | AX | Hammerspoon (MIT) | `machine_space {action:"remove", space}` | P2 | W5 |
| S5 | Move a window to another Space | "The app reopens its window on the desktop where it was" | Public events only: mouse-down on the title bar, press ctrl+N, mouse-up (Silica `SIWindow -moveToSpace:`, used by Amethyst). Fallback: drag the window's thumbnail onto a space in Mission Control. Read back with S1. `private` `SLSMoveWindowsToManagedSpace` stopped moving other processes' windows in macOS 14.5; Hammerspoon's `SLSSpaceSetCompatID` + `SLSSetWindowListWorkspace` workaround is fragile and yabai now requires its scripting addition | unverified: W5 | PostEvent, AX | Silica, Amethyst (MIT); Hammerspoon `moveWindowToSpace` (MIT); yabai (MIT) | `machine_space {action:"move_window", ref, to}` | P2 | W5 |
| S6 | Assign an app to This Desktop, All Desktops, or None | Menu-bar and utility apps whose panels must show on every desktop | Dock item context menu Options > Assign To (AX `AXShowMenu` on the `AXApplicationDockItem`); persisted by the Dock in `com.apple.spaces` `app-bindings`. `private` `SLSProcessAssignToSpace`/`SLSProcessAssignToAllSpaces` exist but bypass the Dock's own record | unverified: W5 | AX | Hammerspoon (MIT) | `machine_space {action:"assign", app, to:"this" \| "all" \| "none"}` | P3 | W5 |
| S7 | Open, read and close Mission Control | See every window of every app, find a window hidden behind others | `open -a "Mission Control"` (the app exists at `/System/Applications/Mission Control.app`), ctrl+up (hotkey 32), or `private` `CoreDockSendNotification("com.apple.expose.awake")`. Its AX tree exists only in the Dock process while visible (Hammerspoon's note); read it then, close with Escape | unverified: W5 | AX, PostEvent | Hammerspoon (MIT) | `machine_space {action:"overview"}` returns spaces and window thumbnails as refs, then closes | P2 | W5 |
| S8 | App Exposé | List all windows of one app, including minimized ones and recent documents | ctrl+down (hotkey 33) or `com.apple.expose.front.awake`; AX in the Dock | unverified: W5 | AX, PostEvent | Hammerspoon (MIT) | `machine_space {action:"app_expose", app}` | P3 | W5 |
| S9 | Show Desktop | Reach desktop icons and widgets; drop a file on the Desktop | F11 (hotkey 36) or `com.apple.showdesktop.awake`; undo with the same. "Click wallpaper to reveal desktop" is off in the image (`desktopprefs.go`) because one missed click hid the app | expected; unverified: W5 | PostEvent | Hammerspoon (MIT) | `machine_space {action:"show_desktop", on:bool}` | P2 | W5 |
| S10 | Full screen as its own Space: enter, exit, wait for the transition | Full-screen layout bugs; the verifier clicking during the 0.7 s animation | AX attribute `AXFullScreen` (undocumented string, not in [HIS], used by Rectangle, Amethyst, Hammerspoon), else the green button (`kAXFullScreenButtonAttribute`, public) or Window > Enter Full Screen; wait until the new Space (type 4 in S1) is current and not animating | expected; unverified: W2 | AX | Rectangle, Amethyst, Hammerspoon (MIT) | `machine_window {action:"fullscreen", on:bool}` reports the Space it landed in | P1 | W2 (covered), W5 adds the wait |
| S11 | Split View | Layout at half width next to another app | Hover the green button, pick "Tile Window to Left of Screen" from its menu (AX), then pick the second window in the picker shown by the Dock | unverified: W5 | AX, PostEvent | none found | `machine_window {action:"split", ref, with}` | P3 | W5 |
| S12 | Stage Manager on or off | Apps whose window frames break under Stage Manager; knowing the image's state so clicks do not land on the strip | Control Center module (AX in ControlCenter), or `defaults write com.apple.WindowManager GloballyEnabled -bool true` (key name from community sources; read back and confirm with a window frame change) | unverified: W6 | AX | none found | `machine_settings {set:{stage_manager:bool}}` | P2 | W6 |
| S13 | Stage Manager groups: list the strip, switch, add a window to a set | Tests of an app's behaviour when it is moved to the strip | AX in the WindowManager process (running on the host as `WindowManager`); switch by pressing a strip item; add by dragging a window onto the stage | unverified: W5 | AX, PostEvent | none found | `machine_space {action:"stage", ...}` | P3 | W5 |
| S14 | Hot corners | A misaimed drag to a corner fires Mission Control or Lock Screen and the verifier sees a different screen | `com.apple.dock` `wvous-{tl,tr,bl,br}-corner` and `-modifier` integers, then `killall Dock` | expected | none | macos-automator-mcp (MIT) | image default: all corners off; `machine_settings {set:{hot_corners:{...}}}` for tests | P2 | W5 (image), W6 (tool) |
| S15 | Stable Space order | Index-based switching and "desktop 2" in a task need a fixed order | `com.apple.dock mru-spaces -bool false` ("Automatically rearrange Spaces based on most recent use" off) | expected | none | yabai docs (MIT) | image default | P1 | W5 |
| S16 | Space change events | `wait_for` a space switch; attention when a full-screen app takes over | `NSWorkspace.activeSpaceDidChangeNotification` (public) as an agent `EVENT` | expected | none | Hammerspoon `hs.spaces.watcher` (MIT) | `machine_wait_for {target:{space:...}}` | P2 | W5 |

---

## 3. Windows

| ID | Capability | Why (Greenroom case) | How on macOS | VM | TCC | Prior art | Tool | P | Wave |
|---|---|---|---|---|---|---|---|---|---|
| W1 | List an app's windows: title, role, subrole, frame, main, focused, minimized, full screen, modal | Every action targets one window; two candidates are refused (docs/21 5.3) | AX `kAXWindowsAttribute`, `AXMain`, `AXFocused`, `AXMinimized`, `AXModal`, subroles `AXStandardWindow`, `AXDialog`, `AXFloatingWindow`, `AXSystemDialog` [HIS]; window number via `private` `_AXUIElementGetWindow` (the one private API AeroSpace allows itself) | yes: the helper does this today (`machine_ui`) | AX | AeroSpace, Rectangle, Peekaboo (MIT) | `machine_window {action:"list"}` | P0 | W2 (covered) |
| W2 | List every on-screen window of every process, in z-order, with layer, owner, alpha | Name what covers the app when it is another process (#195's notification, a system alert) | `CGWindowListCopyWindowInfo(.optionOnScreenOnly)` returns front to back with `kCGWindowLayer`, `kCGWindowOwnerPID`, `kCGWindowAlpha`, bounds; window names need Screen Recording since 10.15 | yes: public API; the agent has ScreenCapture | ScreenCapture | alt-tab-macos (GPL-3.0, ideas only), Hammerspoon (MIT) | `attention` line (W1/W2) and `machine_window {action:"list", all:true}` | P0 | W2 (extend) |
| W3 | Focus and raise a window | Clicking into a background window only activates it | `AXRaise`, set `AXMain`, then `NSRunningApplication.activate(from:options:)` (cooperative activation, macOS 14) | yes | AX | Rectangle (MIT) | `machine_window {action:"focus"}` | P0 | W2 (covered) |
| W4 | Minimize and restore | Minimize-to-Dock bugs; restoring a window a test minimized | `AXMinimized` true/false; the minimized window is also an `AXMinimizedWindowDockItem` | expected | AX | Hammerspoon (MIT) | `machine_window {action:"minimize" \| "restore"}` | P1 | W2 (covered) |
| W5 | Zoom (green button click, Window > Zoom) | Apps with a custom zoom size | `AXZoomButton` press, or Window > Zoom via `machine_menu` | expected | AX | Hammerspoon (MIT) | `machine_window {action:"zoom"}` | P2 | W2 (add) |
| W6 | Move and resize, read constraints | Layout at a given size; windows that refuse a size (min or max) | Set `AXPosition`, `AXSize`, read back (the app may clamp); a clamp is reported, not hidden | yes (helper today) | AX | Rectangle, Amethyst (MIT) | `machine_window {action:"move" \| "resize", frame}` | P1 | W2 (covered) |
| W7 | Built-in window tiling (macOS 15 and later) | Layout at half or quarter screen the way a user does it | Window > Move & Resize > Left, Right, Top, Bottom, quarters, Fill, Center, Return to Previous Size (AX menu path); fn+ctrl+arrow; edge drag when `com.apple.WindowManager EnableTilingByEdgeDrag` is on (host shows the keys `EnableTilingByEdgeDrag`, `EnableTopTilingByEdgeDrag`, `EnableTilingOptionAccelerator`, `EnableTiledWindowMargins`) | unverified: W5 | AX | Rectangle (MIT), Loop (GPL-3.0, ideas only) | `machine_window {action:"tile", position:"left" \| "right" \| "top" \| "bottom" \| "top_left" ... \| "fill" \| "center"}` | P2 | W5 |
| W8 | Close a window | Close buttons that quit the app (docs/21 case 1) | `AXCloseButton` press; warn when it is the app's last window and the app quits with it | yes | AX | Peekaboo (MIT) | `machine_window {action:"close"}` | P0 | W2 (covered) |
| W9 | Native window tabs: list, select, merge, move a tab to a new window | Document apps with tabs; Finder and Safari | Tab bar is an `AXTabGroup` with `AXTabs` in the title bar [HIS]; Window menu: Show Previous/Next Tab, Merge All Windows, Move Tab to New Window; `AppleWindowTabbingMode` (always, fullscreen, manual) in NSGlobalDomain | unverified: W5 | AX | Hammerspoon `hs.tabs` (MIT) | `machine_window {action:"tabs" \| "select_tab" \| "merge_tabs" \| "detach_tab"}` | P2 | W5 |
| W10 | Sheets, popovers, panels, drawers | A sheet blocks its window; a popover closes on the next click | Roles `AXSheet`, `AXPopover`, `AXDrawer`; notifications `AXSheetCreated`, `AXDrawerCreated` [HIS] | yes (roles in the tree today) | AX | Playwright modal states (Apache-2.0) | `attention` + `machine_dialog` | P0 | W2 (covered) |
| W11 | Several windows per app; new window | "File > New Window opens a second, independent window" | cmd+N or the menu; refs per window; ambiguous targets refused | yes | AX | cua-driver `ambiguous_window_target` (MIT) | `machine_window` + `machine_menu` | P1 | W1/W2 (covered) |
| W12 | Window level and "always on top" | Floating inspector panels must stay above the document | Read `kCGWindowLayer` (W2) and compare z-order after activating another app. Setting another app's level needs `private` `SLSSetWindowLevel` from the Dock's connection (yabai scripting addition): not offered | yes for reading (public CGWindowList) | ScreenCapture | yabai (MIT) | `machine_window {action:"list", all:true}` shows `level` | P2 | W5 |
| W13 | Key and main window changes | Focus moved to the wrong window after a sheet closed | `AXFocusedWindowChanged`, `AXMainWindowChanged` notifications [HIS] | yes | AX | Hammerspoon (MIT) | `machine_wait_for {target:{window:...}, state:"focused"}` | P1 | W1 (covered) |
| W14 | Window state restoration on relaunch | "Reopen shows the same windows at the same place" | The image turns it off (`NSQuitAlwaysKeepsWindows false`, `TALLogoutSavesState false` in `desktopprefs.go`); to test it, launch with `-NSQuitAlwaysKeepsWindows YES` (argument domain) or write the app's own domain; state is in `~/Library/Saved Application State/<id>.savedState`. `-ApplePersistenceIgnoreState YES` or `open -F` launches without it | expected | none | none | `machine_app {action:"launch", restore_state:bool}` | P2 | W9 |
| W15 | Capture one window even when covered | Evidence of the app's window, not what sits over it | `SCContentFilter(desktopIndependentWindow:)` [SCK] | yes (docs/21 3.8) | ScreenCapture | Peekaboo (MIT) | `machine_screenshot {window}` | P0 | W1 (covered) |
| W16 | Document window details: represented file, edited dot | "The window shows unsaved changes" | `AXDocument` (file URL), `AXEdited` [HIS] | expected | AX | none | shown in `machine_snapshot` window line | P2 | W1 (add) |
| W17 | Move a window by dragging its title bar | Apps that treat a real drag differently from an AX move (snapping, tiling) | `machine_drag` from the title bar's hit-tested point | expected | PostEvent | Silica (MIT) | `machine_drag {from: window ref, to:{dx,dy}}` | P2 | W2 (covered) |

---

## 4. Apps

| ID | Capability | Why (Greenroom case) | How on macOS | VM | TCC | Prior art | Tool | P | Wave |
|---|---|---|---|---|---|---|---|---|---|
| A1 | Launch by name, bundle id or path, and wait until ready | `open -a Preview` opened no window, which produced false fails (#213) | `NSWorkspace.openApplication(at:configuration:)`; `urlForApplication(withBundleIdentifier:)`; ready = `isFinishedLaunching` + a window + AX answers | yes (public) | none | Peekaboo (MIT) | `machine_app {action:"launch", wait:"window"}` | P0 | W2 (covered) |
| A2 | Launch with arguments, environment, language, region, appearance | Localization, RTL and debug flags without changing the whole system | `NSWorkspace.OpenConfiguration.arguments` and `.environment`; argument-domain defaults: `-AppleLanguages "(fr)"`, `-AppleLocale fr_FR`, `-NSForceRightToLeftWritingDirection YES`, `-NSDoubleLocalizedStrings YES`, `-NSShowNonLocalizedStrings YES`; appearance per launch with `-AppleInterfaceStyle Dark` is unverified | expected; appearance flag unverified: W9 | none | none | `machine_app {action:"launch", args, env, locale, rtl, pseudo}` | P1 | W2 (args), W9 (locale presets) |
| A3 | Launch through a URL scheme or deep link | "myapp://open?id=3 opens the right document" | `NSWorkspace.open(url)`; resolve the handler first with `urlForApplication(toOpen:)` | yes | none | macos-automator-mcp (MIT) | `machine_open {url}` | P1 | W2 (covered) |
| A4 | Activate | Bring the app forward before a check | `NSRunningApplication.activate(from:options:)` | yes | none | all | `machine_app {action:"activate"}` | P0 | W2 (covered) |
| A5 | Hide, unhide, hide others | "Hide others keeps the palette visible" | `NSRunningApplication.hide()`/`unhide()`; cmd+H, opt+cmd+H | expected | none | Hammerspoon (MIT) | `machine_app {action:"hide" \| "unhide" \| "hide_others"}` | P2 | W2 (add unhide) |
| A6 | Quit, with the unsaved-changes sheet named | Quit that asks to save must not be reported as a hang | Apple Event quit (`NSRunningApplication.terminate()`), then watch for `AXSheetCreated` | yes | AppleEvents | Peekaboo (MIT) | `machine_app {action:"quit"}` | P0 | W2 (covered) |
| A7 | Force quit; the Force Quit window | A hung app under test; testing "not responding" UX | `forceTerminate()` / SIGKILL; opt+cmd+Esc opens the Force Quit window (AX in loginwindow, unverified) | yes for the API | none | all | `machine_app {action:"force_quit"}` | P1 | W2 (covered) |
| A8 | Relaunch as an input step | The relaunch tax on 44 of 45 persistence trials (docs/18) | quit, wait for exit, launch, wait for a window | yes | AppleEvents | none | `machine_app {action:"relaunch"}` | P0 | W2 (covered) |
| A9 | Open files with a given app | "Open this .csv in the app under test" | `NSWorkspace.open([urls], withApplicationAt:, configuration:)`; `open -a App file` | yes | none | macos-automator-mcp (MIT) | `machine_open {path, app}` | P1 | W2 (covered) |
| A10 | Read and set default handlers for file types and URL schemes | "The app registers as the handler for .tipsplit files and tipsplit:// links" | Read: `urlForApplication(toOpen:)`, `urlsForApplications(toOpen: UTType)`; set: `setDefaultApplication(at:toOpenContentType:)`, `(at:toOpenURLsWithScheme:)` (macOS 12) [AK]. Changing the default browser shows a system confirmation (unverified for other schemes) | unverified: W8 | none | none | `machine_app {action:"handlers", uti \| scheme}`; `{action:"set_handler", ...}` (setup) | P2 | W8 |
| A11 | LaunchServices registration and claims | A freshly built app in `/tmp` is not the registered handler; stale registrations from old builds | `lsregister -f <app>`; `lsregister -dump` filtered to the bundle id to show claimed types and schemes (CLI in CoreServices) | expected | none | none | `machine_app {action:"register", path}`; claims in `info` | P2 | W8 |
| A12 | Background and agent apps | Menu-bar apps (`LSUIElement`) have no Dock icon and no main window; "launched" means a status item appeared | `NSRunningApplication.activationPolicy` (`regular`, `accessory`, `prohibited`); wait for its `AXExtrasMenuBar` item instead of a window | expected | AX | Peekaboo `menubar` (MIT) | `machine_app {action:"launch", wait:"status_item"}`; `list` shows policy | P1 | W2 (extend) |
| A13 | Login items and background items | "Launch at login" toggles must really register; the "Background Items Added" notification | `SMAppService` status (macOS 13) for the app's own items; `sfltool dumpbtm` lists every Background Task Management record (root; passwordless sudo exists, `timezone.go`); remove through System Settings > General > Login Items (AX) | unverified: W7 | AX; root for sfltool | none | `machine_login_items {action:"list" \| "remove", app}` | P2 | W7 |
| A14 | The app's launchd jobs | Helper daemons and agents the app installs | `launchctl print gui/<uid>/<label>`, `launchctl list` | expected | none | none | `machine_app {action:"info"}` lists jobs | P3 | W8 |
| A15 | First-launch prompts: welcome windows, "Move to Applications?", update checks | They take focus over the window a check is about | Detected as foreign or new windows by the `attention` line; dismissal is recorded as setup under the docs/21 5.4 policy | expected | AX | Peekaboo `dialog` (MIT) | `machine_dialog` + policy | P1 | W2 (covered), W7 (policy list) |
| A16 | App identity: version, bundle id, signature, notarization, entitlements | "The build under test is the commit the coder pushed"; entitlements explain TCC failures | `Info.plist` (`CFBundleShortVersionString`, `CFBundleVersion`), `codesign -dv --entitlements - <app>`, `spctl --assess -vv`, `stapler validate` (all CLI) | yes (CLI present) | none | none | `machine_app {action:"info"}` | P1 | W8 |
| A17 | Not responding | Inputs into a hung app (#185) | AX probe with 250 ms timeout, CPU from `proc_pidinfo` | yes | AX | cua-driver (MIT) | `machine_processes` | P0 | W3 (covered) |
| A18 | Wrap a bare executable in an `.app` | `machine_exec` left an "exec" app in the Dock (#117) | Throwaway bundle with an `Info.plist` | yes | none | none | `machine_app {action:"launch", wrap:true}` | P1 | W2 (covered) |
| A19 | Launch time | "The app opens in under 2 s" | Time from `NSWorkspace.didLaunchApplicationNotification` to the first `AXWindowCreated` | expected | AX | none | `machine_app launch` reports `ready_ms` | P2 | W2 (add) |
| A20 | Several instances of one app | Apps that must stay single-instance | `open -n`; `OpenConfiguration.createsNewApplicationInstance` | expected | none | none | `machine_app {action:"launch", new_instance:true}` | P3 | W8 |
| A21 | Dock bounce and attention requests | "The app bounces when the export finishes in the background" | `NSApp.requestUserAttention` is not visible in AX (unverified); the Dock tile badge is (M4) | unverified: W5 | AX | none | not planned until a case needs it | P3 | none |

---

## 5. Menus and system UI chrome

A correction for docs/21 5.4: it says menu-bar extras are reachable as `app: "SystemUIServer"`.
Since macOS 11 most system status items (Control Center, Wi-Fi, Sound, Focus, Battery, the
clock) belong to the **ControlCenter** process, and notification banners to **NotificationCenter**
(both run as separate processes on the host). SystemUIServer keeps a few legacy extras. Wave 2
should look up extras by owning process, not assume SystemUIServer.

| ID | Capability | Why (Greenroom case) | How on macOS | VM | TCC | Prior art | Tool | P | Wave |
|---|---|---|---|---|---|---|---|---|---|
| M1 | Menu bar items by path | Menu-only commands | AX `AXMenuBar`, `AXMenuBarItem`, `AXMenuItem`, press | yes (helper reads menus today) | AX | Peekaboo (MIT) | `machine_menu {path}` | P0 | W2 (covered) |
| M2 | Context menus | Right-click actions | `AXShowMenu` or a right click at a hit-tested point | expected | AX | Peekaboo (MIT) | `machine_menu {ref, path}` | P0 | W2 (covered) |
| M3 | List menus with keyboard shortcuts | Discover an app's shortcuts | `AXMenuItemCmdChar`, `AXMenuItemCmdModifiers`, `AXMenuItemCmdVirtualKey`, `AXMenuItemCmdGlyph` [HIS] | yes | AX | Peekaboo (MIT) | `machine_menu {list:true}` | P1 | W2 (covered) |
| M4 | Dock: list items, press, context menu, badge, running dot, add, remove | "The badge shows 3 unread"; "the app stays in the Dock after quitting"; #117's stray Dock icon | Dock process AX: `AXList` of `AXApplicationDockItem`, `AXFolderDockItem`, `AXMinimizedWindowDockItem`, `AXTrashDockItem`, `AXIsApplicationRunning` [HIS]; badge text as the undocumented `AXStatusLabel` (unverified); add or remove through `com.apple.dock persistent-apps` + `killall Dock` (as `lean.sh` does) | unverified: W5 (the Dock is present; `lean.sh` rewrites it) | AX | Peekaboo `dock` (MIT) | `machine_dock {action:"list" \| "press" \| "menu" \| "add" \| "remove", item}` | P1 | W5 |
| M5 | Menu bar extras (status items) | Menu-bar apps are a common Greenroom target; Era A spent 52 calls on one popover | Third-party `NSStatusItem`s live in the owning app's `AXExtrasMenuBar` [HIS]; system items in ControlCenter (above). Items hidden by the macOS 26 Menu Bar settings or a narrow 1024-point screen must be reported as hidden, not missing (unverified: W2) | expected | AX | Peekaboo `menubar` (MIT), Ice (GPL-3.0, ideas only) | `machine_menu {extra:"name"}` | P0 | W2 (covered, fix the owner) |
| M6 | Control Center modules | Toggle Wi-Fi-like state, Focus, Stage Manager, display, sound | ControlCenter process AX: press the "Control Center" extra, then module toggles and sliders | unverified: W6 | AX | macos-automator-mcp (MIT) | `machine_control_center {module, action, value}` (setup) | P2 | W6 |
| M7 | Notifications: read, click, act, dismiss, clear all; open Notification Center | "A notification appears when the timer ends and its Snooze action works"; #195's banner over the app | Banners and alerts are windows of the NotificationCenter process: read title and body through AX, press, use its custom AX actions (Close, Show, action buttons), clear. Open the center by pressing the clock (ControlCenter). **The lean profile turns banners off** (`lean.go`), so a notification test needs banners on for that app (System Settings > Notifications, or the private `com.apple.ncprefs` store) | unverified: W7 | AX | Peekaboo (MIT), macos-automator-mcp (MIT) | `machine_notifications {action:"list" \| "press" \| "action" \| "dismiss" \| "clear_all" \| "open_center"}` | P1 | W7 |
| M8 | Notification history (delivered even when not shown) | Prove a notification was posted when banners are off | `private` usernoted database under `~/Library/Group Containers/group.com.apple.usernoted/` (private schema, protected; needs Full Disk Access, which the image does not grant as far as documented) | unverified: W7 | Full Disk Access | none | `machine_notifications {action:"history"}` (research) | P2 | W7 |
| M9 | Widgets (desktop and Notification Center) | Apps that ship WidgetKit widgets | AX of the widget host; adding a widget is a drag from the widget gallery. **Widgets are off in the lean profile** | unverified: W9 | AX | none | `machine_widgets {action:"list" \| "add"}` (research) | P3 | W9 |
| M10 | Spotlight | "The app's documents are found by Spotlight"; launching by name like a user | cmd+space, type, read results (AX of the Spotlight process), return. `mdfind`/`mdls`/`mdimport` for the index. **Spotlight indexing is off in the lean profile**, so content searches need `mdutil -i on` first | unverified: W5 | AX, PostEvent | Peekaboo (MIT) | `machine_spotlight {query, open?:index}` | P2 | W5 |
| M11 | Launchpad / Apps | Rarely needed; launch by NSWorkspace instead | Launchpad is gone on macOS 26 and later (`/System/Applications/Apps.app` replaces it; checked on the host) | expected | none | none | none (use A1) | P3 | none |
| M12 | Services menu | Apps that provide or consume Services | App menu > Services by AX path; `NSPerformService(name, pasteboard)` (public AppKit) invokes one directly; `/System/Library/CoreServices/pbs -update` refreshes the list | expected | AX | none | `machine_menu {path:["App","Services","..."]}`; `machine_services {name, text}` (setup) | P3 | W8 |
| M13 | Share sheet | "Share > Copy link works" | File > Share or a share button opens an `NSSharingServicePicker` popover; items by AX. No iCloud account, so AirDrop, Messages and Mail items are not usable | unverified: W8 | AX | none | `machine_menu` + `machine_select` | P3 | W8 |
| M14 | Help menu search | Find a menu item by name without knowing its path | Help > search field lists matching items; our own menu walk (`machine_menu {find:"Export"}`) is faster and deterministic | yes (menus readable) | AX | none | `machine_menu {find}` | P2 | W2 (add) |
| M15 | Toolbar items and overflow | Items hidden behind the `>>` chevron in a narrow window look missing | `AXToolbar` children, `AXOverflowButton` [HIS] | expected | AX | none | snapshot flags items as `in overflow` | P1 | W1/W2 (add) |
| M16 | Menu bar hidden (auto-hide, full screen) | `machine_menu` must reveal it before pressing | `_HIHideMenuBar` in NSGlobalDomain; move the pointer to the top edge; AX menu bar stays readable even when hidden (expected) | expected | AX | none | handled inside `machine_menu` | P2 | W2 (add) |
| M17 | App switcher (cmd+Tab) | "An accessory app does not appear in cmd+Tab" | Hold cmd, press Tab, read `AXProcessSwitcherList` [HIS] in the Dock, release | unverified: W5 | AX, PostEvent | alt-tab-macos (GPL-3.0, ideas only) | `machine_space {action:"app_switcher"}` | P3 | W5 |
| M18 | Emoji and Character Viewer | Emoji entry in text fields | ctrl+cmd+space or Edit > Emoji & Symbols; but typing Unicode directly (I9) is simpler and exact | expected | AX | none | `machine_type` with Unicode | P3 | W9 |
| M19 | Touch Bar | Apps with Touch Bar items | No Touch Bar in a VM and no simulator without Xcode | **no** | n/a | n/a | none; state "not testable" | P3 | none |

---

## 6. Input

| ID | Capability | Why (Greenroom case) | How on macOS | VM | TCC | Prior art | Tool | P | Wave |
|---|---|---|---|---|---|---|---|---|---|
| I1 | Hover and tooltips | Buttons that appear only on hover (list rows); tooltip text as the only label of an icon button | Post `kCGEventMouseMoved` (a warp with `CGWarpMouseCursorPosition` posts no event, so tracking areas never fire [CG]); dwell up to 3 s; read `AXHelp` and watch `AXHelpTagCreated` [HIS]; diff the tree for hover-revealed elements | expected (moves are posted today) | PostEvent, AX | Peekaboo (MIT) | `machine_hover {ref, dwell_ms}` returns the tooltip and what appeared | P1 | W9 |
| I2 | Click variants: left, right, middle, double, triple | Double-click to open, triple-click to select a paragraph | `kCGEventLeftMouseDown`/`Right`/`OtherMouseDown` [CG]; **double and triple clicks need `kCGMouseEventClickState` set to 2 and 3 on the second and third pairs**, or apps see separate single clicks; spacing below the double-click interval (Y24) | yes (helper posts clicks today) | PostEvent | Hammerspoon (MIT) | `machine_press {count, button}` | P0 | W1 (covered) |
| I3 | Modifier clicks | cmd-click to add to a selection, shift-click for a range, opt-click alternates | Event flags on the mouse events | yes | PostEvent | all | `machine_press {mods}` | P1 | W1 (covered) |
| I4 | Press and hold | Safari's back-button history, the green button's tile menu, long-press UIs | Mouse down, hold, mouse up; posted as one bounded op | expected | PostEvent | Anthropic `left_mouse_down/up` (MIT reference) | `machine_press {hold_ms}` | P2 | W9 |
| I5 | Drag with modifiers, spring-loaded folders, drags that hover | opt-drag copies, cmd-drag moves between volumes, hovering a folder opens it | `kCGEventLeftMouseDragged` path with flags and a dwell point | expected | PostEvent | Silica (MIT) | `machine_drag {mods, via:[points], hold_ms}` | P1 | W2 (base), W9 (mods, dwell) |
| I6 | Drag and drop between apps, to Finder and the Desktop | "Drop a PNG from Finder onto the canvas imports it"; drag-to-Applications installs (F8) | A real pointer drag across windows; the source app builds the drag pasteboard from the events. Verify the drop by the destination's state (the file exists, the canvas has a layer) | unverified: W2 | PostEvent | none | `machine_drag {from: ref in app A, to: ref in app B}` | P1 | W2 (test) |
| I7 | Drag a file from the host into the guest | Users drag files into apps | No host UI in Greenroom; copy with `machine_sync` then drag from Finder (I6) | no (by design) | n/a | n/a | none | P3 | none |
| I8 | Scroll: lines, pixels, horizontal, continuous with phases and momentum | Pull to refresh and elastic bounce need trackpad-style scrolling; wheel lines skip content | `CGEventCreateScrollWheelEvent2` [CG]; trackpad-style by setting `kCGScrollWheelEventIsContinuous` (88), `kCGScrollWheelEventScrollPhase` (99) and `kCGScrollWheelEventMomentumPhase` (123) (public field numbers in `CGEventTypes.h`); shift+wheel for horizontal | expected | PostEvent | Hammerspoon (MIT) | `machine_scroll {style:"wheel" \| "trackpad", momentum:bool}` | P2 | W9 |
| I9 | Type text: by Unicode string or by key codes | Unicode typing is exact but bypasses key equivalents and IMEs; key-code typing is what a keyboard does | `CGEventKeyboardSetUnicodeString` or virtual key codes for the current layout; read back (docs/21) | yes (helper types today) | PostEvent | all | `machine_type {via:"unicode" \| "keys"}` | P0 | W1 (covered; add `via`) |
| I10 | Shortcuts including fn/globe | fn+F full screen, fn+ctrl+arrow tiling, fn+E emoji | `kCGEventFlagMaskSecondaryFn`; tart attaches `VZMacKeyboardConfiguration`, which "supports Apple-specific features such as the globe key" [VZ][tart]; whether the system acts on a **synthesized** fn from inside the guest is unverified | unverified: W9 | PostEvent | none | `machine_key {mods:["fn", ...]}` | P2 | W9 |
| I11 | Media and system keys (volume, brightness, play/pause, Mission Control) | "The app reacts to the play/pause key" | `NX_SYSDEFINED` events (`NSEvent.otherEvent` subtype 8 with `NX_KEYTYPE_*` from IOKit's `ev_keymap.h`) posted as CGEvents | unverified: W9 | PostEvent | Hammerspoon `hs.eventtap.event.newSystemKeyEvent` (MIT) | `machine_key {system_key:"play"}` | P3 | W9 |
| I12 | Dead keys and compose sequences | opt+e then e gives é; text fields that mishandle marked text | Key-code sequences under a layout; read back the field | expected | PostEvent | none | `machine_type {via:"keys"}` | P2 | W9 |
| I13 | Input sources, layouts and IMEs (Japanese, Pinyin, Korean) | CJK input breaks many text fields at commit; layout-specific shortcuts | Text Input Sources API in HIToolbox `TextInputSources.h` (public): `TISCreateInputSourceList`, `TISEnableInputSource`, `TISSelectInputSource`; the candidate window is another process's window; type through the IME with key codes and commit with return | unverified: W9 | PostEvent, AX | Hammerspoon `hs.keycodes` (MIT) | `machine_input_source {action:"list" \| "enable" \| "select", id}` | P2 | W9 |
| I14 | Key hold and auto-repeat | Holding an arrow key scrolls; games | Key down, repeated key-downs with `kCGKeyboardEventAutorepeat` set, key up; `InitialKeyRepeat`/`KeyRepeat` defaults | expected | PostEvent | none | `machine_key {hold_ms}` | P3 | W9 |
| I15 | Secure text fields and secure input | Password fields; typing into them and never reading them back | Secure input is on while one is focused (`IsSecureEventInputEnabled()`, Carbon); taps cannot read keystrokes then, but a trusted poster can still type (unverified on 26); values are redacted (docs/21 6) | unverified: W1 | PostEvent | none | `machine_type` into `AXSecureTextField`, redacted evidence | P1 | W1 (covered, test) |
| I16 | Trackpad gestures: pinch, rotate, smart zoom, swipe between pages, swipe between Spaces | Maps and image apps zoom by pinch; browsers go back by two-finger swipe | **No public API synthesizes gestures.** `NSEventTypeMagnify`, `Rotate`, `Swipe`, `SmartMagnify`, `Gesture` exist to be received only [AK]. `private`: CGEvents of type 29 with undocumented fields, as Hammerspoon's `eventtap/TouchEvents.c` does (**GPL-2.0, do not copy**). The VM has a trackpad device (`VZMacTrackpadConfiguration`, which carries "multi-touch trackpad gestures" from `VZVirtualMachineView` [VZ][tart]), but Greenroom runs `--no-graphics`, so nothing on the host drives it. Use equivalents: cmd+=/cmd+- zoom, cmd+[ back, ctrl+arrow for Spaces, AX `AXIncrement` | unverified: W9 (spike: do private gesture events reach AppKit apps and the Dock?) | PostEvent | Hammerspoon (MIT, but TouchEvents is GPL-2.0) | `machine_gesture {kind, ref, amount}` (research; effect must be read back) | P3 | W9 |
| I17 | Force click and pressure | Force click for Look Up and Quick Look; pressure-sensitive controls | No API to synthesize `NSEventTypePressure`; use ctrl+cmd+D (Look Up) or space (Quick Look) | **no** for pressure | n/a | none | none; equivalents only | P3 | none |
| I18 | Text selection and reading ranges | "Select the second paragraph and bold it"; clicking a word inside a text view | Set `AXSelectedTextRange` (settable on most text views), read `AXSelectedText`; parameterized `AXStringForRange`, `AXBoundsForRange` (screen rect of characters, so a word can be clicked), `AXRangeForPosition`, `AXLineForIndex` [HIS] | expected | AX | AXorcist (MIT) | `machine_text {ref, action:"select" \| "read" \| "bounds", range \| match}` | P1 | W9 |
| I19 | Rich text attributes | "The word is bold and links to the URL" | Parameterized `AXAttributedStringForRange`, `AXRTFForRange` [HIS] | expected | AX | none | `machine_text {action:"attributes", range}` | P2 | W9 |
| I20 | Find and replace inside apps | Editors' find bars and panels | cmd+F then the `AXSearchField`; Edit > Find > Find and Replace; composed from existing tools | expected | AX | none | composed (`machine_key` + `machine_type`) | P3 | none |
| I21 | Clipboard with types: plain, RTF, HTML, image, file URLs, custom UTIs; several items; watch changes | "Copy puts both an image and a URL on the pasteboard"; set up a paste test | `NSPasteboard.general`: `types`, `data(forType:)`, `pasteboardItems`, `changeCount` (no change notification exists: poll) | expected; see section 14 for pasteboard privacy and host sync | none (see 14) | Peekaboo `clipboard` (MIT) | `machine_clipboard {action:"get" \| "set" \| "clear" \| "types", type, data \| path}` and `machine_wait_for {target:"clipboard", state:"changes"}` | P1 | W2 (text), W9 (types, watch) |
| I22 | Dictation | Voice input features | Needs a microphone. tart feeds the **host's** microphone into the guest when audio is on (section 13), so dictation would hear the host's room: unsafe. Test voice features with an audio file through a virtual input device (T5) | **no** (unsafe) | Microphone | none | none | P3 | none |
| I23 | Full keyboard navigation | "Every control is reachable with Tab"; a11y checks | `AppleKeyboardUIMode` (2 or 3) in NSGlobalDomain; press Tab and read `AXFocusedUIElement` each time | expected | AX | none | part of `machine_audit {checks:["keyboard"]}` | P2 | W9 |
| I24 | Input to a background app | Peekaboo types into non-frontmost apps | `CGEventPostToPid` [CG]. **Not used**: Greenroom tests the path a user takes, so it activates first (docs/21 5.3) | yes | PostEvent | Peekaboo (MIT) | none | P3 | none |
| I25 | Pointer location and cursor shape | "The cursor becomes a resize arrow over the splitter" | Location from `CGEvent(source:nil).location`; the cursor image is not exposed for other apps through public API (unverified) | partial | none | none | reported in the snapshot header; shape not planned | P3 | none |

---

## 7. Dialogs, prompts and permissions

Every system prompt comes from a known process, which is what the `attention` line should use
to classify it: **UserNotificationCenter** and **universalAccessAuthWarn** (permission alerts),
**SecurityAgent** (passwords, keychain), **CoreServicesUIAgent** (Gatekeeper, "downloaded from
the Internet"), **NotificationCenter** (banners and notification-permission alerts), **replayd**
(screen-capture alerts, ADR 0013). All five run on the host as separate processes; the mapping
from prompt to process on macOS 26 is unverified per prompt and is wave 7's first task.

| ID | Capability | Why (Greenroom case) | How on macOS | VM | TCC | Prior art | Tool | P | Wave |
|---|---|---|---|---|---|---|---|---|---|
| D1 | App alerts and sheets | The bench's `dialog` infra case | AX `AXSheet`, `AXDialog`, `AXDefaultButton`, `AXCancelButton` [HIS] | yes | AX | Peekaboo `dialog` (MIT) | `machine_dialog` | P0 | W2 (covered) |
| D2 | Open and save panels: path, name, new folder, format pop-up, hide extension, expanded or collapsed | Save-as flows; "Export as PNG" | cmd+shift+G, type the folder, name field, `machine_select` on the format pop-up, the disclosure button; for sandboxed apps the panel is hosted by `com.apple.appkit.xpc.openAndSavePanelService`, reachable through the app's AX tree (Peekaboo handles it) | expected; sandboxed hosting unverified: W2 | AX | Peekaboo `DialogService+FileDialog*` (MIT) | `machine_file_dialog {path, name, format?, new_folder?}` | P0 | W2 (covered, extend) |
| D3 | Print dialog, Save as PDF | "Printing produces two pages" | File > Print sheet, PDF menu > Save as PDF, then D2. No printers in the VM: PDF only, or a CUPS queue that writes to a file (`lpadmin`, root) | expected (PDF); printer queue unverified: W8 | AX | none | `machine_print {to_pdf: path}` returns page count | P2 | W8 |
| D4 | Color and font panels | Apps with color wells and font pickers | `NSColorPanel`: its tabs by AX; the hex field in the sliders pane via `machine_set_value`; font panel (cmd+T) lists family, typeface, size | expected | AX | none | `machine_select` / `machine_set_value` | P3 | W9 |
| D5 | Admin password prompts ("X wants to make changes") | Installers and privileged helpers; SMJobBless flows | SecurityAgent window; password comes from the daemon's config (the base image's admin account), **never from the model**, typed into the secure field, redacted in evidence. Whether SecurityAgent accepts synthesized keystrokes on 26 is unverified | unverified: W7 | PostEvent, AX | none | `machine_auth {action:"inspect" \| "authenticate" \| "cancel"}` | P1 | W7 |
| D6 | Touch ID | Apps offering Touch ID unlock | No biometric device in Virtualization.framework (no such class in the SDK headers [VZ]), so `LAContext` biometry is unavailable and apps fall back to the password path. Touch ID UI cannot be tested | **no** | n/a | none | test the fallback with D5; report "Touch ID not available in a VM" | P2 | W7 |
| D7 | Keychain prompts ("X wants to use your confidential information") | Apps storing secrets; the prompt blocks the flow | Avoid in setup: `security add-generic-password -T <app>`, `security set-generic-password-partition-list`, `security unlock-keychain`; handle when testing the prompt itself with D5's secret route | expected (security CLI) | none | none | `machine_keychain {action:"list" \| "add" \| "delete" \| "unlock"}` (setup) | P2 | W7 |
| D8 | TCC privacy prompts: Accessibility, Screen Recording, Camera, Microphone, Files and Folders (Desktop, Documents, Downloads, removable and network volumes), Full Disk Access, Automation, Input Monitoring, Location, Contacts, Photos, Calendars, Reminders, Local Network, Bluetooth, App Management, Developer Tools, Speech Recognition, Media | "The app asks for Screen Recording and handles a denial"; the verifier must never approve one unless the task says so (docs/21 5.4) | Control the state before the prompt: **grant or deny** by writing TCC.db rows with the app's code requirement (`csreq`), as the image already does for its own binaries (SIP is off, docs/09); **reset to "ask"** with `tccutil reset <Service> <bundle id>`; read status from TCC.db. Handle the prompt itself with `machine_dialog` under the policy. Local Network and Bluetooth may not be TCC.db services (unverified) | expected for TCC.db (the image writes it today [img]); per-service prompts unverified: W7 | root for the system TCC.db | none | `machine_permissions {action:"list" \| "grant" \| "deny" \| "reset", service, app}` (setup) | P1 | W7 |
| D9 | Automation (Apple Events) prompts | "X wants access to control Finder" | Same as D8 with `kTCCServiceAppleEvents` and the target as `indirect_object_identifier` (`base.sh` writes such rows) | yes for writing rows [img] | root | none | `machine_permissions {service:"automation", app, target}` | P1 | W7 |
| D10 | Gatekeeper and quarantine | "The notarized DMG opens without a warning"; an unsigned build shows the right dialog | Simulate a download: `xattr -w com.apple.quarantine "0083;<hex time>;Safari;<uuid>" <app>` (files from `curl` carry no quarantine, ADR 0021); `spctl --assess -vv`; the dialog comes from CoreServicesUIAgent; since macOS 15 an unsigned app needs System Settings > Privacy & Security > Open Anyway instead of ctrl-click Open (unverified on 26). Notarization checks need the network | unverified: W7 | AX | none | `machine_files {action:"quarantine", path, on:bool}` + `machine_dialog` + `machine_app info` | P1 | W7 |
| D11 | Notification permission prompt | "The app asks to send notifications on first launch" | `UNUserNotificationCenter.requestAuthorization` shows an alert in NotificationCenter with Allow and Don't Allow (unverified on 26) | unverified: W7 | AX | none | `machine_notifications {action:"permission", allow:bool}` | P1 | W7 |
| D12 | Update, "What's New", Tips, Setup Assistant popups | #195: a "what's new" notification over the app | Prevent in the image (`softwareupdated` disabled in `base.go`; Setup Assistant prompts and Software Update off in `lean.sh`); detect in `attention`; dismiss as setup | yes for prevention [img]; residual popups seen (#195) | AX | none | image + `attention` + `machine_dialog` | P0 | W2 (covered), image |
| D13 | Unsaved-changes and document sheets | Quit and close flows | `machine_dialog` by button name | yes | AX | Peekaboo (MIT) | `machine_dialog` | P0 | W2 (covered) |
| D14 | "Quit unexpectedly" crash dialog | Crash UX tests | Off in the image (`com.apple.CrashReporter DialogType none`, `base.sh`); turn on for a run that tests it | yes [img] | none | none | `machine_settings {set:{crash_dialog:true}}` | P3 | W6 |
| D15 | Login window | Apps with login-window plugins or fast user switching | The guest agent runs in the logged-in user's GUI session; at the login window there is no agent and no capture approval for that session. Use `machine_reboot` (PR #204) with autologin | **no** (unsafe) | n/a | none | none | P3 | none |
| D16 | Lock screen and screen saver | "The app pauses when the screen locks" | Off in the image (`desktopprefs.go`); unlocking means typing the password into loginwindow's secure field and a black capture meanwhile. Posting `com.apple.screenIsLocked` as a distributed notification fakes the event without locking: allowed only as a labelled simulation | **no** for a real lock (unsafe); simulation expected | none | none | `machine_simulate {event:"screen_locked"}` (labelled, setup) | P3 | W9 |
| D17 | Other system alerts: Keyboard Setup Assistant, low disk, Background Items Added | A new USB keyboard (the VM has `VZUSBKeyboardConfiguration` [tart]) may open Keyboard Setup Assistant at first boot | Detect in `attention`; the dialog gate (ADR 0018) catches them at image build | expected | AX | none | `attention` | P2 | W7 (classifier) |
| D18 | Configuration profiles (MDM) | Apps that read managed preferences | `profiles` cannot install silently since macOS 11; installing needs System Settings > Device Management approval (AX + D5). Managed preferences under `/Library/Managed Preferences` written as root may work with SIP off (unverified) | unverified: W7 | AX, root | none | `machine_profiles {action:"install" \| "remove" \| "list"}` (setup) | P3 | W7 |

---

## 8. Finder and files

| ID | Capability | Why (Greenroom case) | How on macOS | VM | TCC | Prior art | Tool | P | Wave |
|---|---|---|---|---|---|---|---|---|---|
| F1 | Open a Finder window at a path | Verify what a user sees in Finder after an export | `open <dir>`; Finder AppleScript `make new Finder window to POSIX file` (Automation row for Finder exists [img]) | yes (AppleEvents row) | AppleEvents | macos-automator-mcp (MIT) | `machine_finder {action:"open", path}` | P2 | W8 |
| F2 | Reveal and select items | "Show in Finder selects the exported file" | `NSWorkspace.activateFileViewerSelecting([urls])` (public); read Finder's selection by AppleScript | yes | AppleEvents | none | `machine_finder {action:"reveal", paths}`; `{action:"selection"}` | P2 | W8 |
| F3 | Rename, move, copy, trash, empty trash through Finder | Verify Finder-level behaviour (file coordination, document apps noticing renames) | Finder UI by AX for the action under test; `NSWorkspace.recycle` for trash in setup; empty trash by Finder AppleScript (it asks to confirm) | expected | AX, AppleEvents | none | `machine_finder {action:"rename" \| "move" \| "copy" \| "trash" \| "empty_trash"}` | P2 | W8 |
| F4 | Get Info | Kind, size, "Open with" as a user sees them | cmd+I, read the Info window by AX; `mdls` for metadata | expected | AX | none | `machine_finder {action:"info", path}` | P3 | W8 |
| F5 | Quick Look previews and thumbnails | Apps shipping Quick Look or thumbnail extensions | `qlmanage -p <file>` opens a preview panel; `qlmanage -t -s 256 -o <dir> <file>` renders a thumbnail; space bar in Finder | expected | ScreenCapture | none | `machine_quicklook {path, mode:"preview" \| "thumbnail"}` returns a capture | P2 | W8 |
| F6 | Finder tags | "The app tags exported files Red" | `URLResourceValues.tagNames` (read and write on macOS), stored in `com.apple.metadata:_kMDItemUserTags` | expected | none | none | `machine_files {action:"tags", path, set?}` | P3 | W8 |
| F7 | Desktop icons | Drop to Desktop; "the export lands on the Desktop" | Finder's desktop window by AX (items in a scroll area); Show Desktop (S9) first | unverified: W8 | AX | none | `machine_finder {action:"desktop"}` | P3 | W8 |
| F8 | Mount a DMG and install by dragging to Applications | The coding agent ships a DMG; the first-run experience is the product | `hdiutil attach <dmg>` (the Finder window opens unless `-nobrowse`), handle a licence agreement, `machine_drag` the app onto the Applications alias, wait for the copy, `hdiutil detach`; verify `/Applications/X.app` and its signature (A16) | expected (hdiutil present) | AX, PostEvent | none | `machine_install {path, method:"finder_drag" \| "copy"}` | P1 | W8 |
| F9 | PKG install | Installer UX and postinstall scripts | Setup: `sudo installer -pkg <pkg> -target /`; UX test: Installer.app pages by AX plus D5 for the password | expected for CLI; UI unverified: W8 | AX; root | none | `machine_install {path, method:"installer_ui" \| "cli"}` | P2 | W8 |
| F10 | Zip and unzip | "Exported archive opens in Archive Utility" | `ditto -c -k --keepParent` and `ditto -x -k` (keep xattrs and quarantine) vs `unzip` (drops them); `open x.zip` for Archive Utility | yes (CLI present) | none | none | `machine_files {action:"zip" \| "unzip"}` | P2 | W8 |
| F11 | File metadata: stat, xattrs, quarantine flag, Spotlight attributes, ACLs | Evidence for "the exported file is a 2-page PDF with the right creator" | `stat`, `xattr -l`, `mdls`, `ls -le`; as a typed observation step a verdict can cite, instead of free-form `machine_exec` | yes | none | none | `machine_files {action:"stat" \| "xattr" \| "metadata", path}` | P1 | W8 |
| F12 | Wait for a file to appear or change | Exports and autosaves without polling | FSEvents (`FSEventStreamCreate`, public CoreServices) in the agent, bounded like other waits | expected | none (paths the agent can read) | Hammerspoon `hs.pathwatcher` (MIT) | `machine_wait_for {target:{file: path}, state:"exists" \| "changes"}` | P1 | W8 |
| F13 | Which files did the app touch | "The app writes only inside its container" | `fs_usage -w -f filesys <pid>` (root), bounded to seconds | expected | root | none | `machine_files {action:"trace", app, seconds}` | P2 | W8 |
| F14 | iCloud Drive and File Provider | Sync features | No Apple ID in a disposable VM; `fileproviderctl` can inspect a File Provider extension the app ships (unverified) | **no** for iCloud | n/a | none | none | P3 | none |
| F15 | Removable and network volumes | "The app handles an external disk being ejected" | A mounted disk image behaves as an external volume (`hdiutil attach`, `hdiutil detach -force` to simulate yanking); Virtualization.framework can hot-plug USB mass storage from the host (`VZUSBMassStorageDeviceConfiguration`, macOS 13; attach through `VZUSBController`, macOS 15 [VZ]), which tart does not expose | expected for disk images | Files and Folders (removable volumes) for the app | none | `machine_files {action:"mount" \| "eject", image}` | P3 | W8 |
| F16 | Aliases, symlinks, security-scoped bookmarks | "Recent files reopen after relaunch in a sandboxed app" | Composed: create with `ln -s`/Finder alias, relaunch (A8), open recent (menu) | expected | none | none | composed | P3 | none |

---

## 9. System state and settings

Every setting a tool changes goes into a per-run **settings ledger** (old value, new value, how it
was read back) so the daemon restores it at run end and the report lists what was different
from the image. Settings are setup steps.

| ID | Capability | Why (Greenroom case) | How on macOS | VM | TCC | Prior art | Tool | P | Wave |
|---|---|---|---|---|---|---|---|---|---|
| Y1 | Appearance: light, dark, auto | Dark mode checks on every UI task | System Events `tell appearance preferences to set dark mode to true` (the image has an AppleEvents row for System Events [img]); read back `defaults read -g AppleInterfaceStyle` and a probe's `NSApp.effectiveAppearance`. `defaults write -g AppleInterfaceStyle` alone does not notify running apps | expected | AppleEvents | macos-automator-mcp (MIT) | `machine_settings {set:{appearance:"dark"}}` | P1 | W6 |
| Y2 | Accent and highlight color | Apps that tint with the accent color | `AppleAccentColor`, `AppleHighlightColor` in NSGlobalDomain, then the distributed notification `AppleColorPreferencesChangedNotification` (unverified that apps repaint) | unverified: W6 | none | none | `machine_settings {set:{accent:"purple"}}` | P3 | W6 |
| Y3 | Reduce motion, reduce transparency, increase contrast, differentiate without color, invert, grayscale | Accessibility checks; reduce motion also shortens Mission Control and full-screen animations for us | `com.apple.universalaccess` keys (`reduceMotion`, `reduceTransparency`, `increaseContrast`, `differentiateWithoutColor`); writing that domain is restricted to privileged clients (unverified on 26; fallback: System Settings > Accessibility by AX); read back with `NSWorkspace.accessibilityDisplayShouldReduceMotion` and siblings (public getters). Grayscale and invert: `private` `CGDisplayForceToGray`, `CGDisplaySetInvertedPolarity` | unverified: W6 | AX (UI route) | macos-automator-mcp (MIT) | `machine_settings {set:{reduce_motion:true, ...}}` | P1 | W6 |
| Y4 | Display resolution and scaling | Layout at small and large sizes; HiDPI (2x) rendering bugs | The VM's size is set before boot (`tart set --display WxH[pt\|px]`, `--display-refit`); in the guest `CGDisplayCopyAllDisplayModes` and `CGConfigureDisplayWithDisplayMode` (public) change among the modes the virtual display offers (which ones is unverified); the host can call `VZMacGraphicsDisplay` reconfigure (macOS 14) as tart's refit does | unverified: W6 | none | Hammerspoon `hs.screen` (MIT) | `machine_display {action:"list" \| "set", mode}`; `machine_create {display}` | P2 | W6 |
| Y5 | Multiple displays | "The window opens on the display it was on" | **Virtualization.framework supports one display for macOS guests**: `VZMacGraphicsDeviceConfiguration.displays`: "Maximum of one display is supported." [VZ] | **no** | n/a | n/a | none; test display logic with injected `NSScreen` data in unit tests | P3 | none |
| Y6 | Night Shift, True Tone, brightness | Rarely relevant to app behaviour | `private` CoreBrightness `CBBlueLightClient`; a virtual display has no brightness or True Tone | **no** (meaningless on a virtual display) | n/a | none | none | P3 | none |
| Y7 | Language, region, formats, first weekday, 24-hour time, units | Localization and date/number formatting checks | Per app (preferred): A2's argument domain. System-wide: `defaults write -g AppleLanguages -array fr-FR en`, `AppleLocale`, `AppleICUForce24HourTime`, `AppleFirstWeekday`, `AppleMeasurementUnits`; apps must relaunch, some keys need a logout; `languagesetup` (root) for the system language | expected | none | macos-automator-mcp (MIT) | `machine_settings {set:{language, region, clock24}}` | P1 | W6 |
| Y8 | Time zone | "The meeting shows at 9:00 in Tokyo" | `sudo systemsetup -settimezone` (the daemon already does this, `timezone.go`) | yes [img] | root | none | `machine_settings {set:{timezone}}` | P2 | W6 |
| Y9 | Clock control ("time travel") | Trials that expire, date rollovers, "due tomorrow" | `sudo systemsetup -setusingnetworktime off`, `sudo date <MMDDhhmm[[CC]YY]>`, restore network time after. **A clock jump resets replayd's capture approvals** (ADR 0013), so the tool must rewrite them; TLS validation breaks for dates far off. Per-app fake time for hardened or sandboxed apps is not possible (`DYLD_INSERT_LIBRARIES` is blocked); the coder's own debug build can take an injected clock | unverified: W6 | root | none | `machine_clock {action:"set" \| "advance" \| "restore", to}` | P2 | W6 |
| Y10 | Menu bar clock format | Screenshots with a fixed clock | `com.apple.menuextra.clock` keys | expected | none | none | `machine_settings` | P3 | W6 |
| Y11 | Accessibility: VoiceOver, Zoom, text size, cursor size, sticky keys | "VoiceOver reads the button as 'Split bill'" | VoiceOver on and off with cmd+F5; its AppleScript (`tell application "VoiceOver" to content of last phrase`) after enabling `SCREnableAppleScript` in its defaults domain (unverified on 26). **VoiceOver speech plays on the host's speakers** while tart's audio is on (section 13) | unverified: W9 | AppleEvents | none | `machine_voiceover {action:"on" \| "off" \| "last_phrase" \| "next"}` | P2 | W9 |
| Y12 | Sound: volume, mute, output device; did the app play a sound | "A sound plays when the timer ends" | `osascript -e "set volume output volume 30"`; CoreAudio `kAudioHardwareServiceDeviceProperty_VirtualMainVolume` (public AudioToolbox); **evidence of playback** from ScreenCaptureKit audio (`SCStreamConfiguration.capturesAudio`, public) as an RMS level over a window of time | unverified: W6 | ScreenCapture | none | `machine_audio {action:"volume" \| "mute" \| "level", seconds}` | P2 | W6 |
| Y13 | Network conditions: offline, slow, lossy, DNS failure, proxy, blocked host | "The app shows an offline banner"; "retries on timeout" | Guest side (root through passwordless sudo): `networksetup -setnetworkserviceenabled <svc> off`; `dnctl pipe 1 config bw 1Mbit/s delay 200 plr 0.01` with a `pfctl` dummynet anchor (what Network Link Conditioner does, without Xcode's additional tools); `/etc/hosts` or `networksetup -setdnsservers`; `-setwebproxy`/`-setsecurewebproxy`/`-setautoproxyurl`. Host side: tart `--net-softnet-block` (needs a VM restart). `tart exec` does not use the guest's network, but **`machine_sync` (rsync over ssh) does**, so offline mode must warn and restore | unverified: W6 | root | none | `machine_network {action:"offline" \| "online" \| "throttle" \| "block_host" \| "proxy" \| "reset"}` | P2 | W6 |
| Y14 | Battery, low power, power source | "The app pauses sync on battery" | A VM has no battery; `pmset -g batt` should show AC only (expected); low power mode on a desktop Mac is unverified. Power-source code paths need injection in the app | **no** for battery | n/a | none | none | P3 | none |
| Y15 | Sleep and wake | "The app reconnects after wake" | `pmset sleepnow` in the guest: whether a Virtualization.framework guest sleeps and wakes cleanly is unverified, and the channel and `tart exec` may stall. Host-side `VZVirtualMachine.pause()` is not a guest sleep (no notifications). The image disables sleep because a sleeping display turns captures black (measured, `desktopprefs.go`) | unverified: W6 (spike; unsafe until then) | root | none | `machine_power {action:"sleep_wake", seconds}` (research) | P3 | W6 |
| Y16 | Users and fast user switching | Multi-user apps | Creating a user is possible (`sysadminctl -addUser`), but switching moves the console away from the session our agent lives in: blind and unsafe. Run CLI parts as another user with `sudo -u` | **no** for GUI | n/a | none | none | P3 | none |
| Y17 | Open a System Settings pane | "The app's 'Open Settings' button lands on Privacy > Screen Recording" | `open "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture"` (legacy ids still resolve) or the extension ids (`com.apple.settings.PrivacySecurity.extension`); then AX in System Settings (SwiftUI sidebar) | expected | AX | macos-automator-mcp (MIT) | `machine_settings {open_pane:"privacy/screen_recording"}` reports the pane that opened | P1 | W6 |
| Y18 | Defaults: read, write, delete, including sandboxed containers | Seed an app's preferences; reset one flag | `defaults` (it routes sandboxed apps into their container, as `base.sh` notes for Safari); never edit plists directly (cfprefsd caches); a running app sees a write only when it re-reads | yes [img] | none | none | `machine_defaults {domain, key, action:"read" \| "write" \| "delete", value, type}` (setup) | P1 | W6 |
| Y19 | Focus and Do Not Disturb | "Notifications are silenced during Focus"; Focus filters (App Intents) | Control Center's Focus module by AX; `shortcuts run` a prepared "Set Focus" shortcut (creating one headless is not supported, X8); state lives in a private store under `~/Library/DoNotDisturb` | unverified: W6 | AX | none | `machine_settings {set:{focus:"do_not_disturb"}}` | P3 | W6 |
| Y20 | Screen Time | Parental-control-aware apps | Needs account setup; out of scope | **no** (out of scope) | n/a | none | none | P3 | none |
| Y21 | Dock and menu bar preferences | More usable screen; a hidden Dock changes window frames | `com.apple.dock autohide`, `tilesize`; `_HIHideMenuBar` | expected | none | macos-automator-mcp (MIT) | `machine_settings {set:{dock_autohide:true}}` | P3 | W6 |
| Y22 | Pointer preferences: natural scrolling, double-click interval, tracking speed | Our double clicks must fall inside the interval; scroll direction flips `machine_scroll` | `com.apple.swipescrolldirection`, `com.apple.mouse.doubleClickThreshold` in NSGlobalDomain (read by the agent to space clicks) | expected | none | none | image defaults + read by the agent | P2 | W6 |
| Y23 | Keyboard preferences: repeat, fn behaviour, shortcuts | Enable ctrl+N Space switching (S2); fn keys as F-keys | `KeyRepeat`, `InitialKeyRepeat`, `com.apple.keyboard.fnState`, `com.apple.symbolichotkeys` + `activateSettings -u` | expected | none | yabai docs (MIT) | image defaults; `machine_settings {set:{shortcut:{...}}}` | P2 | W6 |
| Y24 | Animation speed for determinism | Fewer frames of motion to settle through; faster verifier loops | `NSAutomaticWindowAnimationsEnabled false`, `NSWindowResizeTime 0.001`, `com.apple.dock expose-animation-duration 0.1`, `launchanim false`, plus Y3 reduce motion | expected | none | yabai, Hammerspoon docs (MIT) | image defaults (a "fast" profile), measured against the bench | P1 | W5 |
| Y25 | Wi-Fi and Bluetooth | Apps that scan networks or devices | No Wi-Fi or Bluetooth hardware in the VM (virtio networking shows as Ethernet); CoreWLAN and CoreBluetooth find nothing | **no** | n/a | n/a | none | P3 | none |
| Y26 | Location services and simulated location | "Shows the weather for the current city" | No Wi-Fi, so CoreLocation has nothing to locate with; location simulation needs Xcode's debugger, which the image does not have (docs/02) | **no** | n/a | n/a | none; app-level injection | P3 | none |
| Y27 | App Nap | Background apps under test slow down while the verifier looks elsewhere | Per app `NSAppSleepDisabled -bool true` | expected | none | none | `machine_defaults` | P3 | W6 |
| Y28 | Printers | Printing to a real queue | CUPS: `lpadmin -p Test -E -v file:///tmp/print.out -m everywhere` (root); unverified that a file backend is allowed | unverified: W8 | root | none | part of `machine_print` | P3 | W8 |

---

## 10. Web and cross-app scripting

UFO2's lesson (docs/21 3.7): where an app has an API, it beats the GUI. For the verifier these
are **setup and observation routes**, recorded as "scripted, not a user action"; a check about a
user action still goes through the pointer and keyboard.

| ID | Capability | Why (Greenroom case) | How on macOS | VM | TCC | Prior art | Tool | P | Wave |
|---|---|---|---|---|---|---|---|---|---|
| X1 | Open a URL in the default or a given browser | "The Help button opens the docs page" | `NSWorkspace.open(url)`, `open -a Safari <url>` | yes | none | all | `machine_open {url, app}` | P1 | W2 (covered) |
| X2 | Safari tabs and windows: list, open, set URL, close | Web flows around a desktop app (OAuth, docs links) | Safari AppleScript dictionary (`URL of tabs of window 1`, `make new tab`); the image has an AppleEvents row for Safari [img] | expected | AppleEvents | macos-automator-mcp (MIT) | `machine_web {action:"tabs" \| "open" \| "url" \| "close"}` | P2 | W8 |
| X3 | Page content and JavaScript | Coders who build a web app or a WKWebView-based app; reading a page exactly instead of through a screenshot | Safari `do JavaScript` (the image sets `AllowJavaScriptFromAppleEvents` [img]); or `sudo safaridriver --enable` then W3C WebDriver on localhost for full automation; AX of `AXWebArea` for what a user sees (W1). Page text is untrusted data (docs/21 6) | expected | AppleEvents | Playwright (Apache-2.0) for the model | `machine_web {action:"eval" \| "read" \| "wait_for", script \| selector}` | P2 | W8 |
| X4 | Web Inspector and WKWebView inspection | Console errors in an app's web view | Safari's Develop menu (`IncludeDevelopMenu`, in Safari's container); an app's web view must set `WKWebView.isInspectable = true` (macOS 13.3); console messages also reach the unified log under the WebKit subsystems | unverified: W8 | none | none | `machine_app_logs {subsystem:"com.apple.WebKit"}` | P3 | W8 |
| X5 | Other browsers | Chrome-only web apps | Not in the image; Chrome's `--remote-debugging-port` (CDP) if a task installs it | n/a | none | Playwright (Apache-2.0) | none | P3 | none |
| X6 | Universal links | "Clicking the link opens the app, not the browser" | Needs associated domains, the network and a real domain's AASA file: not reproducible in a disposable VM. Custom schemes work (A3) | **no** (mostly) | n/a | none | none | P3 | none |
| X7 | AppleScript and JXA, and reading an app's scripting dictionary | Test the app's own scripting support; set up Mail, Notes or Finder state | `NSAppleScript`/`OSAScript` in the agent (or `osascript -l JavaScript`); `sdef <app>` prints the dictionary. Each new target app prompts for Automation (D9) | yes (osascript works today) | AppleEvents per target | macos-automator-mcp (MIT) | `machine_script {language:"applescript" \| "jxa", source, timeout_ms}`; `machine_script {action:"dictionary", app}` | P2 | W8 |
| X8 | Shortcuts and App Intents | "The app's 'Split Bill' intent shows in Shortcuts and runs" | `shortcuts list`, `shortcuts run <name> -i <in> -o <out>` (CLI); an app's actions are listed in the Shortcuts app's action library (AX); creating a shortcut headless is not supported (import shows a prompt) | unverified: W8 | AX | none | `machine_shortcuts {action:"list" \| "run" \| "actions", app}` | P2 | W8 |
| X9 | Automator workflows and Services | Legacy automation the app provides | `automator -i <input> <workflow>`; Services in M12 | expected | AppleEvents | none | `machine_script {language:"automator"}` | P3 | W8 |
| X10 | System Events UI scripting | Legacy route | Superseded by direct AX (docs/21 7.2 bans `osascript` for UI). Kept only for property sets with no other route (Y1) | yes | AppleEvents | none | none | P3 | none |
| X11 | `open` options | Launch hidden, in background, fresh, wait for exit | `open -g` (background), `-j` (hidden), `-F` (fresh: no restored windows), `-W` (wait), `-n`, `--env`, `--args`, `-R` (reveal) | yes | none | none | flags on `machine_app launch` and `machine_open` | P2 | W2 (add) |
| X12 | Distributed notifications and notify(3) | Simulate system events an app listens to | `DistributedNotificationCenter.post`, `notifyutil -p`. Faking a system event lies about state; allowed only as a labelled simulation | expected | none | none | `machine_simulate {notification}` (labelled, setup) | P3 | W9 |
| X13 | Calendar, Contacts, Reminders, Photos fixtures | Apps built on EventKit, Contacts, PhotoKit | Seed through AppleScript or by opening `.ics`/`.vcf` files; **the lean profile disables `calaccessd`, the AddressBook agents, `photolibraryd` and more** (`lean.sh`), so such apps need the base image or those agents re-enabled | unverified: W9 | Contacts, Calendars, Photos for the agent | none | `machine_fixture {kind:"calendar" \| "contacts", file}` | P3 | W9 |

---

## 11. Observation and diagnostics

| ID | Capability | Why (Greenroom case) | How on macOS | VM | TCC | Prior art | Tool | P | Wave |
|---|---|---|---|---|---|---|---|---|---|
| O1 | Full-screen capture | Visual checks | `SCScreenshotManager.captureImage` (macOS 14), `captureImage(in:)` (15.2), `captureScreenshot` (26) [SCK] | yes (the helper captures today; SCK in W1) | ScreenCapture | Peekaboo (MIT) | `machine_screenshot` | P0 | W1 (covered) |
| O2 | Window capture, even covered | Evidence of one window | `SCContentFilter(desktopIndependentWindow:)` | yes | ScreenCapture | Peekaboo (MIT) | `machine_screenshot {window}` | P0 | W1 (covered) |
| O3 | Element crop | Evidence for one control | Crop to the ref's visible rect plus a margin | yes | ScreenCapture | Anthropic `zoom` | `machine_screenshot {ref}` | P0 | W1 (covered) |
| O4 | Video recording | Animations, transitions, "the spinner shows and then goes away", timing checks, and a clip for the Companion | `SCRecordingOutput` (macOS 15) records an `SCStream` to a movie file [SCK]; `screencapture -v` as a fallback; bounded length and frame rate; key frames extracted for the describer | expected | ScreenCapture | cua (MIT) | `machine_record {action:"start" \| "stop", window \| region, fps, max_seconds <= 60}` | P1 | W9 |
| O5 | Accessibility tree and notifications | Everything in W1 | AX + `AXObserver` | yes | AX | all | `machine_snapshot`, `machine_wait_for` | P0 | W1 (covered) |
| O6 | OCR | Canvas and web content without AX text | Vision `VNRecognizeTextRequest` | expected | none | Apple sample code | `machine_ocr` | P1 | W3 (covered) |
| O7 | Window server state | What covers what | W2 | yes | ScreenCapture | yabai (MIT) | W2 | P0 | W2 |
| O8 | Processes | Responding or not, CPU, memory | `proc_pidinfo`, `sysctl`, AX probe | yes | none | cua-driver (MIT) | `machine_processes` | P0 | W3 (covered) |
| O9 | Unified log | App errors behind a failed check | `log show`/`log stream --predicate` | yes | none | none | `machine_app_logs` | P0 | W3 (covered) |
| O10 | Crash reports | ADR 0028: a crash is evidence | `.ips` under `~/Library/Logs/DiagnosticReports` | yes | none | none | `machine_crashes` | P0 | W3 (covered) |
| O11 | Hang diagnosis: `sample` and `spindump` | #185 hang; #187 WindowServer stalls | `sample <pid> <s>` (W3); add `spindump <pid> <s> -file` (root) for system-wide stacks including WindowServer | expected | root for spindump | none | `machine_sample {mode:"sample" \| "spindump"}` | P1 | W3 (extend) |
| O12 | Memory, leaks, energy | "Memory stays flat after 50 opens" | `footprint <pid>`, `leaks <pid>` (stacks need `MallocStackLogging=1` at launch, A2), `vmmap --summary`; `powermetrics` (root) has little meaning in a VM | expected | none; root for powermetrics | none | `machine_processes {detail:"memory" \| "leaks"}` | P2 | W3 (extend) |
| O13 | Network activity per process | "The app makes no requests in offline mode"; "it calls the right host" | `nettop -P -L 1 -p <pid>` (bytes), `lsof -i -p <pid>` (connections), `log stream` on network subsystems | expected | none | none | `machine_network {action:"connections" \| "traffic", app}` | P2 | W6 |
| O14 | File system activity | F12, F13 | FSEvents, `fs_usage` | expected | root for fs_usage | none | F12, F13 | P1 | W8 |
| O15 | Notification history | M8 | M8 | unverified | FDA | none | M8 | P2 | W7 |
| O16 | Pasteboard changes | "Copy updates the clipboard" | Poll `NSPasteboard.changeCount` | expected | none (see 14) | none | `machine_wait_for {target:"clipboard"}` | P2 | W9 |
| O17 | Accessibility announcements | "The app announces 'Saved'" without running VoiceOver | Observe `AXAnnouncementRequested` [HIS] | expected | AX | none | `machine_wait_for {target:{announcement:"Saved"}}` | P2 | W9 |
| O18 | New windows and apps anywhere | Unexpected windows (#195) | `AXWindowCreated` + NSWorkspace launch notifications as agent events | yes | AX | none | `attention` | P0 | W1/W2 (covered) |
| O19 | Windows excluded from capture | `NSWindow.sharingType = .none` windows are blank in captures; must not be judged as "empty" | Compare the window's `kCGWindowSharingState` (CGWindowList) with the capture; report "not capturable" | expected | ScreenCapture | none | flag in `machine_screenshot` results | P2 | W3 (add) |
| O20 | Performance traces | Instruments-style traces | `xctrace` needs Xcode, not in the image (docs/02); signposts reach the unified log | **no** without Xcode | n/a | none | `machine_app_logs {signposts:true}` | P3 | W3 (add) |

---

## 12. Testing aids

| ID | Capability | Why (Greenroom case) | How on macOS | VM | TCC | Prior art | Tool | P | Wave |
|---|---|---|---|---|---|---|---|---|---|
| T1 | Reset an app's state | "First launch" checks after a run already launched the app; persistence trials | Quit; `defaults delete <id>`; remove `~/Library/Containers/<id>` (a sandboxed app's container; since macOS 14 touching another app's container can raise the "access data from other apps" prompt, App Management / `kTCCServiceSystemPolicyAppData`, unverified for our agent), Application Support, Caches, Saved Application State, HTTP storage; `security delete-generic-password -s <service>`; `tccutil reset All <id>` | unverified: W9 | root may be needed | none | `machine_app {action:"reset", scope:["defaults","container","saved_state","caches","keychain","permissions"]}` returns what was removed (setup) | P1 | W9 |
| T2 | Fixture data | Documents to open, preferences to seed | `machine_sync` (exists) + Y18 | yes | none | none | existing | P1 | covered |
| T3 | Time travel | Y9 | Y9 | unverified | root | none | `machine_clock` | P2 | W6 |
| T4 | Camera input | Video apps, QR scanning | No camera device in Virtualization.framework [VZ]. USB passthrough (`VZUSBPassthroughDeviceConfiguration`, host macOS 27 [VZ]) would expose the host's real camera: unsafe, and tart 2.32.1 does not offer it. A CoreMediaIO camera extension that plays a video file needs a system extension approval (with SIP off, `systemextensionsctl developer on`; unverified) | **no** today; spike W9 | Camera for the app | none | `machine_camera {action:"install_virtual", video}` (research) | P3 | W9 |
| T5 | Microphone input | Voice apps | tart attaches the host microphone to the guest when audio is on (section 13): unsafe. A virtual audio HAL plug-in in `/Library/Audio/Plug-Ins/HAL` (BlackHole is GPL-3.0: use only as a separately installed tool, never linked) could play a file as input | **no** today; spike W9 | Microphone for the app | none | `machine_audio {action:"play_as_input", file}` (research) | P3 | W9 |
| T6 | Location | Y26 | none | **no** | n/a | none | none | P3 | none |
| T7 | Accessibility audit | "Every button has a label"; a11y regressions | Walk the AX tree: controls without `AXTitle`/`AXDescription`, images without descriptions, `AXRoleDescription` "unknown", targets under 24 by 24 points, keyboard reachability (I23), contrast (WCAG ratio from pixels at the element's rect), clipped text (docs/21 flags). `XCUIApplication.performAccessibilityAudit` needs Xcode's UI test runner, which the image lacks | expected | AX, ScreenCapture | Accessibility Inspector (Apple, ideas) | `machine_audit {window \| ref, checks:["labels","targets","contrast","keyboard","clipping"]}` returns findings with refs and crops | P2 | W9 |
| T8 | Localization, RTL, pseudo-localization | Truncated translations; mirrored layouts | A2 launch presets (`-AppleLanguages`, `-NSForceRightToLeftWritingDirection YES`, `-NSDoubleLocalizedStrings YES`, `-NSShowNonLocalizedStrings YES`), then a snapshot for `clipped` flags and a log read for missing keys | expected | none | none | `machine_app {action:"launch", locale:"ar", rtl:true, pseudo:"double"}` | P2 | W9 |
| T9 | Dark mode comparison | Unreadable text in dark mode | Y1 plus per-element contrast from T7 on both appearances | expected | ScreenCapture | none | `machine_audit {checks:["contrast"], appearances:["light","dark"]}` | P2 | W9 |
| T10 | Window size classes | Layout at compact and wide sizes | W6 resize to a list of widths, snapshot each for `clipped`/`covered` flags | expected | AX | none | composed; optional `machine_audit {sizes:[...]}` | P2 | W9 |
| T11 | Crash detection and restart | ADR 0028 | O10 + A8 | yes | none | none | existing | P0 | W3 (covered) |
| T12 | Fault injection: stop, low memory, full disk | "The app survives memory pressure"; "shows an error when the disk is full" | `kill -STOP/-CONT`; `memory_pressure -S -l critical` (CLI present); a disk image of fixed size as the save target (F15) | expected | none | none | `machine_simulate {fault:"memory_pressure" \| "disk_full" \| "stop"}` (labelled) | P3 | W9 |
| T13 | Determinism profile | Faster settling, fewer surprises | S14, S15, Y22 to Y24, D12, notifications and widgets policy | expected | none | none | image recipe (`imageRecipeVersion` bump) | P1 | W5 |
| T14 | Whole-VM snapshot and restore | Reset the desktop between checks faster than a reboot | Host side: `VZVirtualMachine.saveMachineStateTo(url:)` and `restoreMachineStateFrom` (macOS 14 [VZ]); tart's `--suspendable` drops the audio input and cannot be used with `--no-trackpad` [tart]. The guest agent's channel and TCC state must survive the restore (unverified) | unverified: W9 (spike) | n/a | tart (FSL-1.1-ALv2, ideas only) | `machine_checkpoint {action:"save" \| "restore"}` (research) | P3 | W9 |

---

## 13. VM feasibility summary, and what is impossible or unsafe

### 13.1 What the VM is, from primary sources

| Aspect | Fact | Source |
|---|---|---|
| Displays | One. "Maximum of one display is supported." | `VZMacGraphicsDeviceConfiguration.h` [VZ] |
| Display size | Set per VM (`tart set --display WxH[pt\|px]`); tart can refit it to its window; Greenroom runs 1024x768 (docs/02) | tart `set --help`, `Platform/Darwin.swift` |
| Pointing devices | A USB screen-coordinate pointer **and** a Mac trackpad (`VZMacTrackpadConfiguration`, guests 13+) unless `--no-trackpad` | tart `Platform/Darwin.swift` `pointingDevices`, [VZ] |
| Gestures | The trackpad device carries gestures from `VZVirtualMachineView` on the host; Greenroom runs `--no-graphics`, and no public API synthesizes gestures inside the guest | [VZ], `tart.go` (`run name --no-graphics`), [AK] |
| Keyboards | USB keyboard plus `VZMacKeyboardConfiguration` (macOS 14; globe key) | tart `keyboards()`, [VZ] |
| Audio | With audio on (tart's default; Greenroom does not pass `--no-audio`), guest output goes to the **host's speakers** and the **host's microphone** is the guest's input (`VZHostAudioInputStreamSource`); with `--no-audio` tart keeps only a null speaker | tart `VM.swift` lines 351 to 365 |
| Clipboard | Shared between host and guest through the Spice agent port unless `--no-clipboard`; Greenroom does not pass it | tart `Run.swift` (`clipboard: !noClipboard`), `VM.swift` |
| Camera | None: no camera device class in Virtualization.framework. USB passthrough exists only from host macOS 27 and would hand over a real device | [VZ] `VZUSBPassthroughDeviceConfiguration.h` |
| Touch ID | None: no biometric device class | [VZ] |
| Sleep | Unverified for the guest; the host can pause, resume, save and restore state (macOS 14) | [VZ] `VZVirtualMachine.h` |
| Display sleep | Black captures with exit 0 (measured); disabled in the image | `desktopprefs.go` |
| SIP | Off in the image, TCC.db written at build time | docs/02, docs/09 |
| Wi-Fi, Bluetooth, battery, location | None | virtio devices only in tart `VM.swift` |
| Network | NAT by default; softnet can allow or block CIDRs (restart needed); `tart exec` does not depend on guest networking, rsync does | `tart run --help`, docs/02 |
| Xcode | Not in the image (Command Line Tools only) | docs/02 |
| Lean profile | Notification banners, widgets, Spotlight indexing, Siri, calendar, contacts, photos and many media agents off | `lean.go`, `lean.sh` |

### 13.2 Impossible or unsafe in the VM, and what to do instead

| Capability | Why not | Instead |
|---|---|---|
| Multiple displays (Y5) | One display is the framework's limit | Unit tests with injected screen data; say "not testable in a VM" in the verdict |
| Touch ID (D6) | No biometric device | Test the password fallback (D5); report Touch ID as not testable |
| Real trackpad gestures, force click (I16, I17) | No public synthesis; the host view is not used | Keyboard and menu equivalents; a W9 spike on private gesture events, results marked `unverifiable` when nothing changes |
| Camera (T4) | No device; passthrough would expose the host's camera | Virtual camera extension spike (W9); otherwise "not testable" |
| Microphone and dictation (I22, T5) | tart wires the host's real microphone into the guest | Pass `--no-audio` (section 14); virtual input device spike (W9) |
| Location (Y26), Wi-Fi, Bluetooth, battery (Y14, Y25) | No hardware; no Xcode location simulation | App-level injection by the coder; "not testable" |
| Login window, lock screen, fast user switching (D15, D16, Y16) | The agent lives in one user's GUI session; leaving it blinds the toolkit | `machine_reboot` with autologin; labelled distributed-notification simulations |
| Guest sleep and wake (Y15) | Unverified; risks stalling the channel and `tart exec` | Research spike in W6; until then, not offered |
| Night Shift, True Tone, brightness (Y6) | Meaningless on a virtual display | None |
| iCloud, universal links, Screen Time, Apple ID features (F14, X6, Y20) | No account, no real domain | Custom URL schemes; "not testable" |
| Spaces changes through Dock injection (S3, S5 via yabai's scripting addition) | Changes the system under test; breaks per release | Keyboard shortcuts and Mission Control's AX (section 2) |
| Setting other apps' window levels (W12) | Needs Dock injection | Read-only level checks |
| Instruments traces, `performAccessibilityAudit`, Touch Bar simulator (O20, T7, M19) | Need Xcode | Our own audit (T7), `sample`/`spindump`, signposts in the log |

---

## 14. Findings that change waves 1 to 4

1. **Host microphone and speakers are wired into every Greenroom VM.** `tart.go` runs `tart run
   <name> --no-graphics` only; tart's default attaches `VZHostAudioInputStreamSource` and
   `VZHostAudioOutputStreamSink` (`VM.swift`). An app under test that records audio, or dictation,
   would hear the host's room, and VoiceOver or app sounds play on the host. Proposal: pass
   `--no-audio` (tart then keeps a null speaker, so apps still have an output device), and
   verify in wave 1 that ScreenCaptureKit audio capture (Y12) still reports levels with the null
   speaker. Whether the host has granted tart microphone access is unverified.
2. **Host and guest clipboards may be synced.** tart enables Spice clipboard sharing unless
   `--no-clipboard`, and the image runs tart-guest-agent, which implements it on macOS (tart's
   help text). A secret on the host's clipboard could reach the guest, and what the verifier
   copies could reach the host. Proposal: pass `--no-clipboard`; wave 2's `machine_clipboard`
   tests confirm the guest pasteboard is isolated.
3. **Pasteboard privacy alerts.** AppKit's `NSPasteboardAccessBehavior` (API from macOS 15.4)
   documents that the general pasteboard's default is to **ask** on programmatic access. Wave 2
   must check whether the agent's reads (or the app's) raise an alert on macOS 26 and, if so,
   handle it under the prompt policy.
4. **Menu-bar extras are not in SystemUIServer.** System status items belong to ControlCenter
   (section 5); docs/21 5.4 should say "look up extras by owning process".
5. **Double and triple clicks** need `kCGMouseEventClickState` on each event pair (I2); wave 1's
   `machine_press {count}` should test a triple-click paragraph selection.
6. **Clock changes reset capture approvals** (ADR 0013); any tool that changes the clock (Y9)
   must rewrite them before the next capture.
7. **The lean profile hides whole feature areas**: notification banners, widgets, Spotlight
   indexing and the calendar, contacts and photos agents are off. The attention policy is right to
   treat them as noise, but tasks about those features need a profile switch; wave 7 and wave 9
   should add per-run toggles, and the verifier should say "not testable in the lean image"
   rather than fail.
8. **Launchpad is gone** on macOS 26 and later; no tool should rely on it.

---

## 15. Proposed wave plan for everything not in waves 1 to 4

Waves 5 to 9 each depend on wave 2 (menus, dialogs, windows, apps) and on nothing else in this
list, so they can run in any order or in parallel. Suggested order by P1 items and field data:
**7** (six P1; prompts cause the most unplanned failures, #195), **5** (six), **6** (five),
**8** (five), **9** (four). Hover (I1) and text selection (I18) from wave 9 are small and appear
in most UI tasks, so they can be pulled into whichever wave runs first. Each wave lands behind the
`-desktop-toolkit` flag like waves 1 to 3, adds `caps` to `HELLO` (docs/21 4.3), bumps the helper
version, and ends with a bench comparison on the navigation tier. Every new tool follows the
docs/21 contract: effect with read-back, a recorded step, a deadline, setup tools labelled.

### Wave 5: Spaces, Mission Control, windows and the Dock

- Tools: `machine_space` (list, switch, create, remove, move_window, assign, overview,
  app_expose, show_desktop, stage, app_switcher), `machine_window` additions (tile, tabs, split,
  zoom, `list all:true` with level and z-order, full-screen transition wait), `machine_dock`,
  `machine_spotlight`, `space` in `machine_wait_for`.
- Image defaults (T13): `mru-spaces` off, ctrl+N Space shortcuts on, hot corners off, window and
  Mission Control animations shortened; measured before and after on the bench.
- SkyLight reads behind `dlsym` with an `unsupported` result when a symbol is missing.
- Catalog items: S1 to S11, S13 to S16, W7, W9, W12, W17, M4, M10, M17, Y24, T13.

### Wave 6: system settings and state control

- Tools: `machine_settings` (appearance, accent, reduce motion, transparency, contrast, language,
  region, time zone, 24-hour clock, stage manager, hot corners, dock, pointer, keyboard,
  shortcuts, focus, crash dialog, `open_pane`), `machine_defaults`, `machine_clock`,
  `machine_display`, `machine_audio`, `machine_network`, `machine_control_center`.
- A settings ledger per run: every change records old and new values and its read-back, and the
  daemon restores them at run end; `run_report` lists them.
- Catalog items: S12, S14 (tool), D14, Y1 to Y4, Y7 to Y13, Y15 (spike), Y17 to Y19, Y21 to
  Y23, Y27, M6, O13.

### Wave 7: prompts, permissions and system dialogs

- Attention classifier by owning process (UserNotificationCenter, universalAccessAuthWarn,
  SecurityAgent, CoreServicesUIAgent, NotificationCenter, replayd) with a per-prompt table
  measured on macOS 26.
- Tools: `machine_permissions` (TCC list, grant, deny, reset), `machine_auth` (password from the
  daemon, never the model), `machine_notifications` (list, press, action, dismiss, clear, open
  center, permission, history research), `machine_keychain`, `machine_login_items`,
  `machine_profiles`, quarantine in `machine_files`.
- Policy: which prompts the verifier may answer as setup, which only when the task says so, and
  how each is recorded.
- Catalog items: A13, A15 (policy), D5 to D11, D17, D18, M7, M8, O15.

### Wave 8: files, installs, handlers, scripting and the web

- Tools: `machine_finder`, `machine_files` (stat, xattr, metadata, tags, zip, unzip, mount, eject,
  trace), `machine_install` (DMG drag, PKG CLI and UI, zip), `machine_quicklook`, `machine_print`,
  `file` targets in `machine_wait_for`, handler and registration actions and `info` in
  `machine_app`, `machine_script` (AppleScript, JXA, Automator, dictionaries),
  `machine_shortcuts`, `machine_web` (Safari tabs, eval, read, wait; WebDriver behind a flag),
  `machine_services`.
- Catalog items: A10, A11, A14, A16, A20, M12, M13, D3, F1 to F13, F15, X2 to X4, X7 to X9, Y28.

### Wave 9: advanced input, text, and test instruments

- Input: `machine_hover`, `hold_ms` on press and key, drag modifiers and dwell, trackpad-style
  scrolling, `via` on `machine_type`, fn and system keys, `machine_input_source` and IME typing,
  dead keys.
- Text: `machine_text` (select, read, bounds, attributes); rich `machine_clipboard` types and a
  clipboard wait.
- Instruments: `machine_record` (video), `machine_audit` (labels, targets, contrast, keyboard,
  clipping, light and dark, sizes), localization and RTL launch presets, `machine_app reset`,
  `machine_voiceover`, announcement waits, `machine_simulate` (labelled faults and
  notifications), fixtures for calendar and contacts.
- Spikes with a written result each: synthesized gestures (I16), virtual camera (T4), virtual
  microphone (T5), VM checkpoints (T14).
- Catalog items: W14, A2 (presets), M9, M18, D4, D16, I1, I4, I5, I8, I10 to I14, I16, I18, I19,
  I21, I23, O4, O16, O17, T1, T4, T5, T7 to T10, T12, T14, X12, X13, Y11.

### Additions to existing waves

- **Wave 1 (#212)**: `via` on `machine_type` (I9); triple-click test (I2); secure-field typing
  test (I15); `AXDocument`/`AXEdited` in the window line (W16); overflow toolbar items (M15);
  `--no-audio` and `--no-clipboard` decision (section 14).
- **Wave 2 (#213)**: owner-process lookup for extras (M5); `zoom`, `unhide`, `list all:true`
  (W2, W5, A5); menu `find` (M14); hidden menu bar (M16); launch `args`, `env`, `wait:
  "status_item"`, `ready_ms`, `open` flags (A2, A12, A19, X11); cross-app drag test (I6);
  sandboxed save panels (D2); clipboard isolation and pasteboard alert checks (section 14).
- **Wave 3 (#214)**: `spindump` mode (O11); memory and leaks detail (O12); "not capturable"
  windows (O19); signposts (O20).

---

## 16. Prior art and licences

Licences checked on 2026-09-27 with the GitHub licence API and, where the API said
`NOASSERTION` or `Other`, the licence file itself.

| Project | Licence | What it shows for this catalog |
|---|---|---|
| `koekeishiya/yabai` | MIT | SkyLight reads (`src/misc/extern.h`: `SLSCopyManagedDisplaySpaces`, `SLSCopySpacesForWindows`, `SLSManagedDisplayIsAnimating`, ...); Space create, destroy, focus and window moves through a Dock scripting addition that needs SIP partly off and `-arm64e_preview_abi` (`src/sa.m`) |
| `Hammerspoon/hammerspoon` | MIT (the `eventtap/TouchEvents` files are **GPL-2.0**, from Calf Trail) | `hs.spaces`: Mission Control through the Dock's AX (`mc`, `mc.display`, `mc.spaces`, `AXRemoveDesktop`), `CoreDockSendNotification` for Mission Control, App Exposé, Show Desktop and Launchpad; window moves between Spaces after 14.5 via `SLSSpaceSetCompatID`; gesture synthesis (GPL part) |
| `openclaw/Peekaboo` (was `steipete/Peekaboo`) | MIT | `space` (list, switch, move-window with explicit consent to follow), `dock`, `menubar`, `dialog` (file dialogs, classification), `clipboard`, permissions; CGS declarations in `SpaceCGSPrivateAPI.swift` |
| `steipete/AXorcist` | MIT | AX queries and observers |
| `nikitabobko/AeroSpace` | MIT | Emulated workspaces without Spaces or SIP changes; one private API, `_AXUIElementGetWindow` |
| `ianyh/Amethyst` and `ianyh/Silica` | MIT | Moving a window to a Space with public events: title-bar mouse-down plus the Switch-to-Desktop shortcut (`SIWindow -moveToSpace:`) |
| `rxhanson/Rectangle` | MIT (based on Spectacle) | Window move, resize, tiling; `AXFullScreen` usage |
| `steipete/macos-automator-mcp` | MIT | A knowledge base of AppleScript and JXA recipes: system settings, display, audio, notifications, power, screen lock, window management |
| `trycua/cua` | MIT (except `libs/python/som` and `cua-perception`: AGPL-3.0) | Result contract, recording (docs/21 3.3) |
| `mediar-ai/terminator` | MIT (Windows only now) | Selector and batch ideas (docs/21 3.5) |
| `cirruslabs/tart` | FSL-1.1-ALv2 (Functional Source License, converts to Apache-2.0 two years after each release) | VM device facts in this document; not a code source for us |
| `cirruslabs/tart-guest-agent` | FSL-1.1-ALv2 | Clipboard sharing, exec |
| `lwouis/alt-tab-macos` | GPL-3.0 | App switcher ideas only |
| `jordanbaird/Ice` | GPL-3.0 | Menu bar item hiding ideas only |
| `MrKai77/Loop` | GPL-3.0 | Window tiling ideas only |
| `microsoft/playwright` | Apache-2.0 | The contract model (docs/21 3.2) |
| Apple SDK headers and sample code | Apple SDK licence | Every public symbol cited |

Rule, unchanged from docs/21: code may be adapted from MIT and Apache-2.0 projects with
attribution (and a NOTICE for Apache-2.0); GPL, AGPL, BSL and FSL projects are for reading only.

---

## 17. Sources

- SDK headers in `xcrun --show-sdk-path` (Xcode on the host, 2026-09-27): Virtualization
  (`VZMacGraphicsDeviceConfiguration.h`, `VZMacTrackpadConfiguration.h`,
  `VZMacKeyboardConfiguration.h`, `VZUSBPassthroughDeviceConfiguration.h`, `VZVirtualMachine.h`,
  `VZVmnetNetworkDeviceAttachment.h`), HIServices (`AXAttributeConstants.h`, `AXRoleConstants.h`,
  `AXNotificationConstants.h`, `AXActionConstants.h`, `AXUIElement.h`), CoreGraphics
  (`CGEvent.h`, `CGEventTypes.h`, `CGRemoteOperation.h`, `CGWindow.h`), AppKit (`NSEvent.h`,
  `NSWorkspace.h`, `NSPasteboard.h`), ScreenCaptureKit (`SCScreenshotManager.h`,
  `SCRecordingOutput.h`), ServiceManagement (`SMAppService.h`), HIToolbox
  (`TextInputSources.h`).
- tart source (`Sources/tart/VM.swift`, `Platform/Darwin.swift`, `Commands/Run.swift`) and
  `tart run --help`, `tart set --help` from tart 2.32.1.
- Greenroom: `apps/daemon/internal/tart/tart.go`, `apps/daemon/internal/machine/{base,lean,
  desktopprefs,timezone}.go`, `guest/base.sh`, `guest/lean.sh`; docs/02, docs/09, docs/21; ADRs
  0013, 0018, 0021, 0028, 0037.
- Prior-art source read through the GitHub API on 2026-09-27: yabai `src/misc/extern.h`,
  `src/sa.m`, `src/window_manager.c`, `src/space_manager.c`; Hammerspoon
  `extensions/spaces/{spaces.lua,libspaces.m,private.h}`, `extensions/eventtap/TouchEvents.h`
  and its `LICENSE.md`; Peekaboo `SpaceCommand+MoveWindow.swift`, `SpaceCGSPrivateAPI.swift`;
  Amethyst `Amethyst/Model/Window.swift`; Silica `SIWindow.m`; AeroSpace README;
  macos-automator-mcp `knowledge_base/04_system`.
- Host checks (read-only): presence of the CLIs named in the tables, `/System/Applications/Mission
  Control.app`, `/System/Applications/Apps.app` (and no `Launchpad.app`), the
  `com.apple.WindowManager` keys, and the ControlCenter, NotificationCenter, SystemUIServer,
  WindowManager, UserNotificationCenter, universalAccessAuthWarn and CoreServicesUIAgent
  processes.
