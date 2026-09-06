package main

import (
	"sort"
	"testing"
	"time"
)

// Commit dates carry the committer's offset, so a string order sorted by
// local time: 2026-01-01T23:00:00+05:00 is earlier than
// 2026-01-01T20:00:00Z, and a string order puts it after.
func TestCommitsSortByInstantNotByLocalTime(t *testing.T) {
	commits := []commit{{sha: "later", date: "2026-01-01T20:00:00Z"}, {sha: "earlier", date: "2026-01-01T23:00:00+05:00"}}
	when := func(c commit) time.Time {
		tm, err := time.Parse(time.RFC3339, c.date)
		if err != nil {
			return time.Time{}
		}
		return tm
	}
	sort.Slice(commits, func(i, j int) bool { return when(commits[i]).Before(when(commits[j])) })
	if commits[0].sha != "earlier" {
		t.Errorf("order = %s, %s; want the +05:00 commit first", commits[0].sha, commits[1].sha)
	}
}
