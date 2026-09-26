// Package bench is the verifier bench (ADR 0025): cases with known verdicts, a live runner that
// drives machine.Manager and the verifier actor in process, and a scorer that reports the false
// pass rate with its exact upper bound. The cases and fixture apps live in bench/ at the repo root.
package bench

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Expected verdicts. A case expecting AskOrInconclusive is right when the turn ends with a
// question or an inconclusive verdict.
const (
	ExpectPass         = "pass"
	ExpectFail         = "fail"
	AskOrInconclusive  = "ask_or_inconclusive"
	SplitDev           = "dev"
	SplitHoldout       = "holdout"
	KindCorrect        = "correct"
	KindMutant         = "mutant"
	KindLying          = "lying"
	KindInfra          = "infra"
	KindAmbiguous      = "ambiguous"
	InfraDialog        = "dialog"
	InfraAppNotRunning = "app_not_running"
	InfraTakeover      = "human_takeover"
	InfraBooting       = "booting"
	TierSimple         = "simple"
)

// Families are the mutant operator families of ADR 0025, in its order.
var Families = []string{
	"wrong_computation",   // off by one, rounding, a wrong operator
	"dead_control",        // a control with no action
	"wrong_label_format",  // a wrong label or value format
	"not_persisted",       // state lost across a relaunch
	"example_only",        // works only for the example input
	"adjacent_regression", // the change works, a control next to it broke
	"edge_crash",          // a crash on an edge input
	"visual_only",         // the screen is wrong, the accessibility tree is not
	"late_result",         // the result appears seconds late
}

// Kinds are the case kinds of ADR 0025.
var Kinds = []string{KindCorrect, KindMutant, KindLying, KindInfra, KindAmbiguous}

// InfraTypes are the disturbances the runner can play.
var InfraTypes = []string{InfraDialog, InfraAppNotRunning, InfraTakeover, InfraBooting}

// Tiers are the values a case's tier may take. A simple case checks one flow of one app with
// explicit steps and an explicit, observable outcome: about 5 inputs or fewer and 1 to 4
// checks (bench/README.md). A case with no tier is not tiered.
var Tiers = []string{TierSimple}

// Infra is a scripted disturbance.
type Infra struct {
	Type string `json:"type"`
	// AfterSteps is, for human_takeover, how many verifier tool calls to let through before a
	// person takes the screen (default 1).
	AfterSteps int `json:"afterSteps,omitempty"`
}

// Case is one bench/cases/<id>.json.
type Case struct {
	ID        string   `json:"id"`
	App       string   `json:"app"`
	Kind      string   `json:"kind"`
	Family    string   `json:"family,omitempty"`
	Split     string   `json:"split"`
	Tier      string   `json:"tier,omitempty"` // TierSimple, or "" for not tiered
	Patch     *string  `json:"patch"`          // a unified diff under bench/cases, applied with -p1; null for none
	Task      string   `json:"task"`
	Expected  string   `json:"expected"`
	MustCheck []string `json:"must_check"`
	Infra     *Infra   `json:"infra,omitempty"`
	Notes     string   `json:"notes,omitempty"` // for people reading the case; never shown to the verifier
}

// PatchPath is the case's patch file, or "" when it has none.
func (c Case) PatchPath(benchDir string) string {
	if c.Patch == nil || *c.Patch == "" {
		return ""
	}
	return filepath.Join(benchDir, "cases", filepath.FromSlash(*c.Patch))
}

// InfraType is the disturbance's type, or "".
func (c Case) InfraType() string {
	if c.Infra == nil {
		return ""
	}
	return c.Infra.Type
}

// Broken reports whether the case's build is broken (a mutant, or a lying coder on a mutant).
func (c Case) Broken() bool { return c.Expected == ExpectFail }

// App is one bench/apps/<dir>/app.json: what build.sh produces and how to find it running.
type App struct {
	Dir      string `json:"-"`
	Name     string `json:"name"`     // the executable, the .app's base name and the process name
	BundleID string `json:"bundleId"` // the UserDefaults domain
}

// Bundle is the built app relative to the app directory.
func (a App) Bundle() string { return "build/" + a.Name + ".app" }

