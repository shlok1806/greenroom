package summary

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// NameWords is the longest a run's name may be (docs/20 principle 8).
const NameWords = 5

// Name is the run's name: the coding agent's, clipped to NameWords, else one made from its
// task. Deterministic: the same task always gives the same name.
func Name(given, task string) string {
	if n := clipWords(strings.Join(strings.Fields(given), " "), NameWords); n != "" {
		return n
	}
	return NameFromTask(task)
}

var (
	// camelCase is an app-like name with two or more humps: TipSplit, WordCount, HelloGreenroom.
	camelCase = regexp.MustCompile(`\b[A-Z][a-z0-9]+(?:[A-Z][a-z0-9]*)+\b`)
	// quoted is a short quoted phrase of letters, in straight or curly double quotes.
	quoted      = regexp.MustCompile(`["\x{201C}]([A-Za-z][A-Za-z ]{0,30}[A-Za-z])["\x{201D}]`)
	issueRef    = regexp.MustCompile(`#(\d+)\b`)
	parenthesis = regexp.MustCompile(`\s*\([^()]*\)`)
	sentenceEnd = regexp.MustCompile(`[.!?:;](\s|$)`)
)

// leadWords start a task sentence without naming what it is about.
var leadWords = []string{
	"a", "an", "the", "this", "that", "these", "please", "verify", "check", "test", "look", "make",
	"new", "now", "can", "could", "would", "you", "i", "we", "my", "our", "in", "on", "it", "is",
	"there", "here", "what", "when", "open", "run", "use", "see", "try", "find", "tell", "read",
	"one", "two", "three", "every", "each", "all", "first", "then", "after", "before", "if",
}

// NameFromTask makes a name of NameWords or fewer from a task: its subject (the first
// app-like name, else the first capitalised words that are not a sentence's lead), then a
// short quoted phrase or an issue number from the task, else the subject alone; with no
// subject, the task's first words.
func NameFromTask(task string) string {
	text := strings.Join(strings.Fields(task), " ")
	if text == "" {
		return "Untitled run"
	}
	bare := parenthesis.ReplaceAllString(text, "")
	subject := camelCase.FindString(bare)
	if subject == "" {
		subject = capitalisedRun(firstSentence(bare))
	}
	if subject == "" {
		// The sentence's first words, as a name: no ellipsis, and no dangling "in" or "the".
		words := strings.Fields(strings.TrimRight(firstSentence(bare), ".,;:!? "))
		words = words[:min(len(words), NameWords)]
		for len(words) > 1 && slices.Contains(leadWords, strings.ToLower(words[len(words)-1])) {
			words = words[:len(words)-1]
		}
		return strings.TrimRight(strings.Join(words, " "), ",;:")
	}
	if extra := nameDetail(bare, subject); extra != "" {
		return clipWords(subject+": "+extra, NameWords)
	}
	return clipWords(subject, NameWords)
}

func firstSentence(s string) string {
	if loc := sentenceEnd.FindStringIndex(s); loc != nil {
		return s[:loc[0]]
	}
	return s
}

