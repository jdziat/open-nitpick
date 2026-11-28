package insights

import (
	"context"
	"sort"
)

// View is the reviews of one window, loaded once, with the analytics over them. A
// request builds one and asks it several questions, so the database is read once
// per request and a failed read is one error to report, not a quiet empty answer
// from every question.
type View struct{ reviews []Review }

// View loads what a Query can ask about: every repository, from the start of the
// window before the one asked for, because a card's change against the period
// before it and a hotspot's trend read both. Reviews dated after q.Now are left
// out, as the analytics leave them out.
func (s *Store) View(ctx context.Context, q Query) (*View, error) {
	_, priorFrom := q.window()
	reviews, err := s.load(ctx, Filter{Since: priorFrom, Until: q.Now})
	if err != nil {
		return nil, err
	}
	return &View{reviews: reviews}, nil
}

// Reviews lists the loaded reviews a filter selects, newest first.
func (v *View) Reviews(f Filter) []Review {
	var out []Review
	for _, r := range v.reviews {
		if f.Repo != "" && r.Repo != f.Repo || r.At.Before(f.Since) || !f.Until.IsZero() && r.At.After(f.Until) {
			continue
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// Len is how many reviews are loaded.
func (v *View) Len() int { return len(v.reviews) }

// NewViewOf wraps reviews as a View, for a caller that already has them.
func NewViewOf(reviews []Review) *View { return &View{reviews: reviews} }
