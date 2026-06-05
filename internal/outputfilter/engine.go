package outputfilter

// Engine holds the ordered list of builtin filters and applies the first
// matching one to command output.
type Engine struct {
	filters []*Filter
}

// NewEngine returns an Engine pre-loaded with all builtin filters.
func NewEngine() *Engine {
	return &Engine{filters: builtinFilters}
}

// Apply finds the first filter whose MatchCommand regex matches cmd and runs
// the pipeline on output. Returns (filteredOutput, filterName, true) when a
// filter matched. Returns ("", "", false) when no filter matches (passthrough).
func (e *Engine) Apply(cmd, output string) (filtered string, name string, matched bool) {
	for _, f := range e.filters {
		result, ok := f.Apply(cmd, output)
		if ok {
			return result, f.Name, true
		}
	}
	return "", "", false
}
