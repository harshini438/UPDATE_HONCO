package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// escapeLike is what stops a search term being read as a pattern. These
// cases are the difference between "find rows containing 100%" and "find
// every row".
func TestEscapeLike(t *testing.T) {
	cases := []struct{ in, want string }{
		{"login", "login"},
		{"100%", `100\%`},
		{"a_b", `a\_b`},
		{`back\slash`, `back\\slash`},
		{"%_%", `\%\_\%`},
		{"", ""},
	}
	for _, c := range cases {
		if got := escapeLike(c.in); got != c.want {
			t.Errorf("escapeLike(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The backslash must be escaped before the wildcards, or the escapes
// themselves get escaped and the pattern breaks.
func TestEscapeLikeOrdering(t *testing.T) {
	if got := escapeLike(`\%`); got != `\\\%` {
		t.Errorf(`escapeLike("\\%%") = %q, want %q`, got, `\\\%`)
	}
}

func TestPlaceholders(t *testing.T) {
	sql, args := placeholders(3, []string{"a", "b", "c"})
	if sql != "$3,$4,$5" {
		t.Errorf("placeholders sql = %q", sql)
	}
	if len(args) != 3 || args[0] != "a" || args[2] != "c" {
		t.Errorf("placeholders args = %v", args)
	}

	sql, args = placeholders(1, nil)
	if sql != "" || len(args) != 0 {
		t.Errorf("empty placeholders = %q %v", sql, args)
	}
}

// An empty scope must mean "sees nothing", never "sees everything". Every
// query builder checks this before it runs.
func TestScopeEmpty(t *testing.T) {
	if !(&searchScope{}).empty() {
		t.Error("a scope with no teams and no channels should be empty")
	}
	if (&searchScope{TeamIDs: []string{"t"}}).empty() {
		t.Error("a scope with a team is not empty")
	}
	if (&searchScope{ChannelIDs: []string{"c"}}).empty() {
		t.Error("a scope with a channel is not empty")
	}
}

func params(query string) (*searchParams, string) {
	return parseSearchParams(httptest.NewRequest("GET", "/search?"+query, nil))
}

func TestParseSearchParamsRejects(t *testing.T) {
	cases := []struct{ query, wantErr string }{
		{"", "required"},
		{"q=", "required"},
		{"q=%20%20", "required"},
		{"q=a", "at least 2"},
		{"q=" + strings.Repeat("x", 129), "at most 128"},
		{"q=login&type=passwords", "unknown search type"},
	}
	for _, c := range cases {
		got, problem := params(c.query)
		if problem == "" {
			t.Errorf("%q was accepted, expected a refusal", c.query)
			continue
		}
		if !strings.Contains(problem, c.wantErr) {
			t.Errorf("%q -> %q, want something containing %q", c.query, problem, c.wantErr)
		}
		if got != nil {
			t.Errorf("%q returned params alongside a refusal", c.query)
		}
	}
}

func TestParseSearchParamsAccepts(t *testing.T) {
	p, problem := params("q=" + strings.Repeat("x", 128))
	if problem != "" {
		t.Fatalf("a 128 character query was refused: %s", problem)
	}
	if p.Type != SearchTypeAll {
		t.Errorf("default type = %q, want %q", p.Type, SearchTypeAll)
	}
	if p.Limit != searchDefaultLimit {
		t.Errorf("default limit = %d, want %d", p.Limit, searchDefaultLimit)
	}

	for _, ty := range []string{SearchTypeTasks, SearchTypeMeetings,
		SearchTypeRecordings, SearchTypeSummaries, SearchTypeSupport} {
		if _, problem := params("q=login&type=" + ty); problem != "" {
			t.Errorf("type %q was refused: %s", ty, problem)
		}
	}
}

// A caller asking for more than we will serve gets the most we will serve,
// not an error and not what they asked for.
func TestParseSearchParamsClamps(t *testing.T) {
	cases := []struct {
		query     string
		wantPage  int
		wantLimit int
	}{
		{"q=login&limit=100000", 0, searchMaxLimit},
		{"q=login&limit=-1", 0, searchDefaultLimit},
		{"q=login&limit=0", 0, searchDefaultLimit},
		{"q=login&limit=abc", 0, searchDefaultLimit},
		{"q=login&page=-5", 0, searchDefaultLimit},
		{"q=login&page=999999", searchMaxPage, searchDefaultLimit},
		{"q=login&page=notanumber", 0, searchDefaultLimit},
	}
	for _, c := range cases {
		p, problem := params(c.query)
		if problem != "" {
			t.Errorf("%q was refused: %s", c.query, problem)
			continue
		}
		if p.Page != c.wantPage {
			t.Errorf("%q page = %d, want %d", c.query, p.Page, c.wantPage)
		}
		if p.Limit != c.wantLimit {
			t.Errorf("%q limit = %d, want %d", c.query, p.Limit, c.wantLimit)
		}
	}
}

// The query is trimmed, so a term of spaces is not a term.
func TestParseSearchParamsTrims(t *testing.T) {
	p, problem := params("q=%20%20login%20%20")
	if problem != "" {
		t.Fatalf("refused: %s", problem)
	}
	if p.Query != "login" {
		t.Errorf("query = %q, want %q", p.Query, "login")
	}
}

// The rank expression is a three-way CASE and nothing more. This asserts
// its shape so it cannot quietly turn into something that claims to be a
// relevance score.
func TestRankExprIsThreeWay(t *testing.T) {
	got := rankExpr("title", 1, 2)
	for _, want := range []string{"THEN 0", "THEN 1", "ELSE 2", "ILIKE $2", `ESCAPE '\'`} {
		if !strings.Contains(got, want) {
			t.Errorf("rankExpr missing %q, got %q", want, got)
		}
	}
}
