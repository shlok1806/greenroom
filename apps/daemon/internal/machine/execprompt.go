package machine

// A command stopped by a system prompt (ADR 0047, issue #283). ADR 0038 point 3 made a
// machine_exec that is still running after its wait look at the screen, so an MCP caller learns
// a prompt is blocking it. The verifier's machine_exec blocks for the whole command instead, so
// ExecWatched looks while it waits and, on a prompt it has not already reported, stops the
// command and says what the prompt says. Nothing here answers or closes the prompt (ADR 0018
// point 4): measured live, it stays until someone answers it or it times out, whatever happens
// to the command that raised it.

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// defaultPromptLook is how often ExecWatched looks at the screen while a command runs: the
// prompt appears within a second of the request, so a stalled command comes back after about
// this long instead of its timeout. Each look is one guest round trip.
const defaultPromptLook = 15 * time.Second

// execStopGrace is how long a command stopped with TERM has to exit before it gets KILL, and how
// long after KILL the host waits before it gives up on the guest and ends its own tart exec.
const execStopGrace = 5 * time.Second

// promptTextTimeout bounds reading what the prompts on screen say.
const promptTextTimeout = 10 * time.Second

// promptTextScript prints, for each pid in its arguments, the pid, a tab and the static texts of
// that process's windows on one line, read through System Events (Apple Events to System Events
// are granted in the image, ADR 0038 point 1). machine_ui cannot read it: it reads only regular
// apps, and the process that draws a TCC prompt is not one (issue #223). The texts are joined in
// AppleScript, never by splitting its list on ", ", which a prompt's own text contains. A failed
// read prints the pid alone.
const promptTextScript = `: greenroom-prompt-text
for p in "$@"; do
  t=$(/usr/bin/osascript -e 'on run argv' -e 'set out to ""' -e 'with timeout of 5 seconds' \
    -e 'tell application "System Events" to set vals to value of every static text of every window of (first process whose unix id is ((item 1 of argv) as integer))' \
    -e 'end timeout' \
    -e 'repeat with ws in vals' -e 'repeat with v in (contents of ws)' -e 'set s to contents of v' \
    -e 'if s is not missing value then set out to out & s & linefeed' \
    -e 'end repeat' -e 'end repeat' -e 'return out' -e 'end run' \
    "$p" 2>/dev/null | tr '\n\t' '  ')
  printf '%s\t%s\n' "$p" "$t"
done
`

// readPromptTexts fills each prompt's Text from the guest. A prompt whose text cannot be read
// keeps an empty one; the window alone is still reported.
func (m *Manager) readPromptTexts(ctx context.Context, vm string, prompts []DesktopPrompt) {
	ctx, cancel := context.WithTimeout(ctx, promptTextTimeout)
	defer cancel()
	args := []string{"/bin/sh", "-c", promptTextScript, "greenroom-prompt-text"}
	seen := map[int]bool{}
	for _, p := range prompts {
		if p.PID > 0 && !seen[p.PID] {
			seen[p.PID] = true
			args = append(args, strconv.Itoa(p.PID))
		}
	}
	if len(seen) == 0 {
		return
	}
	res, err := m.tart.Exec(ctx, vm, args...)
	if err != nil {
		return
	}
	texts := map[int]string{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		pid, text, ok := strings.Cut(line, "\t")
		if n, err := strconv.Atoi(pid); ok && err == nil {
			texts[n] = cleanPromptText(text)
		}
	}
	for i := range prompts {
		prompts[i].Text = texts[prompts[i].PID]
	}
}

// cleanPromptText is the texts of a prompt's windows on one line, its runs of spaces collapsed.
func cleanPromptText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// lookAtDesktop is the look both exec paths take at a command that has not returned (ADR 0038
// point 3, ADR 0047): every on-screen window, whoever owns it, against the clean desktop, plus
// what each prompt says. A clean desktop is nil, and so is a look that fails, with ok false, so
// a command that is only slow is never reported. A finding is written to the machine's own Desktop (which machine_wait
// and the Companion read), logged and sent as a "desktop" event. It runs on its own budget, not
// the caller's ctx, which may be nearly spent.
func (m *Manager) lookAtDesktop(mc *Machine, execID string) (r *DesktopReport, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), execDesktopLookTimeout)
	defer cancel()
	d, err := readDesktop(ctx, m.tart, mc.Name)
	if err != nil {
		return nil, false
	}
	rep := d.Report()
	if rep.Clean {
		return nil, true
	}
	r = &rep
	m.readPromptTexts(context.Background(), mc.Name, r.Prompts)
	m.mu.Lock()
	same := mc.Desktop != nil && reflect.DeepEqual(*mc.Desktop, *r)
	mc.Desktop = r
	m.mu.Unlock()
	if same {
		return r, true // already logged and sent: a prompt that stays is looked at every promptLook
	}
	m.Log.Warn("a command is still running with something besides the desktop on screen; it may be blocked on a "+
		"system prompt that greenroom never auto-clicks or closes (ADR 0018)",
		"runId", mc.RunID, "execId", execID, "found", strings.Join(r.Findings(), "; "))
	m.emit(LifecycleEvent{Kind: "desktop", RunID: mc.RunID, Machine: m.snapshot(mc)})
	return r, true
}

