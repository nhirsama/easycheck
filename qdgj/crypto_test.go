package qdgj

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"
)

// 用于测试 RSA 登录表单的合成公钥。
const fixturePublicKey = "MFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBAM0lIa8taIYX0beck8CCKoublJ0oiSsPPtcPdtjuhRK4lVduwmqL0hlRiyBgAiF5K9VRmUagZGgBCoCZ6BdvVB0CAwEAAQ=="

func TestFrontendDigestVectors(t *testing.T) {
	vectors := []struct{ Input, Expected string }{
		{"abc", "a9993e364706816aba3e25717850c26c9cd0d89d"},
		{"密码", "1b6dae7d682d6efe9918a582badb7435e44de50f"},
		{"", "da39a3ee5e6b4b0d3255bfef95601890afd80709"},
	}
	for _, vector := range vectors {
		if actual := frontendDigest(vector.Input); actual != vector.Expected {
			t.Errorf("digest for %q: got %s, want %s", vector.Input, actual, vector.Expected)
		}
	}
}

func TestSiteRSAForm(t *testing.T) {
	t.Setenv("GODEBUG", "rsa1024min=1")
	now := time.UnixMilli(1700000000000)
	first, err := makeLoginForm(fixturePublicKey, "test001", "Probe-Only-2026", "AB12", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := makeLoginForm(fixturePublicKey, "test001", "Probe-Only-2026", "", now)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(first.Get("password"))
	if err != nil || len(ciphertext) != 64 {
		t.Fatalf("expected 64-byte RSA ciphertext, got %d bytes: %v", len(ciphertext), err)
	}
	if first.Get("password") == second.Get("password") {
		t.Fatal("PKCS#1 v1.5 encryption should be randomized")
	}
	if first.Get("timestamp") != "1700000000000" || first.Get("encryptTransfer") != "true" || first.Get("systemId") != "ly-zhxg" || first.Get("captcha") != "AB12" {
		t.Fatalf("unexpected form fields: %v", first)
	}
	if second.Has("captcha") {
		t.Fatal("captcha should be omitted when not enabled")
	}
	if _, err := makeLoginForm(fixturePublicKey, "test", strings.Repeat("密", 20), "", now); err == nil {
		t.Fatal("passwords larger than the site's RSA block must return an error")
	}
}

func TestLegacyEncryptionDecryptsWithStandardRSA(t *testing.T) {
	t.Setenv("GODEBUG", "rsa1024min=1")
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range [][]byte{nil, frontendPasswordBytes("密😀"), bytes.Repeat([]byte("x"), key.Size()-11)} {
		ciphertext, err := encryptLegacyPKCS1v15(&key.PublicKey, message)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := rsa.DecryptPKCS1v15(rand.Reader, key, ciphertext)
		if err != nil || !bytes.Equal(plain, message) {
			t.Fatalf("PKCS#1 compatibility: err=%v", err)
		}
	}
	if _, err := encryptLegacyPKCS1v15(&key.PublicKey, make([]byte, key.Size()-10)); err != rsa.ErrMessageTooLong {
		t.Fatalf("oversized message: %v", err)
	}
	if os.Getenv("GODEBUG") != "rsa1024min=1" {
		t.Fatal("encryption changed process environment")
	}
}

func TestFrontendPasswordEncoding(t *testing.T) {
	if got := hex.EncodeToString(frontendPasswordBytes("a密😀")); got != "61e5af86eda0bdedb880" {
		t.Fatalf("unexpected JSEncrypt encoding: %s", got)
	}
	if err := validateCredentials(strings.Repeat("😀", 11), "test"); err == nil {
		t.Fatal("page maxlength counts UTF-16 code units")
	}
}
