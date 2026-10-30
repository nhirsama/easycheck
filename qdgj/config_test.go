package qdgj

import (
	"context"
	"strings"
	"testing"
)

func TestBaseURLAndPublicKeyFromEnvironment(t *testing.T) {
	t.Setenv("EASYCHECK_BASE_URL", "")
	t.Setenv("EASYCHECK_PUBLIC_KEY", "")
	if _, err := NewClient(Options{}); err == nil || !strings.Contains(err.Error(), "EASYCHECK_BASE_URL") {
		t.Fatalf("missing URL silently accepted: %v", err)
	}
	t.Setenv("EASYCHECK_BASE_URL", " https://example.invalid/mobile-app ")
	t.Setenv("EASYCHECK_PUBLIC_KEY", fixturePublicKey)
	c, err := NewClient(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if c.base.String() != "https://example.invalid/mobile-app/" || c.loginPublicKey != fixturePublicKey {
		t.Fatal("environment configuration not loaded")
	}
	c, err = NewClient(Options{BaseURL: "https://override.invalid/app/", LoginPublicKey: "override-key"})
	if err != nil || c.base.Host != "override.invalid" || c.loginPublicKey != "override-key" {
		t.Fatalf("explicit options did not override environment: %v", err)
	}
}

func TestSessionMustMatchConfiguredEnvironment(t *testing.T) {
	saved := fixtureSession("https://example.invalid")
	t.Setenv("EASYCHECK_BASE_URL", "https://example.invalid/qdgj/")
	if _, err := NewClientFromSession(saved, Options{}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EASYCHECK_BASE_URL", "https://other.invalid/qdgj/")
	if _, err := NewClientFromSession(saved, Options{}); err == nil {
		t.Fatal("different environment target accepted saved credentials")
	}
	t.Setenv("EASYCHECK_BASE_URL", "")
	if _, err := NewClientFromSession(saved, Options{}); err == nil {
		t.Fatal("missing environment silently fell back to saved target")
	}
}

func TestMissingOrInvalidPublicKeyFailsBeforeNetwork(t *testing.T) {
	t.Setenv("EASYCHECK_PUBLIC_KEY", "")
	for _, key := range []string{"", "invalid-key"} {
		c, err := NewClient(Options{BaseURL: "https://example.invalid/", LoginPublicKey: key})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Login(context.Background(), LoginOptions{Username: "example-user", Password: "example-password"})
		if err == nil {
			t.Fatal("invalid public key accepted")
		}
		if strings.Contains(err.Error(), "Post ") || strings.Contains(err.Error(), "Get ") {
			t.Fatalf("invalid key reached network: %v", err)
		}
	}
}
