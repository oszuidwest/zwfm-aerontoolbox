package database

import "strconv"

// paramList accumulates positional query parameters and hands out their
// PostgreSQL placeholders ($1, $2, ...) in declaration order. next(v) binds v
// and returns its placeholder in one step, so a placeholder and its value can
// never drift out of sync - the class of bug a hand-maintained counter invites.
//
// A placeholder may be reused in the query string (e.g. "%[1]s::date AND
// < %[1]s::date") by calling next once and referencing the returned string
// multiple times; only call next again when a new value is bound.
type paramList struct {
	values []any
}

// next binds v and returns its placeholder (e.g. "$3").
func (p *paramList) next(v any) string {
	p.values = append(p.values, v)
	return "$" + strconv.Itoa(len(p.values))
}
