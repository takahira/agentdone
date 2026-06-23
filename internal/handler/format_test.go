package handler

import "testing"

func TestTruncate(t *testing.T) {
	// Short strings pass through; longer ones are cut on a RUNE boundary so
	// multi-byte text is never split mid-character.
	cases := []struct {
		s    string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 5, "hello"},
		{"日本語テスト", 3, "日本語"},    // 6 runes -> first 3 runes (9 bytes), not 3 bytes
		{"日本語テスト", 6, "日本語テスト"}, // exactly the length
	}
	for _, c := range cases {
		if got := truncate(c.s, c.n); got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
		}
	}
}

func TestTruncateHead(t *testing.T) {
	// confirmation excerpts keep the TAIL, where the question lives.
	if got := truncateHead("hello", 10); got != "hello" {
		t.Errorf("truncateHead short = %q, want unchanged", got)
	}
	if got := truncateHead("long summary then the question", 12); got != "…the question" {
		t.Errorf("truncateHead = %q, want …the question", got)
	}
}

func TestOneLine(t *testing.T) {
	if got := oneLine("a\nb\r\nc"); got != "a b  c" {
		t.Errorf("oneLine = %q, want %q", got, "a b  c")
	}
}

func TestFormatTokens(t *testing.T) {
	cases := map[int64]string{
		500:   "500",
		1000:  "1.0k",
		1099:  "1.1k",
		1500:  "1.5k",
		1950:  "2.0k",
		27300: "27.3k",
	}
	for n, want := range cases {
		if got := formatTokens(n); got != want {
			t.Errorf("formatTokens(%d) = %q, want %q", n, got, want)
		}
	}
}
