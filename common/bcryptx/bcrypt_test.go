package bcryptx

import (
	"errors"
	"strings"
	"testing"
)

func TestHashAndVerify_RoundTrip(t *testing.T) {
	passwords := []string{
		"hello123",
		"very-long-password-with-special-chars-!@#$%^&*()",
		"中文密码-emoji-🎉",
		"",
	}

	for _, p := range passwords {
		hash, err := Hash(p)
		if err != nil {
			t.Fatalf("Hash(%q) unexpected error: %v", p, err)
		}
		if hash == "" {
			t.Fatalf("Hash(%q) returned empty hash", p)
		}
		if hash == p {
			t.Fatalf("Hash(%q) returned plaintext (no hashing)", p)
		}
		if !strings.HasPrefix(hash, "$2a$") && !strings.HasPrefix(hash, "$2b$") {
			t.Fatalf("Hash(%q) missing bcrypt prefix, got: %s", p, hash[:10])
		}
		if err := Verify(hash, p); err != nil {
			t.Fatalf("Verify(%q) round-trip failed: %v", p, err)
		}
	}
}

func TestVerify_WrongPassword(t *testing.T) {
	hash, err := Hash("correct-password")
	if err != nil {
		t.Fatalf("Hash failed: %v", err)
	}
	tests := []struct {
		name string
		pass string
	}{
		{"empty", ""},
		{"similar", "correct-password "}, // 多一个空格
		{"different", "wrong-password"},
		{"case-sensitive", "Correct-Password"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := Verify(hash, tc.pass); err == nil {
				t.Fatalf("expected error for password=%q, got nil", tc.pass)
			} else if !errors.Is(err, err) {
				// bcrypt 错误不是 sentinel,只验 non-nil
				t.Logf("got expected error: %v", err)
			}
		})
	}
}

func TestVerify_MalformedHash(t *testing.T) {
	// 损坏的 hash 应该返回错误(而不是 panic)
	badHashes := []string{
		"",
		"not-a-hash",
		"$2a$10$tooshort",          // salt 太短
		"$2x$10$xxxxxxxxxxxxxxxxx", // 错的版本前缀
	}
	for _, h := range badHashes {
		t.Run(h, func(t *testing.T) {
			// 空 hash 会 panic?测一下,确保优雅处理
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Verify(%q) panicked: %v", h, r)
				}
			}()
			_ = Verify(h, "any-password")
		})
	}
}

func TestHash_GeneratesUniqueSalts(t *testing.T) {
	// 同一个密码 hash 两次应该得到不同结果(因为 salt 随机)
	h1, _ := Hash("same-password")
	h2, _ := Hash("same-password")
	if h1 == h2 {
		t.Fatal("two hashes of same password are identical (salt should be random)")
	}
	if Verify(h1, "same-password") != nil || Verify(h2, "same-password") != nil {
		t.Fatal("verify failed for one of the two hashes")
	}
}