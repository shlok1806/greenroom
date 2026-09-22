package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := `# a comment
GR_PLAIN=plain
export GR_EXPORTED=exported
GR_DOUBLE="double quoted # not a comment"
GR_SINGLE='single quoted'
GR_QUOTED_THEN_COMMENT="value" # trailing comment
GR_MISMATCHED="half
GR_INNER=it's
GR_COMMENT=value # trailing comment
GR_TAB_COMMENT=value	# trailing comment
GR_HASH=a#b
GR_ONLY_COMMENT= # nothing here
GR_EQUALS=a=b
GR_SET=from file
not a pair

`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"GR_PLAIN":               "plain",
		"GR_EXPORTED":            "exported",
		"GR_DOUBLE":              "double quoted # not a comment",
		"GR_SINGLE":              "single quoted",
		"GR_QUOTED_THEN_COMMENT": "value",
		"GR_MISMATCHED":          `"half`,
		"GR_INNER":               "it's",
		"GR_COMMENT":             "value",
		"GR_TAB_COMMENT":         "value",
		"GR_HASH":                "a#b",
		"GR_ONLY_COMMENT":        "",
		"GR_EQUALS":              "a=b",
		"GR_SET":                 "from environment",
	}
	for k := range want {
		t.Setenv(k, "") // registers cleanup
		_ = os.Unsetenv(k)
	}
	t.Setenv("GR_SET", "from environment")

	if err := loadEnvFile(path); err != nil {
		t.Fatalf("loadEnvFile: %v", err)
	}
	for k, v := range want {
		got, ok := os.LookupEnv(k)
		if !ok || got != v {
			t.Errorf("%s = %q (set %v), want %q", k, got, ok, v)
		}
	}
}

func TestLoadEnvFileWithoutAFileIsFine(t *testing.T) {
	if err := loadEnvFile(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Errorf("a missing file failed: %v", err)
	}
}
