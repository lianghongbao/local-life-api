package user

import (
	"testing"
)

func TestPhoneRE(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"valid 138", "13800138000", true},
		{"valid 159", "15912345678", true},
		{"valid 188", "18888888888", true},
		{"invalid 120", "12000000000", false},
		{"invalid 11 digits wrong start", "02345678901", false},
		{"too short 10 digits", "1380013800", false},
		{"too long 12 digits", "138001380001", false},
		{"letters", "1380013800a", false},
		{"with dash", "138-0013-8000", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := phoneRE.MatchString(c.in)
			if got != c.want {
				t.Errorf("phoneRE.MatchString(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