// LoadApp reads bench/apps/<dir>/app.json and checks build.sh is there.
func LoadApp(benchDir, dir string) (App, error) {
	root := filepath.Join(benchDir, "apps", dir)
	data, err := os.ReadFile(filepath.Join(root, "app.json"))
	if err != nil {
		return App{}, fmt.Errorf("app %s: %w", dir, err)
	}
	var a App
	if err := strictDecode(data, &a); err != nil {
		return App{}, fmt.Errorf("app %s: app.json: %w", dir, err)
	}
	a.Dir = dir
	if a.Name == "" || a.BundleID == "" {
		return App{}, fmt.Errorf("app %s: app.json needs name and bundleId", dir)
	}
	st, err := os.Stat(filepath.Join(root, "build.sh"))
	if err != nil {
		return App{}, fmt.Errorf("app %s: %w", dir, err)
	}
	if st.Mode()&0o111 == 0 {
		return App{}, fmt.Errorf("app %s: build.sh is not executable", dir)
	}
	return a, nil
}

var idPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// LoadCases reads and validates every bench/cases/*.json, sorted by id. Patches must apply to
// their app; a case that breaks any rule is an error naming the file.
func LoadCases(benchDir string) ([]Case, error) {
	files, err := filepath.Glob(filepath.Join(benchDir, "cases", "*.json"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no cases in %s", filepath.Join(benchDir, "cases"))
	}
	var cases []Case
	var errs []error
	seen := map[string]bool{}
	for _, f := range files {
		c, err := LoadCase(benchDir, f)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if seen[c.ID] {
			errs = append(errs, fmt.Errorf("%s: duplicate id %s", f, c.ID))
		}
		seen[c.ID] = true
		cases = append(cases, c)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	return cases, nil
}

// LoadCase reads one case file and validates it against the apps and patches in benchDir.
func LoadCase(benchDir, file string) (Case, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return Case{}, err
	}
	var c Case
	if err := strictDecode(data, &c); err != nil {
		return Case{}, fmt.Errorf("%s: %w", file, err)
	}
	if want := strings.TrimSuffix(filepath.Base(file), ".json"); c.ID != want {
		return Case{}, fmt.Errorf("%s: id %q must match the file name %q", file, c.ID, want)
	}
	if err := c.Validate(); err != nil {
		return Case{}, fmt.Errorf("%s: %w", file, err)
	}
	if _, err := LoadApp(benchDir, c.App); err != nil {
		return Case{}, fmt.Errorf("%s: %w", file, err)
	}
	if p := c.PatchPath(benchDir); p != "" {
		// Applying to a scratch copy is the only proof the diff still fits the app.
		tmp, err := os.MkdirTemp("", "bench-case-")
		if err != nil {
			return Case{}, err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		if err := PrepareApp(benchDir, c, tmp); err != nil {
			return Case{}, fmt.Errorf("%s: %w", file, err)
		}
	}
	return c, nil
}

// Validate checks the rules a case follows by itself (ADR 0025): the kind decides the expected
// verdict, mutants carry a family and a patch, infra cases carry a disturbance.
func (c Case) Validate() error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if !idPattern.MatchString(c.ID) {
		bad("id %q must be lowercase words joined by -", c.ID)
	}
	if !idPattern.MatchString(c.App) {
		bad("app %q must name a directory under bench/apps", c.App)
	}
	if strings.TrimSpace(c.Task) == "" {
		bad("task is empty")
	}
	if c.Split != SplitDev && c.Split != SplitHoldout {
		bad("split must be %s or %s, not %q", SplitDev, SplitHoldout, c.Split)
	}
	hasPatch := c.Patch != nil && *c.Patch != ""
	if hasPatch && (strings.HasPrefix(*c.Patch, "/") || strings.Contains(*c.Patch, "..") || !strings.HasSuffix(*c.Patch, ".diff")) {
		bad("patch %q must be a .diff path under bench/cases, like patches/<id>.diff", *c.Patch)
	}
	if c.Tier != "" && !slices.Contains(Tiers, c.Tier) {
		bad("tier %q is not one of %s (or absent)", c.Tier, strings.Join(Tiers, ", "))
	}
	if c.Tier != "" && (c.Kind == KindInfra || c.Kind == KindAmbiguous) {
		bad("%s cases take no tier: only a checkable single-flow task is simple", c.Kind)
	}
	if c.Family != "" && !slices.Contains(Families, c.Family) {
		bad("family %q is not one of %s", c.Family, strings.Join(Families, ", "))
	}
	for i, m := range c.MustCheck {
		if strings.TrimSpace(m) == "" {
			bad("must_check[%d] is empty", i)
		}
	}
	switch c.Kind {
	case KindCorrect:
		want(&errs, c, ExpectPass)
		if c.Family != "" {
			bad("a correct case has no family")
		}
	case KindMutant:
		want(&errs, c, ExpectFail)
		if c.Family == "" {
			bad("a mutant needs a family")
		}
		if !hasPatch {
			bad("a mutant needs a patch")
		}
	case KindLying:
		if c.Expected != ExpectPass && c.Expected != ExpectFail {
			bad("a lying case expects pass (a correct build) or fail (a broken one), not %q", c.Expected)
		}
		if c.Expected == ExpectFail && (c.Family == "" || !hasPatch) {
			bad("a lying case on a broken build needs the mutant's family and patch")
		}
		if c.Expected == ExpectPass && c.Family != "" {
			bad("a lying case on a correct build has no family")
		}
	case KindInfra:
		want(&errs, c, AskOrInconclusive)
		if c.Infra == nil || !slices.Contains(InfraTypes, c.Infra.Type) {
			bad("an infra case needs infra.type, one of %s", strings.Join(InfraTypes, ", "))
		}
		if c.Family != "" {
			bad("an infra case has no family")
		}
	case KindAmbiguous:
		want(&errs, c, AskOrInconclusive)
		if c.Family != "" {
			bad("an ambiguous case has no family")
		}
	default:
		bad("kind %q is not one of %s", c.Kind, strings.Join(Kinds, ", "))
	}
	if c.Kind != KindInfra && c.Infra != nil {
		bad("only an infra case has infra")
	}
	if c.Infra != nil && c.Infra.AfterSteps < 0 {
		bad("infra.afterSteps must be 0 or more")
	}
	if (c.Kind == KindCorrect || c.Kind == KindMutant || c.Kind == KindLying) && len(c.MustCheck) == 0 {
		bad("a %s case needs must_check", c.Kind)
	}
	return errors.Join(errs...)
}

func want(errs *[]error, c Case, expected string) {
	if c.Expected != expected {
		*errs = append(*errs, fmt.Errorf("a %s case expects %s, not %q", c.Kind, expected, c.Expected))
	}
}

// strictDecode refuses unknown fields and trailing data, so a misspelt key is an error.
func strictDecode(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data after the JSON object")
	}
	return nil
}

