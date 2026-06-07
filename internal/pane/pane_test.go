package pane

import "testing"

func TestToToken(t *testing.T) {
	cases := []struct {
		in        string
		wantKind  string
		wantToken string
	}{
		{"esc", KindKey, "Escape"},
		{"Enter", KindKey, "Enter"},
		{"right arrow", KindKey, "Right"},
		{"PgUp", KindKey, "PageUp"},
		{"f5", KindKey, "F5"},
		{"ctrl+p", KindKey, "C-p"},
		{"alt+x", KindKey, "M-x"},
		{"  Tab  ", KindKey, "Tab"},
		{"m", KindLit, "m"},
		{"git status", KindLit, "git status"},
	}
	for _, c := range cases {
		kind, tok := ToToken(c.in)
		if kind != c.wantKind || tok != c.wantToken {
			t.Errorf("ToToken(%q) = (%s,%q), want (%s,%q)", c.in, kind, tok, c.wantKind, c.wantToken)
		}
	}
}
