package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"easycheck/qdgj"
)

func TestHelpHidesSessionPath(t *testing.T) {
	const session = "private-fixture-session.json"
	t.Setenv("EASYCHECK_SESSION_FILE", session)
	for _, argument := range []string{"-help", "-unknown-option"} {
		t.Run(argument, func(t *testing.T) {
			var output, diagnostics bytes.Buffer
			err := run([]string{argument}, &output, &diagnostics)
			if (err != nil) != (argument == "-unknown-option") {
				t.Fatalf("unexpected argument result: %v", err)
			}
			text := output.String() + diagnostics.String()
			if err != nil {
				text += err.Error()
			}
			if strings.Contains(text, session) || !strings.Contains(text, "-session") {
				t.Fatal("help must show the session option and hide its configured path")
			}
		})
	}
}

func TestInspectOnlyQueriesAndKeepsSavedSession(t *testing.T) {
	const raw = `{"code":200,"data":{"list":[{"rwid":"task","pcid":"batch","qdrw":"fixture","status":"进行中","zt":"未签到"}]}}`
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/qdgj/api/sm-registration/qdgjQdrw/queryQdrw" {
			t.Errorf("inspect called unexpected endpoint: %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, raw)
	}))
	defer server.Close()
	t.Setenv("EASYCHECK_BASE_URL", server.URL+"/qdgj/")
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	saved := &qdgj.Session{BaseURL: server.URL + "/qdgj/", UserAgent: qdgj.MobileUserAgent, Authorization: "fixture-token", LoginResponse: json.RawMessage(`{"data":{"userId":"fixture-user"}}`)}
	if err := saved.Save(path); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, detail := range []bool{false, true} {
		out := filepath.Join(dir, fmt.Sprintf("response-%t.json", detail))
		args := []string{"-session", path, "-out", out}
		if detail {
			args = append(args, "-rwid", "task", "-pcid", "batch")
		}
		var output bytes.Buffer
		if err := run(args, &output, io.Discard); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), "未提交签到") {
			t.Fatal("missing read-only result")
		}
		content, err := os.ReadFile(out)
		if err != nil || string(content) != raw {
			t.Fatal("raw response changed")
		}
		info, err := os.Stat(out)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("response is not private")
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) || calls != 2 {
		t.Fatalf("session changed or requests retried: calls=%d err=%v", calls, err)
	}
}
