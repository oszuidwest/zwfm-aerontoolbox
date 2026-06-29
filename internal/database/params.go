package database

import "strconv"

// paramList binds positional query parameters and hands out their $N
// placeholders in order, so a placeholder and its value can never drift out of
// sync. Reuse a placeholder by calling next once and referencing the result.
type paramList struct {
	values []any
}

// next binds v and returns its placeholder (e.g. "$3").
func (p *paramList) next(v any) string {
	p.values = append(p.values, v)
	return "$" + strconv.Itoa(len(p.values))
}
