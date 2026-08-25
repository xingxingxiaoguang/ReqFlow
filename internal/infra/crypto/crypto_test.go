package crypto

import (
	"strings"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	// 32 字节密钥的 hex
	keyHex := strings.Repeat("ab", 32)
	enc, err := New(keyHex)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, plain := range []string{"", "hello", "access_token-中文凭据🔐", strings.Repeat("x", 4096)} {
		ct, err := enc.Encrypt(plain)
		if err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
		got, err := enc.Decrypt(ct)
		if err != nil {
			t.Fatalf("Decrypt: %v", err)
		}
		if got != plain {
			t.Errorf("round trip mismatch: got %q want %q", got, plain)
		}
	}
}

func TestNewRejectsBadKeys(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Error("空密钥应报错")
	}
	if _, err := New("zzzz"); err == nil {
		t.Error("非法 hex 应报错")
	}
	if _, err := New(strings.Repeat("ab", 16)); err == nil {
		t.Error("16 字节密钥应报错（须 32 字节）")
	}
}
