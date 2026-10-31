package cli

import (
	"bytes"
	"context"
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

func runInspect(args []string, output, diagnostics io.Writer) error {
	a := &app{input: strings.NewReader(""), output: output, diagnostics: diagnostics}
	return a.runCommand(context.Background(), inspectSpec, args)
}

func TestHelpHidesSessionPath(t *testing.T) {
	const session = "private-fixture-session.json"
	t.Setenv("EASYCHECK_SESSION_FILE", session)
	for _, argument := range []string{"-help", "-unknown-option"} {
		t.Run(argument, func(t *testing.T) {
			var output, diagnostics bytes.Buffer
			err := runInspect([]string{argument}, &output, &diagnostics)
			if (err != nil) != (argument == "-unknown-option") {
				t.Fatalf("unexpected argument result: %v", err)
			}
			text := output.String() + diagnostics.String()
			if err != nil {
				text += err.Error()
			}
			if strings.Contains(text, session) || argument == "-help" && !strings.Contains(text, "--session") {
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
		if err := runInspect(args, &output, io.Discard); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), "fixture") || !strings.Contains(output.String(), "未签到") {
			t.Fatal("missing task fields")
		}
		canonical := []string{"list", "--session", path}
		if detail {
			canonical = []string{"detail", "--session", path, "--rwid", "task", "--pcid", "batch"}
		}
		var expected bytes.Buffer
		if err := run(canonical, strings.NewReader(""), &expected, io.Discard); err != nil {
			t.Fatal(err)
		}
		if output.String() != expected.String() {
			t.Fatal("independent query output differs from the main CLI")
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
	if err != nil || !bytes.Equal(before, after) || calls != 4 {
		t.Fatalf("session changed or requests retried: calls=%d err=%v", calls, err)
	}
}
