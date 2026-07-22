package user

import (
	"strings"
	"testing"
)

func TestValidateNicknameLen(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"empty ok", "", false},
		{"1 char", "a", false},
		{"16 chars", strings.Repeat("a", 16), false},
		{"17 chars too long", strings.Repeat("a", 17), true},
		{"100 chars", strings.Repeat("x", 100), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateNicknameLen(c.in)
			if (err != nil) != c.wantErr {
				t.Errorf("validateNicknameLen(%q) err=%v, wantErr=%v",
					c.in, err, c.wantErr)
			}
		})
	}
}

func TestStripHTML(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain", "正常昵称", "正常昵称"},
		{"script tag", "<script>x</script>", "x"},
		{"img tag", "<img src=x/>", ""},
		{"partial tag", "<b>bold", "bold"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stripHTML(c.in)
			if got != c.want {
				t.Errorf("stripHTML(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestStripControlChars(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain chinese", "你好", "你好"},
		{"emoji with ZWJ preserved", "👨‍👩‍👧", "👨‍👩‍👧"},
		{"null byte removed", "\x00昵称", "昵称"},
		{"tab and newline removed", "a\tb\nc", "abc"},
		{"ANSI ESC removed but brackets kept", "\x1b[31mred\x1b[0m", "[31mred[0m"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stripControlChars(c.in)
			if got != c.want {
				t.Errorf("stripControlChars(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