// capitalisedRun is the first run of capitalised words (up to three) that is not a sentence's
// lead: "Greenroom Companion", "Groceries".
func capitalisedRun(sentence string) string {
	var run []string
	for _, w := range strings.Fields(sentence) {
		word := strings.TrimFunc(w, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		word = strings.TrimSuffix(word, "'s")
		if word == "" || !unicode.IsUpper([]rune(word)[0]) || slices.Contains(leadWords, strings.ToLower(word)) {
			if len(run) > 0 {
				break
			}
			continue
		}
		run = append(run, word)
		if len(run) == 3 || strings.ContainsAny(w, ",.;:!?") {
			break
		}
	}
	return strings.Join(run, " ")
}

// nameDetail is what tells this run apart within its subject: the first short quoted phrase
// that is not the subject's own word, else "issue N".
func nameDetail(text, subject string) string {
	own := strings.Fields(strings.ToLower(subject))
	for _, m := range quoted.FindAllStringSubmatch(text, -1) {
		phrase := strings.TrimSpace(m[1])
		if n := len(strings.Fields(phrase)); n == 0 || n > 3 || slices.Contains(own, strings.ToLower(phrase)) {
			continue
		}
		return phrase
	}
	if m := issueRef.FindStringSubmatch(text); m != nil {
		return "issue " + m[1]
	}
	return ""
}

// SourceWords names the MCP client that created a run as a person would: "claude-code" is
// "Claude Code". A client this does not know keeps its own name, tidied.
func SourceWords(client string) string {
	c := strings.TrimSpace(client)
	switch strings.ToLower(c) {
	case "":
		return ""
	case "claude-code", "claude code", "claude-ai", "claude":
		return "Claude Code"
	case "codex", "codex-mcp-client", "codex-cli":
		return "Codex"
	case "cursor", "cursor-vscode":
		return "Cursor"
	case "gemini", "gemini-cli", "gemini-cli-mcp-client":
		return "Gemini CLI"
	case "opencode":
		return "opencode"
	case "github-copilot", "copilot", "copilot-cli":
		return "GitHub Copilot"
	case "vscode", "visual studio code":
		return "VS Code"
	case "greenroom-bench", "bench":
		return "the bench"
	}
	words := strings.FieldsFunc(c, func(r rune) bool { return r == '-' || r == '_' || unicode.IsSpace(r) })
	return clipWords(strings.Join(words, " "), 3)
}

// clipWords keeps the first n words of s, marking a cut with an ellipsis on the last one.
func clipWords(s string, n int) string {
	words := strings.Fields(s)
	if len(words) <= n {
		return strings.Join(words, " ")
	}
	kept := strings.Join(words[:n], " ")
	return strings.TrimRight(kept, ".,;:!?-") + "…"
}

var (
	// stepRefs are a verifier's asides that cite its records: "(steps 19, 20)", "(step 5, element 7)".
	stepRefs = regexp.MustCompile(`\s*\((?:[^()]*\b(?:steps?|elements?|UI reads?|screenshots?)\s+\d[^()]*)\)`)
	// daemonNote is the note greenroom appends to a verdict it reviewed ("[greenroom] Reported as ...").
	daemonNote  = regexp.MustCompile(`\s*\[greenroom\].*$`)
	spaceBefore = regexp.MustCompile(`\s+([.,;:!?])`)
	toolName    = regexp.MustCompile(`\b(?:machine|agent|run|report|declare)_[a-z_]+\b`)
)

// toolWords say what a tool a verifier names in its prose does.
var toolWords = map[string]string{
	"machine_screenshot": "a screenshot",
	"machine_ui":         "a read of the screen",
	"machine_exec":       "a command",
	"machine_input":      "input",
	"machine_click":      "a click",
	"machine_key":        "a key press",
}

// plain is a verifier's sentence on one line, without its record citations and with any tool
// it names said in words.
func plain(s string) string {
	s = daemonNote.ReplaceAllString(strings.Join(strings.Fields(s), " "), "")
	s = stepRefs.ReplaceAllString(s, "")
	s = toolName.ReplaceAllStringFunc(s, func(name string) string {
		if w, ok := toolWords[name]; ok {
			return w
		}
		return "a tool"
	})
	s = spaceBefore.ReplaceAllString(s, "$1")
	return strings.TrimSpace(s)
}

// value is money, a number or a percentage: "$50.00", "25%", "1,200".
var value = regexp.MustCompile(`[-+]?[$\x{20AC}\x{00A3}]?\d[\d,]*(?:\.\d+)?%?`)

// Disagreement finds the values a failed check's criterion and observation disagree on, from
// their words. Quoted text counts as a value. Either is empty when the words do not show it.
//
// saw is a value only the observation names: the one that follows a result word ("reads
// $8.00"), else the first. expected is the value the observation itself says was wanted
// ("not $48.00", "instead of $50.00"), else a value of saw's kind from the criterion: one the
// observation does not repeat, that follows a result word ("Each pays reads $48.00"), before
// one that only sets the scene ("With Bill 120"). Taking the first value of each (the live
// check's criterion opens "With Bill 120, 20% tip, People 3") gave "expected 120".
func Disagreement(criterion, observed string) (expected, saw string) {
	want, got := valuesAt(criterion), valuesAt(observed)
	for _, g := range got {
		if g.follows(observed, wantedLead) {
			expected = g.v
			break
		}
	}
	saw = pick(got, observed, func(f found) bool { return f.v != expected && !has(want, f.v) })
	if expected != "" || saw == "" {
		return expected, saw
	}
	kind := valueKind(saw)
	ofKind := func(f found) bool { return f.v != saw && valueKind(f.v) == kind }
	expected = pick(want, criterion, func(f found) bool { return ofKind(f) && !has(got, f.v) })
	if expected == "" {
		expected = pick(want, criterion, ofKind)
	}
	return expected, saw
}

var (
	// resultLead ends the words before a value that is a result: "Each pays reads ".
	resultLead = regexp.MustCompile(`(?i)\b(?:reads?|is|are|was|were|shows?|showed|shown|becomes?|became|says?|said|displays?|displayed|equals?|gives?|gave|to)\s+(?:exactly\s+|only\s+|now\s+|still\s+|as\s+)?$`)
	// wantedLead ends the words before a value the observation says was wanted: "not ".
	wantedLead = regexp.MustCompile(`(?i)\b(?:not(?:\s+(?:read|show|say|display|equal|be)s?)?|instead of|rather than|expected|should (?:be|read|show|say))\s+(?:the\s+)?(?:expected\s+)?$`)
)

// found is a value and where it starts in its text.
type found struct {
	at int
	v  string
}

// follows reports whether the words of s before the value end as lead does.
func (f found) follows(s string, lead *regexp.Regexp) bool {
	return f.at <= len(s) && lead.MatchString(s[:f.at])
}

// pick is the first value ok allows that follows a result word in s, else the first it allows.
func pick(vals []found, s string, ok func(found) bool) string {
	first := ""
	for _, f := range vals {
		if !ok(f) {
			continue
		}
		if f.follows(s, resultLead) {
			return f.v
		}
		if first == "" {
			first = f.v
		}
	}
	return first
}

func has(vals []found, v string) bool {
	return slices.ContainsFunc(vals, func(f found) bool { return f.v == v })
}

// valuesAt lists the quoted phrases and the numeric values in s, in the order they appear. A
// number inside a quote is listed too, after its quote.
func valuesAt(s string) []found {
	var all []found
	for _, m := range quotedValue.FindAllStringSubmatchIndex(s, -1) {
		if v := strings.TrimSpace(s[m[2]:m[3]]); v != "" {
			all = append(all, found{m[0], "“" + v + "”"})
		}
	}
	for _, m := range value.FindAllStringIndex(s, -1) {
		all = append(all, found{m[0], strings.TrimRight(s[m[0]:m[1]], ",")})
	}
	slices.SortStableFunc(all, func(a, b found) int { return a.at - b.at })
	return all
}

var quotedValue = regexp.MustCompile(`["\x{201C}]([^"\x{201C}\x{201D}]{1,60})["\x{201D}]`)

func valueKind(v string) string {
	switch {
	case strings.HasPrefix(v, "“"):
		return "text"
	case strings.ContainsAny(v, "$€£"):
		return "money"
	case strings.HasSuffix(v, "%"):
		return "percent"
	}
	return "number"
}

// remarshal copies a decoded JSON value (a step's input or output) into out.
func remarshal(in any, out any) bool {
	if in == nil {
		return false
	}
	data, err := json.Marshal(in)
	if err != nil {
		return false
	}
	return json.Unmarshal(data, out) == nil
}
