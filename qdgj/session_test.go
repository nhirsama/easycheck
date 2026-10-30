package qdgj

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
)

func TestLoadSessionUsesConfiguredPathAndPreservesStateOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	config := Config{BaseURL: "https://example.invalid/qdgj/", SessionFile: path}
	client, err := NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	saved := fixtureSession("https://example.invalid")
	if err := saved.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := client.LoadSession(); err != nil {
		t.Fatal(err)
	}
	before, err := client.Session()
	if err != nil {
		t.Fatal(err)
	}
	client.http.Jar.SetCookies(client.base, []*http.Cookie{{Name: "stale", Value: "old"}})
	if err := client.LoadSession(); err != nil {
		t.Fatal(err)
	}
	for _, cookie := range client.http.Jar.Cookies(client.base) {
		if cookie.Name == "stale" {
			t.Fatal("loading session retained stale cookies")
		}
	}
	other := fixtureSession("https://other.invalid")
	if err := other.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := client.LoadSession(); err == nil {
		t.Fatal("cross-site session was accepted")
	}
	after, err := client.Session()
	if err != nil || before.Authorization != after.Authorization || string(before.LoginResponse) != string(after.LoginResponse) {
		t.Fatalf("failed load changed authenticated state: %v", err)
	}
	// 使用取消的 context 验证原会话的查询取消处理。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.ListTasks(ctx, ListOptions{}); err == nil {
		t.Fatal("canceled query succeeded")
	}
}
