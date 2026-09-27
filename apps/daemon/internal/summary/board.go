package summary

import (
	"fmt"
	"slices"
	"time"
)

// Board is every run's summary in its group, with the counts a list heads itself with.
type Board struct {
	Groups []GroupRuns `json:"groups"`
	Macs   Macs        `json:"macs"`
	// UpdatedAt is the newest summary's.
	UpdatedAt time.Time `json:"updatedAt"`
}

// GroupRuns is one group of the board, newest first.
type GroupRuns struct {
	ID    Group     `json:"id"`
	Title string    `json:"title"`
	Count int       `json:"count"`
	Runs  []Summary `json:"runs"`
}

// Macs is how many machines the host may still start.
type Macs struct {
	Free  int `json:"free"`
	Total int `json:"total"`
	// Text is "2 of 3 Macs free"; empty when the daemon has no limit.
	Text string `json:"text,omitempty"`
}

// MacsFree words the host's capacity: limit machines at most, inUse of them taken. A limit
// of 0 or less means none.
func MacsFree(limit, inUse int) Macs {
	if limit <= 0 {
		return Macs{}
	}
	free := max(0, limit-inUse)
	return Macs{Free: free, Total: limit, Text: fmt.Sprintf("%d of %d %s free", free, limit, plural(limit, "Mac"))}
}

// NewBoard groups runs. Every group is present, empty or not, in list order; within one, the
// run that entered its status last comes first, then the newer run.
func NewBoard(runs []Summary, macs Macs) Board {
	b := Board{Macs: macs, Groups: make([]GroupRuns, 0, len(Groups))}
	for _, g := range Groups {
		gr := GroupRuns{ID: g, Title: g.Title(), Runs: []Summary{}}
		for _, s := range runs {
			if s.Group == g {
				gr.Runs = append(gr.Runs, s)
			}
		}
		slices.SortStableFunc(gr.Runs, func(a, b Summary) int {
			if c := b.Since.Compare(a.Since); c != 0 {
				return c
			}
			return b.StartedAt.Compare(a.StartedAt)
		})
		gr.Count = len(gr.Runs)
		b.Groups = append(b.Groups, gr)
	}
	for _, s := range runs {
		b.UpdatedAt = laterOf(b.UpdatedAt, s.UpdatedAt)
	}
	return b
}
