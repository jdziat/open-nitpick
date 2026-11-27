package server

import (
	"errors"
	"fmt"
	"strings"
)

// RoleRule grants roles to the identities it matches. An identity holds the
// union of the roles of every rule that matches it, and none when no rule does:
// a sign-in alone grants nothing.
type RoleRule struct {
	Roles []Role

	// Provider limits the rule to one sign-in provider. Empty matches any.
	Provider string

	// Exactly one of the following selects who the rule is for.
	User  string // the subject, matched exactly
	Group string // a group the provider reported, matched exactly
}

// Validate reports a rule that could never match, or that would match everyone.
func (r RoleRule) Validate() error {
	if len(r.Roles) == 0 {
		return errors.New("a role rule names no role")
	}
	for _, role := range r.Roles {
		if strings.TrimSpace(string(role)) == "" {
			return errors.New("a role rule names an empty role")
		}
	}
	selectors := 0
	for _, s := range []string{r.User, r.Group} {
		if s != "" {
			selectors++
		}
	}
	if selectors != 1 {
		return fmt.Errorf("a role rule must select by exactly one of user or group, got %d", selectors)
	}
	return nil
}

// RoleMap turns an identity into the roles it holds.
type RoleMap struct{ rules []RoleRule }

// NewRoleMap checks the rules and returns the map.
func NewRoleMap(rules []RoleRule) (*RoleMap, error) {
	for i, r := range rules {
		if err := r.Validate(); err != nil {
			return nil, fmt.Errorf("role rule %d: %w", i+1, err)
		}
	}
	return &RoleMap{rules: rules}, nil
}

// Grant returns the identity with the roles the rules give it.
func (m *RoleMap) Grant(id Identity) Identity {
	id.Roles = nil
	for _, r := range m.rules {
		if r.Provider != "" && r.Provider != id.Provider {
			continue
		}
		byUser := r.User != "" && r.User == id.Subject
		byGroup := r.Group != "" && containsString(id.Groups, r.Group)
		if !byUser && !byGroup {
			continue
		}
		for _, role := range r.Roles {
			if !id.Has(role) {
				id.Roles = append(id.Roles, role)
			}
		}
	}
	return id
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Check refuses a rule that names a role not in the declared set, and names the
// set. With nothing declared there is nothing to check against.
func (m *RoleMap) Check(declared []Role) error {
	if len(declared) == 0 {
		return nil
	}
	known := map[Role]bool{}
	var names []string
	for _, r := range declared {
		if !known[r] {
			known[r] = true
			names = append(names, string(r))
		}
	}
	for i, rule := range m.rules {
		for _, role := range rule.Roles {
			if !known[role] {
				return fmt.Errorf("role rule %d: no component knows the role %q; the roles are %s", i+1, role, strings.Join(names, ", "))
			}
		}
	}
	return nil
}
