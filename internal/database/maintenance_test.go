package database

import "testing"

func TestLongRunningQueryTextExpr(t *testing.T) {
	if got := longRunningQueryTextExpr(false); got != "''" {
		t.Fatalf("longRunningQueryTextExpr(false) = %q, want empty SQL literal", got)
	}
	if got := longRunningQueryTextExpr(true); got != "LEFT(query, 200)" {
		t.Fatalf("longRunningQueryTextExpr(true) = %q, want query excerpt expression", got)
	}
}
