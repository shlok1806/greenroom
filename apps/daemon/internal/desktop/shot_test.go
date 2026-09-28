package desktop

import (
	"encoding/json"
	"testing"
)

func TestShotArgs(t *testing.T) {
	screen := Screen{Width: 1024, Height: 768}
	for _, tc := range []struct {
		name    string
		raw     string
		crops   bool
		wire    string
		wantErr string
	}{
		{name: "the whole screen", raw: `{"runId":"r1"}`, wire: `{"format":"png"}`},
		{name: "a ref with the agent's margin", raw: `{"ref":"e41"}`, crops: true, wire: `{"format":"png","ref":"e41"}`},
		{name: "a ref with a margin", raw: `{"ref":"e41","margin":8}`, crops: true, wire: `{"format":"png","ref":"e41","margin":8}`},
		{name: "a margin is clamped", raw: `{"ref":"e41","margin":9000}`, crops: true, wire: `{"format":"png","ref":"e41","margin":200}`},
		{name: "a window by ref has no margin", raw: `{"window":"e1"}`, crops: true, wire: `{"format":"png","ref":"e1","margin":0}`},
		{name: "a window by title is resolved by the caller", raw: `{"window":"Settings"}`, crops: true, wire: `{"format":"png"}`},
		{name: "a region in fractions becomes points", raw: `{"region":[0.25,0.5,0.5,0.25]}`, crops: true,
			wire: `{"format":"png","rect":[256,384,512,192]}`},
		{name: "two crops", raw: `{"ref":"e41","window":"e1"}`, wantErr: "ref: pass one of ref, window or region, not several"},
		{name: "a bare number", raw: `{"ref":"17"}`, wantErr: `ref: "17" is not a ref; refs look like "e17" and come from machine_snapshot, machine_find or an action's result (machine_ui's element numbers are not refs)`},
		{name: "a short region", raw: `{"region":[0.1,0.1]}`, wantErr: "region: has 2 numbers; pass [x, y, w, h] as fractions of the screen, 0 to 1, such as [0.25, 0.25, 0.5, 0.5]"},
		{name: "a region past the edge", raw: `{"region":[0.5,0.5,0.6,0.1]}`, wantErr: "region: reaches past the edge of the screen (x + w and y + h must be at most 1); pass [x, y, w, h] as fractions of the screen, 0 to 1, such as [0.25, 0.25, 0.5, 0.5]"},
		{name: "a region with no area", raw: `{"region":[0.5,0.5,0,0.1]}`, wantErr: "region: has no area: w and h must be above 0; pass [x, y, w, h] as fractions of the screen, 0 to 1, such as [0.25, 0.25, 0.5, 0.5]"},
		{name: "a margin with no crop", raw: `{"margin":4}`, wantErr: "margin: a margin is for a crop to ref or window; leave it out"},
		{name: "a negative margin", raw: `{"ref":"e2","margin":-4}`, wantErr: "margin: -4 is negative; pass points of margin, 0 to 200"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := parse[ShotArgs](tc.raw)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("error %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if a.Crops() != tc.crops {
				t.Errorf("crops %v, want %v", a.Crops(), tc.crops)
			}
			wire, _ := json.Marshal(a.Op(screen))
			if string(wire) != tc.wire {
				t.Errorf("wire %s, want %s", wire, tc.wire)
			}
		})
	}
}
