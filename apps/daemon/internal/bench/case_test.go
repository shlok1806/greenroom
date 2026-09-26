package bench

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// repoBench is the real bench/ at the repo root.
func repoBench(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "bench"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cases")); err != nil {
		t.Fatalf("no bench/cases at %s: %v", dir, err)
	}
	return dir
}

// Every case file validates, and every patch applies to its app (LoadCase applies it).
func TestEveryBenchCaseIsValidAndItsPatchApplies(t *testing.T) {
	cases, err := LoadCases(repoBench(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if strings.Contains(c.Task, "—") || strings.Contains(c.Notes, "—") {
			t.Errorf("%s: no em dashes in case text", c.ID)
		}
	}
}

// ADR 0025's v1 sizes: 30 broken builds over the 9 families, correct, lying, infra and
// ambiguous cases, and a holdout of about a quarter.
func TestTheBenchMeetsTheFirstVersionsSizes(t *testing.T) {
	cases, err := LoadCases(repoBench(t))
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	families := map[string]int{}
	infra := map[string]int{}
	apps := map[string]int{}
	holdout, broken := 0, 0
	for _, c := range cases {
		kinds[c.Kind]++
		if c.Kind == KindMutant {
			families[c.Family]++
			apps[c.App]++
		}
		if c.Broken() {
			broken++
		}
		if c.Infra != nil {
			infra[c.Infra.Type]++
		}
		if c.Split == SplitHoldout {
			holdout++
		}
	}
	for kind, least := range map[string]int{KindMutant: 30, KindCorrect: 8, KindLying: 4, KindInfra: 4, KindAmbiguous: 2} {
		if kinds[kind] < least {
			t.Errorf("%d %s cases, want at least %d", kinds[kind], kind, least)
		}
	}
	if broken < 30 {
		t.Errorf("%d broken builds, want at least 30 (a 10%% bound at zero false passes)", broken)
	}
	for _, f := range Families {
		if families[f] < 2 {
			t.Errorf("family %s has %d mutants, want at least 2", f, families[f])
		}
	}
	for _, typ := range InfraTypes {
		if infra[typ] == 0 {
			t.Errorf("no infra case of type %s", typ)
		}
	}
	if len(apps) < 3 {
		t.Errorf("mutants span %d apps, want at least 3", len(apps))
	}
	if share := float64(holdout) / float64(len(cases)); share < 0.2 || share > 0.35 {
		t.Errorf("holdout is %d of %d (%.0f%%), want about a quarter", holdout, len(cases), share*100)
	}
}

// Every mutant's patch really changes its app, and no two mutants share one.
func TestEveryMutantChangesItsApp(t *testing.T) {
	dir := repoBench(t)
	cases, err := LoadCases(dir)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, c := range cases {
		if c.Kind != KindMutant {
			continue
		}
		if other, ok := seen[*c.Patch]; ok {
			t.Errorf("%s and %s share %s", c.ID, other, *c.Patch)
		}
		seen[*c.Patch] = c.ID
		base, patched := t.TempDir(), t.TempDir()
		if err := PrepareApp(dir, Case{App: c.App}, base); err != nil {
			t.Fatal(err)
		}
		if err := PrepareApp(dir, c, patched); err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		if treeText(t, base) == treeText(t, patched) {
			t.Errorf("%s: the patch changes nothing", c.ID)
		}
	}
}

func treeText(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		b.WriteString(p[len(dir):] + "\n" + string(data))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestEveryFixtureAppHasItsManifestAndBuildScript(t *testing.T) {
	dir := repoBench(t)
	entries, err := os.ReadDir(filepath.Join(dir, "apps"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		n++
		app, err := LoadApp(dir, e.Name())
		if err != nil {
			t.Error(err)
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "apps", e.Name(), "Sources", app.Name)); err != nil {
			t.Errorf("app %s: sources must be in Sources/%s, which build.sh compiles: %v", e.Name(), app.Name, err)
		}
		if !strings.HasPrefix(app.BundleID, "com.greenroom.bench.") {
			t.Errorf("app %s: bundle id %s should be com.greenroom.bench.<dir>", e.Name(), app.BundleID)
		}
	}
	if n < 3 {
		t.Errorf("%d fixture apps, want at least 3", n)
	}
}

func str(s string) *string { return &s }

func TestValidateRefusesCasesThatBreakTheRules(t *testing.T) {
	good := Case{ID: "a-mutant", App: "app", Kind: KindMutant, Family: "dead_control", Split: SplitDev,
		Patch: str("patches/a-mutant.diff"), Task: "Check it.", Expected: ExpectFail, MustCheck: []string{"x"}}
	if err := good.Validate(); err != nil {
		t.Fatalf("a good mutant was refused: %v", err)
	}
	for name, tt := range map[string]struct {
		edit func(*Case)
		want string
	}{
		"mutant expecting pass":      {func(c *Case) { c.Expected = ExpectPass }, "expects fail"},
		"mutant with no family":      {func(c *Case) { c.Family = "" }, "needs a family"},
		"mutant with no patch":       {func(c *Case) { c.Patch = nil }, "needs a patch"},
		"unknown family":             {func(c *Case) { c.Family = "typo" }, "not one of"},
		"unknown kind":               {func(c *Case) { c.Kind = "weird" }, "kind"},
		"bad split":                  {func(c *Case) { c.Split = "test" }, "split"},
		"empty task":                 {func(c *Case) { c.Task = " " }, "task is empty"},
		"no must_check":              {func(c *Case) { c.MustCheck = nil }, "needs must_check"},
		"patch leaving cases":        {func(c *Case) { c.Patch = str("../x.diff") }, "under bench/cases"},
		"bad id":                     {func(c *Case) { c.ID = "Bad_ID" }, "id"},
		"infra field on a mutant":    {func(c *Case) { c.Infra = &Infra{Type: InfraDialog} }, "only an infra case"},
		"correct with a family":      {func(c *Case) { c.Kind, c.Expected = KindCorrect, ExpectPass }, "no family"},
		"infra without a type":       {func(c *Case) { c.Kind, c.Family, c.Expected = KindInfra, "", AskOrInconclusive }, "infra.type"},
		"infra expecting a verdict":  {func(c *Case) { c.Kind, c.Family, c.Infra = KindInfra, "", &Infra{Type: InfraBooting} }, "expects ask_or_inconclusive"},
		"lying broken with no patch": {func(c *Case) { c.Kind, c.Patch = KindLying, nil }, "family and patch"},
		"ambiguous expecting pass":   {func(c *Case) { c.Kind, c.Family, c.Expected = KindAmbiguous, "", ExpectPass }, "expects ask_or_inconclusive"},
	} {
		c := good
		tt.edit(&c)
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want one containing %q", name, err, tt.want)
		}
	}
}

func TestLoadCaseRefusesUnknownFieldsAndAMismatchedID(t *testing.T) {
	dir := fixtureBench(t)
	write := func(name, body string) string {
		p := filepath.Join(dir, "cases", name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	ok := `{"id":"ok","app":"demo","kind":"correct","split":"dev","patch":null,"task":"Check.","expected":"pass","must_check":["x"]}`
	if _, err := LoadCase(dir, write("ok.json", ok)); err != nil {
		t.Fatalf("a good case was refused: %v", err)
	}
	typo := strings.Replace(ok, `"ok"`, `"typo"`, 1)
	if _, err := LoadCase(dir, write("typo.json", typo[:len(typo)-1]+`,"mustcheck":[]}`)); err == nil ||
		!strings.Contains(err.Error(), "unknown field") {
		t.Errorf("a misspelt field was accepted: %v", err)
	}
	if _, err := LoadCase(dir, write("other.json", ok)); err == nil || !strings.Contains(err.Error(), "must match the file name") {
		t.Errorf("an id that is not the file name was accepted: %v", err)
	}
	if _, err := LoadCase(dir, write("noapp.json", strings.Replace(strings.Replace(ok, `"ok"`, `"noapp"`, 1), `"demo"`, `"missing"`, 1))); err == nil {
		t.Error("a case for a missing app was accepted")
	}
	bad := strings.Replace(strings.Replace(ok, `"ok"`, `"badpatch"`, 1), `"patch":null`, `"patch":"patches/bad.diff"`, 1)
	if err := os.WriteFile(filepath.Join(dir, "cases", "patches", "bad.diff"), []byte("--- a/main.swift\n+++ b/main.swift\n@@ -1 +1 @@\n-not in the file\n+x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCase(dir, write("badpatch.json", bad)); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("a patch that does not apply was accepted: %v", err)
	}
}

func TestFilterSelectsByIDSplitAndKind(t *testing.T) {
	cases := []Case{
		{ID: "a", Split: SplitDev, Kind: KindMutant},
		{ID: "b", Split: SplitHoldout, Kind: KindMutant},
		{ID: "c", Split: SplitDev, Kind: KindCorrect},
	}
	ids := func(cs []Case) []string {
		var out []string
		for _, c := range cs {
			out = append(out, c.ID)
		}
		return out
	}
	for name, tt := range map[string]struct {
		got, want []string
	}{
		"all":     {ids(Filter(cases, nil, "", "")), []string{"a", "b", "c"}},
		"ids":     {ids(Filter(cases, []string{"c", "a"}, "", "")), []string{"a", "c"}},
		"split":   {ids(Filter(cases, nil, SplitHoldout, "")), []string{"b"}},
		"kind":    {ids(Filter(cases, nil, "", KindMutant)), []string{"a", "b"}},
		"both":    {ids(Filter(cases, nil, SplitDev, KindMutant)), []string{"a"}},
		"no hits": {ids(Filter(cases, []string{"z"}, "", "")), nil},
	} {
		if !slices.Equal(tt.got, tt.want) {
			t.Errorf("%s: got %v, want %v", name, tt.got, tt.want)
		}
	}
}

// fixtureBench is a tiny bench with one app, "demo", for tests that write their own cases.
func fixtureBench(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{"cases/patches", "apps/demo/Sources/Demo"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"apps/demo/app.json":                `{"name": "Demo", "bundleId": "com.greenroom.bench.demo"}`,
		"apps/demo/build.sh":                "#!/bin/sh\necho built\n",
		"apps/demo/main.swift":              "let a = 1\nlet b = 2\nprint(a + b)\n",
		"apps/demo/Sources/Demo/main.swift": "print(1)\n",
	}
	for name, body := range files {
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
