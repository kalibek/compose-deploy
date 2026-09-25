package crypto

import (
	"encoding/base64"
	"testing"
)

func TestRoundtrip(t *testing.T) {
	b, err := New("secret")
	if err != nil {
		t.Fatal(err)
	}
	c1, err := b.Encrypt([]byte("hello world"))
	if err != nil {
		t.Fatal(err)
	}
	c2, _ := b.Encrypt([]byte("hello world"))
	if c1 == c2 {
		t.Fatal("expected random nonce to produce different ciphertexts")
	}
	pt, err := b.Decrypt(c1)
	if err != nil || string(pt) != "hello world" {
		t.Fatalf("roundtrip failed: %q %v", pt, err)
	}
	s, err := b.EncryptString("")
	if err != nil || s != "" {
		t.Fatalf("empty string should encrypt to empty, got %q", s)
	}
	d, err := b.DecryptString("")
	if err != nil || d != "" {
		t.Fatal("empty decrypt")
	}
}

func TestTamper(t *testing.T) {
	b, _ := New("secret")
	c, _ := b.Encrypt([]byte("payload"))
	raw, _ := base64.StdEncoding.DecodeString(c)
	for i := range raw {
		mod := append([]byte(nil), raw...)
		mod[i] ^= 0x01
		if _, err := b.Decrypt(base64.StdEncoding.EncodeToString(mod)); err == nil {
			t.Fatalf("tampered byte %d was accepted", i)
		}
	}
	if _, err := b.Decrypt("!!!notbase64"); err == nil {
		t.Fatal("expected error for bad base64")
	}
	if _, err := b.Decrypt(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("expected error for short input")
	}
	other, _ := New("other-secret")
	if _, err := other.Decrypt(c); err == nil {
		t.Fatal("wrong key accepted")
	}
	if _, err := New(""); err == nil {
		t.Fatal("empty secret accepted")
	}
}
