package db

// Args accumulates positional query parameters and tracks the SQL $N
// placeholder for each — the shared piece of the "conds []string / args
// []any / n++" pattern hand-rolled across several repositories for dynamic
// WHERE clauses (organization file/webhook-delivery listing, notification
// message listing, account login-event listing, platform/audit filtering).
// It only tracks argument order/indexing; callers still compose their own
// condition strings and gate each Add call with their own `if`, matching
// every existing call site's style — this isn't a full query builder, just
// the part that was actually duplicated.
type Args struct {
	values []any
}

// NewArgs seeds the accumulator with parameters already bound ahead of the
// first optional condition (e.g. a required "organization_id = $1"), so the
// next Add continues the placeholder numbering correctly.
func NewArgs(seed ...any) *Args {
	return &Args{values: append([]any{}, seed...)}
}

// Add appends value and returns its 1-based placeholder position — use it
// to format the "$N" in a condition string, e.g.:
//
//	if folderID != nil {
//	    conds = append(conds, fmt.Sprintf("folder_id = $%d", args.Add(*folderID)))
//	}
func (a *Args) Add(value any) int {
	a.values = append(a.values, value)
	return len(a.values)
}

// Next returns the placeholder position the *next* Add call would use,
// without adding anything — for a trailing LIMIT/OFFSET appended after a
// conds slice that's already been joined into the query.
func (a *Args) Next() int {
	return len(a.values) + 1
}

// Values returns the accumulated arguments in placeholder order, ready to
// pass as the variadic query parameters.
func (a *Args) Values() []any {
	return a.values
}
