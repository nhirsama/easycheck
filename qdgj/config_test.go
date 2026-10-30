package qdgj

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientKeepsConfiguredLoginAcrossEnvironmentChanges(t *testing.T) {
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/":
			fmt.Fprintf(w, `<script>encryptor.setPublicKey("%s")</script>`, fixturePublicKey)
		case "/app/ng/captcha/status":
			fmt.Fprint(w, `{"meta":{"statusCode":200},"data":false}`)
		case "/app/login":
			posts++
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.PostForm.Get("username") != "configured-user" {
				t.Error("object reread the environment username")
			}
			w.Write(fixtureSession("").LoginResponse)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "configured-session.json")
	t.Setenv("EASYCHECK_BASE_URL", server.URL+"/app/")
	t.Setenv("EASYCHECK_USERNAME", "configured-user")
	t.Setenv("EASYCHECK_PASSWORD", "configured-password")
	t.Setenv("EASYCHECK_SESSION_FILE", path)
	client, err := NewClient(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("EASYCHECK_BASE_URL", "https://other.invalid/")
	t.Setenv("EASYCHECK_USERNAME", "changed-user")
	t.Setenv("EASYCHECK_PASSWORD", "")
	t.Setenv("EASYCHECK_SESSION_FILE", filepath.Join(t.TempDir(), "unused-session.json"))
	for range 2 {
		if _, err := client.Login(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if posts != 2 {
		t.Fatalf("expected one request per explicit Login call, got %d", posts)
	}
	if err := client.SaveSession(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !json.Valid(raw) {
		t.Fatalf("configured session path was not used: %v", err)
	}
	if strings.Contains(string(raw), "configured-password") {
		t.Fatal("password was written to session")
	}
}

func TestClientConfigFromEnvironment(t *testing.T) {
	t.Setenv("EASYCHECK_BASE_URL", "")
	t.Setenv("EASYCHECK_USERNAME", "")
	t.Setenv("EASYCHECK_PASSWORD", "")
	t.Setenv("EASYCHECK_SESSION_FILE", "")
	if _, err := NewClient(Config{}); err == nil || !strings.Contains(err.Error(), "EASYCHECK_BASE_URL") {
		t.Fatalf("missing URL silently accepted: %v", err)
	}
	t.Setenv("EASYCHECK_BASE_URL", " https://example.invalid/mobile-app ")
	t.Setenv("EASYCHECK_USERNAME", "example-user")
	t.Setenv("EASYCHECK_PASSWORD", "example-password")
	t.Setenv("EASYCHECK_SESSION_FILE", "example-session.json")
	c, err := NewClient(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if c.base.String() != "https://example.invalid/mobile-app/" || c.config.Username != "example-user" || c.config.Password != "example-password" || c.config.SessionFile != "example-session.json" {
		t.Fatal("environment configuration not loaded")
	}
	c, err = NewClient(Config{BaseURL: "https://override.invalid/app/", Username: "explicit-user", Password: "explicit-password", SessionFile: "explicit-session.json"})
	if err != nil || c.base.Host != "override.invalid" || c.config.Username != "explicit-user" || c.config.Password != "explicit-password" || c.config.SessionFile != "explicit-session.json" {
		t.Fatalf("explicit options did not override environment: %v", err)
	}
}

func TestSessionMustMatchConfiguredEnvironment(t *testing.T) {
	saved := fixtureSession("https://example.invalid")
	t.Setenv("EASYCHECK_BASE_URL", "https://example.invalid/qdgj/")
	if _, err := NewClientFromSession(saved, Config{}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EASYCHECK_BASE_URL", "https://other.invalid/qdgj/")
	if _, err := NewClientFromSession(saved, Config{}); err == nil {
		t.Fatal("different environment target accepted saved credentials")
	}
	t.Setenv("EASYCHECK_BASE_URL", "")
	if _, err := NewClientFromSession(saved, Config{}); err == nil {
		t.Fatal("missing environment silently fell back to saved target")
	}
}