// promptKey names a prompt across looks: the process drawing it and what it says. A second
// prompt with the same text from the same process (the same request made again) has the same key.
func promptKey(p DesktopPrompt) string {
	return fmt.Sprintf("%s|%d|%s", p.Owner, p.PID, p.Text)
}

// newPrompts records which prompts r shows and returns those not already reported by an earlier
// stop. A prompt a look no longer finds is forgotten, so the same request raised again after it
// went is new. A nil r (a clean desktop) forgets them all.
func (m *Manager) newPrompts(mc *Machine, r *DesktopReport) []DesktopPrompt {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := map[string]bool{}
	var fresh []DesktopPrompt
	if r != nil {
		for _, p := range r.Prompts {
			k := promptKey(p)
			now[k] = true
			if !mc.promptsSeen[k] {
				fresh = append(fresh, p)
			}
		}
	}
	for k := range mc.promptsSeen {
		if !now[k] {
			delete(mc.promptsSeen, k)
		}
	}
	return fresh
}

// markPromptsSeen records that a command was stopped for r's prompts.
func (m *Manager) markPromptsSeen(mc *Machine, r *DesktopReport) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mc.promptsSeen == nil {
		mc.promptsSeen = map[string]bool{}
	}
	for _, p := range r.Prompts {
		mc.promptsSeen[promptKey(p)] = true
	}
}

// ExecWatched is ExecAs for a caller that cannot come back for a command later, which is the
// verifier (ADR 0047). While the command runs it looks at the screen every promptLook; when a
// prompt is on screen that no earlier stop reported, it stops the command (TERM to its process
// group in the guest, KILL execStopGrace later) and returns its result with StoppedForPrompt and
// Desktop, which names the prompt and what it says. A prompt an earlier stop already reported
// does not stop another command: it stays on screen until it is answered or times out, and a
// command run meanwhile (a wait, a build) is not the one it blocks. machine_exec's own MCP path
// never stops a command: its caller collects it with machine_exec_wait and decides.
func (m *Manager) ExecWatched(ctx context.Context, runID, by, command, cwd string, timeout time.Duration) (ExecResult, error) {
	j, err := m.startExec(ctx, runID, by, command, cwd, timeout)
	if err != nil {
		return ExecResult{}, err
	}
	mc, err := m.get(runID)
	if err != nil {
		<-j.done
		return j.res, j.err
	}
	every := m.promptLook
	if every <= 0 {
		every = defaultPromptLook
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-j.done:
			return j.res, j.err
		case <-ctx.Done():
			j.cancel()
			<-j.done
			return j.res, j.err
		case <-tick.C:
			if j.finished() {
				continue
			}
			r, ok := m.lookAtDesktop(mc, j.id)
			if !ok {
				continue // nothing is known; a failed look neither stops the command nor forgets a prompt
			}
			if fresh := m.newPrompts(mc, r); len(fresh) > 0 {
				m.markPromptsSeen(mc, r)
				j.stoppedFor.Store(r)
				m.stopExec(mc, j)
				return j.res, j.err
			}
		}
	}
}

// stopExecScript ends the command a greenroom-exec wrapper runs for execId $1 with signal $2: the
// wrapper is found by its argv (`/bin/sh -s greenroom-exec <secs> <execId>`, guest.go), and its
// login zsh, which `set -m` made a process group leader, is signalled as a group, so the
// command's children go with it. The wrapper then prints the output so far and exits as usual.
const stopExecScript = `: greenroom-exec-stop
for p in $(pgrep -f "^/bin/sh -s greenroom-exec [0-9]+ $1\$"); do
  for z in $(pgrep -P "$p" -x zsh); do kill -"$2" -"$z" 2>/dev/null; done
done
exit 0
`

// stopExec ends j's command in the guest and waits for its result: TERM, then KILL after
// execStopGrace, then the host's own cancel (which ends tart exec) if the guest did not answer.
func (m *Manager) stopExec(mc *Machine, j *execJob) {
	for _, sig := range []string{"TERM", "KILL"} {
		ctx, cancel := context.WithTimeout(context.Background(), execStopGrace)
		_, err := m.tart.Exec(ctx, mc.Name, "/bin/sh", "-c", stopExecScript, "greenroom-exec-stop", j.id, sig)
		cancel()
		if err != nil {
			m.Log.Warn("could not stop a command blocked on a prompt", "runId", mc.RunID, "execId", j.id, "signal", sig, "err", err)
		}
		select {
		case <-j.done:
			return
		case <-time.After(execStopGrace):
		}
	}
	j.cancel()
	<-j.done
}