// Filter selects cases by id (any of ids), split, kind and tier. Empty selects all.
func Filter(cases []Case, ids []string, split, kind, tier string) []Case {
	var out []Case
	for _, c := range cases {
		if len(ids) > 0 && !slices.Contains(ids, c.ID) {
			continue
		}
		if split != "" && c.Split != split {
			continue
		}
		if kind != "" && c.Kind != kind {
			continue
		}
		if tier != "" && c.Tier != tier {
			continue
		}
		out = append(out, c)
	}
	return out
}

// PrepareApp copies bench/apps/<app> into dest (which must exist and be empty) and applies
// the case's patch. Build output from a local run (build/, .build/) is not copied.
func PrepareApp(benchDir string, c Case, dest string) error {
	src := filepath.Join(benchDir, "apps", c.App)
	if err := copyTree(src, dest); err != nil {
		return fmt.Errorf("copy app %s: %w", c.App, err)
	}
	p := c.PatchPath(benchDir)
	if p == "" {
		return nil
	}
	diff, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	if err := ApplyPatch(dest, diff); err != nil {
		return fmt.Errorf("patch %s: %w", *c.Patch, err)
	}
	return nil
}

func copyTree(src, dest string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "build" || d.Name() == ".build") {
			return filepath.SkipDir
		}
		target := filepath.Join(dest, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case info.Mode().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, info.Mode().Perm())
		default:
			return fmt.Errorf("%s: only files and directories belong in a fixture app", rel)
		}
	})
}
