package insights

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// Every list route takes the same parameters and answers in the same envelope, so
// the web app has one table to build and an agent has one thing to learn.
const (
	defaultPageSize = 20
	maxPageSize     = 100
	maxQuery        = 200
)

// ListParams are the paging, sorting and search of a list request.
type ListParams struct {
	Page, PageSize int
	Sort           string
	Desc           bool
	Q              string
}

// sortSpec says what a route may be sorted by, and how it is when asked for
// nothing.
type sortSpec struct {
	allowed []string
	def     string
	defDesc bool
}

// parseList reads and checks page, page_size, sort, order and q. A value that is
// not allowed is an error and not a default: a client that misspells a sort field
// would otherwise get an order it did not ask for and think it had.
func parseList(r *http.Request, spec sortSpec) (ListParams, error) {
	v := r.URL.Query()
	p := ListParams{Page: 1, PageSize: defaultPageSize, Sort: spec.def, Desc: spec.defDesc, Q: v.Get("q")}
	if s := v.Get("page"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 1_000_000 {
			return p, fmt.Errorf("page must be a whole number from 1")
		}
		p.Page = n
	}
	if s := v.Get("page_size"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxPageSize {
			return p, fmt.Errorf("page_size must be a whole number from 1 to %d", maxPageSize)
		}
		p.PageSize = n
	}
	if s := v.Get("sort"); s != "" {
		if !slices.Contains(spec.allowed, s) {
			return p, fmt.Errorf("sort must be one of %s", strings.Join(spec.allowed, ", "))
		}
		p.Sort = s
	}
	switch v.Get("order") {
	case "":
	case "asc":
		p.Desc = false
	case "desc":
		p.Desc = true
	default:
		return p, fmt.Errorf("order must be asc or desc")
	}
	if len(p.Q) > maxQuery {
		return p, fmt.Errorf("q must be at most %d characters", maxQuery)
	}
	p.Q = strings.TrimSpace(p.Q)
	return p, nil
}

// Paged is one page of a list, with the size of the whole of it.
type Paged[T any] struct {
	Items    []T `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
	Pages    int `json:"pages"`
}

// paginate cuts a page out of rows that are already filtered and sorted. A page
// past the end is empty with the true total, which is a place a person can reach
// by deleting rows, not an error.
func paginate[T any](rows []T, p ListParams) Paged[T] {
	out := Paged[T]{Items: []T{}, Total: len(rows), Page: p.Page, PageSize: p.PageSize}
	out.Pages = (len(rows) + p.PageSize - 1) / p.PageSize
	start := (p.Page - 1) * p.PageSize
	if start < len(rows) {
		out.Items = rows[start:min(start+p.PageSize, len(rows))]
	}
	return out
}

// contains reports whether any field holds q, ignoring case. An empty q matches
// everything.
func contains(q string, fields ...string) bool {
	if q == "" {
		return true
	}
	q = strings.ToLower(q)
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

// sortRows sorts by the comparison for p.Sort. The sort is stable, so rows that
// compare equal keep the order the store gave them, and a page does not shuffle
// between two requests.
func sortRows[T any](rows []T, p ListParams, by map[string]func(a, b T) int) {
	cmpf, ok := by[p.Sort]
	if !ok {
		return
	}
	slices.SortStableFunc(rows, func(a, b T) int {
		c := cmpf(a, b)
		if p.Desc {
			return -c
		}
		return c
	})
}
