package buildinfo

import "testing"

func TestAnUnstampedBuildSaysSo(t *testing.T) {
	if got := Get(); got.Stamped() || got.String() != "unstamped build" {
		t.Fatalf("Get() = %+v, %q", got, got.String())
	}
}

func TestAStampedBuildNamesItsCommitAndTime(t *testing.T) {
	for _, tc := range []struct {
		info Info
		want string
	}{
		{Info{Commit: "abc1234", BuiltAt: "2026-09-27T10:00:00Z"}, "abc1234, built 2026-09-27T10:00:00Z"},
		{Info{Commit: "abc1234", Dirty: true, BuiltAt: "2026-09-27T10:00:00Z"}, "abc1234 (local changes), built 2026-09-27T10:00:00Z"},
		{Info{Commit: "abc1234"}, "abc1234"},
	} {
		if got := tc.info.String(); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.info, got, tc.want)
		}
	}
}

func TestTheStampsAreRead(t *testing.T) {
	old := [3]string{commit, dirty, builtAt}
	t.Cleanup(func() { commit, dirty, builtAt = old[0], old[1], old[2] })
	commit, dirty, builtAt = " abc1234\n", "true", "2026-09-27T10:00:00Z"
	got := Get()
	if got != (Info{Commit: "abc1234", Dirty: true, BuiltAt: "2026-09-27T10:00:00Z"}) {
		t.Fatalf("Get() = %+v", got)
	}
	dirty = "false"
	if Get().Dirty {
		t.Fatal("dirty=false read as dirty")
	}
}
