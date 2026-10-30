package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"easycheck/qdgj"
)

func taskCLIFixture(t *testing.T, handler http.HandlerFunc) (string, string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "fixture-token" || r.Header.Get("loginUserId") != "fixture-user" || r.Header.Get("loginUserOrgId") != "fixture-org" {
			t.Error("missing session identity or wrong request method")
		}
		if cookie, err := r.Cookie("customsid"); err != nil || cookie.Value != "fixture-cookie" {
			t.Error("saved cookie lost")
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	base := server.URL + "/qdgj/"
	path := filepath.Join(t.TempDir(), "session.json")
	saved := &qdgj.Session{
		BaseURL: base, UserAgent: qdgj.MobileUserAgent, Authorization: "fixture-token",
		Cookies:       []*http.Cookie{{Name: "customsid", Value: "fixture-cookie"}},
		LoginResponse: json.RawMessage(`{"data":{"userId":"fixture-user","orgId":"fixture-org"}}`),
	}
	if err := saved.Save(path); err != nil {
		t.Fatal(err)
	}
	return base, path
}

func assertCLIJSON(t *testing.T, actual []byte, expected string) {
	t.Helper()
	var compact bytes.Buffer
	if err := json.Compact(&compact, actual); err != nil || compact.String() != expected {
		t.Fatalf("unexpected JSON output: %s (%v)", actual, err)
	}
}

func TestListCommandPaginationAndOutput(t *testing.T) {
	const raw = `{"code":200,"data":{"list":[{"rwid":"task","pcid":"batch","qdrw":"fixture task","status":"进行中","zt":"未签到"}],"pageNum":2,"pageSize":5,"total":8,"pageCount":2}}`
	for _, jsonOutput := range []bool{false, true} {
		t.Run(fmt.Sprint(jsonOutput), func(t *testing.T) {
			var calls atomic.Int32
			base, session := taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/qdgj/api/sm-registration/qdgjQdrw/queryQdrw" {
					t.Errorf("unexpected list endpoint: %s", r.URL.Path)
				}
				var body struct {
					UserID     string                       `json:"userid"`
					TaskID     string                       `json:"rwid"`
					Pagination struct{ Page, PageSize int } `json:"pagination"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.UserID != "fixture-user" || body.TaskID != "" || body.Pagination.Page != 2 || body.Pagination.PageSize != 5 {
					t.Errorf("wrong list query: %+v (%v)", body, err)
				}
				fmt.Fprint(w, raw)
			})
			before, err := os.ReadFile(session)
			if err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(t.TempDir(), "response.json")
			args := []string{"list", "-base-url", base, "-session", session, "-page", "2", "-page-size", "5", "-out", out}
			if jsonOutput {
				args = append(args, "-json")
			}
			var output bytes.Buffer
			if err := run(args, strings.NewReader(""), &output, io.Discard); err != nil {
				t.Fatal(err)
			}
			if jsonOutput {
				assertCLIJSON(t, output.Bytes(), raw)
			} else {
				for _, text := range []string{"第 2 页", "共 8 条", "fixture task", "未签到", "rwid=task", "pcid=batch"} {
					if !strings.Contains(output.String(), text) {
						t.Errorf("missing list field %q: %s", text, &output)
					}
				}
			}
			saved, err := os.ReadFile(out)
			if err != nil || string(saved) != raw {
				t.Fatalf("raw response changed: %v", err)
			}
			info, err := os.Stat(out)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("raw output permissions are not 0600")
			}
			after, err := os.ReadFile(session)
			if err != nil || !bytes.Equal(before, after) || calls.Load() != 1 {
				t.Fatalf("unexpected session write or request replay: %v", err)
			}
		})
	}
}

func TestDetailCommandDisplaysRulesAndLocations(t *testing.T) {
	const raw = `{"code":200,"data":{"list":[{"rwid":"task","pcid":"batch","qdrw":"fixture detail","qdfs":"定位签到","sfsctp":"1","pzyq":"fixture photo rule","qddd":"fixture past address","jdzb":null,"wdzb":20.5,"qddds":[{"qddd":"fixture allowed address","jdzb":"10.5","wdzb":20.5,"yxbj":"500"}]}]}}`
	var calls atomic.Int32
	base, session := taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/qdgj/api/sm-registration/qdgjQdrw/queryQdrw" {
			t.Errorf("unexpected detail endpoint: %s", r.URL.Path)
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || string(body["rwid"]) != `"task"` || string(body["pcid"]) != `"batch"` || body["pagination"] != nil {
			t.Errorf("wrong detail query: %v (%v)", body, err)
		}
		fmt.Fprint(w, raw)
	})
	var output bytes.Buffer
	if err := run([]string{"detail", "-base-url", base, "-session", session, "-rwid", "task", "-pcid", "batch"}, strings.NewReader(""), &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"fixture detail", "定位签到", "fixture photo rule", "fixture past address", "经度 未知", "fixture allowed address", "经度 10.5", "半径 500 米"} {
		if !strings.Contains(output.String(), text) {
			t.Errorf("missing detail field %q: %s", text, &output)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("detail made extra requests")
	}
}

func TestSignCommandPayloadAndSingleSubmission(t *testing.T) {
	const raw = `{"code":200,"meta":{"message":"fixture accepted"},"data":{"accepted":true}}`
	for _, outside := range []bool{false, true} {
		t.Run(fmt.Sprint(outside), func(t *testing.T) {
			var calls atomic.Int32
			base, session := taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/qdgj/api/sm-registration/qdgjQdrw/startSign" {
					t.Errorf("unexpected sign endpoint: %s", r.URL.Path)
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				for key, expected := range map[string]string{"rwid": `"task"`, "pcid": `"batch"`, "userid": `"fixture-user"`, "qddd": `"fixture address"`, "jdzb": "0", "wdzb": "0"} {
					if string(body[key]) != expected {
						t.Errorf("wrong %s: got %s want %s", key, body[key], expected)
					}
				}
				if outside {
					if string(body["sfxdfwwqd"]) != `"1"` || string(body["bz"]) != `"fixture remark"` || string(body["qdtp"]) != `"fixture-photo"` {
						t.Error("outside-range/photo flags lost")
					}
				} else if body["sfxdfwwqd"] != nil || body["bz"] != nil || string(body["qdtp"]) != "null" {
					t.Error("wrong normal sign fields")
				}
				fmt.Fprint(w, raw)
			})
			out := filepath.Join(t.TempDir(), "sign-response.json")
			args := []string{"sign", "-base-url", base, "-session", session, "-rwid", "task", "-pcid", "batch", "-address", "fixture address", "-longitude", "0", "-latitude", "0", "-out", out}
			if outside {
				args = append(args, "-outside-range", "-remark", "fixture remark", "-photo-id", "fixture-photo", "-json")
			}
			var output bytes.Buffer
			if err := run(args, strings.NewReader(""), &output, io.Discard); err != nil {
				t.Fatal(err)
			}
			if outside {
				assertCLIJSON(t, output.Bytes(), raw)
			} else if !strings.Contains(output.String(), "签到成功") || !strings.Contains(output.String(), "fixture accepted") {
				t.Fatalf("missing sign result: %s", &output)
			}
			if calls.Load() != 1 {
				t.Fatalf("sign made %d requests", calls.Load())
			}
			saved, err := os.ReadFile(out)
			if err != nil || string(saved) != raw {
				t.Fatalf("sign raw response lost: %v", err)
			}
		})
	}
}

func TestSignCommandFailureAndResponseSaveFailure(t *testing.T) {
	for _, success := range []bool{false, true} {
		t.Run(fmt.Sprint(success), func(t *testing.T) {
			var calls atomic.Int32
			base, session := taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if success {
					fmt.Fprint(w, `{"code":200,"data":{}}`)
				} else {
					fmt.Fprint(w, `{"code":409,"meta":{"message":"fixture already signed"}}`)
				}
			})
			args := []string{"sign", "-base-url", base, "-session", session, "-rwid", "task", "-pcid", "batch", "-address", "fixture", "-longitude", "10.5", "-latitude", "20.5", "-out", t.TempDir()}
			var output bytes.Buffer
			err := run(args, strings.NewReader(""), &output, io.Discard)
			if err == nil || calls.Load() != 1 {
				t.Fatalf("failure retried or ignored: calls=%d err=%v", calls.Load(), err)
			}
			if success {
				if !strings.Contains(err.Error(), "操作成功，但保存响应失败") || !strings.Contains(output.String(), "签到成功") {
					t.Fatalf("accepted result lost after save failure: %v", err)
				}
			} else if !strings.Contains(err.Error(), "fixture already signed") || output.Len() != 0 {
				t.Fatalf("business failure printed as success: %v", err)
			}
		})
	}
}

type closedOutput struct{}

func (closedOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestSignCommandOutputFailurePreservesAcceptedState(t *testing.T) {
	var calls atomic.Int32
	base, session := taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"code":200,"data":{}}`)
	})
	args := []string{"sign", "-base-url", base, "-session", session, "-rwid", "task", "-pcid", "batch", "-address", "fixture", "-longitude", "10.5", "-latitude", "20.5"}
	err := run(args, strings.NewReader(""), closedOutput{}, io.Discard)
	if !errors.Is(err, io.ErrClosedPipe) || !strings.Contains(err.Error(), "操作成功，但输出响应失败") || calls.Load() != 1 {
		t.Fatalf("accepted state lost on output failure: calls=%d err=%v", calls.Load(), err)
	}
}

func TestTaskCommandValidationBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	base, session := taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	for _, args := range [][]string{
		{"list", "-page", "0"}, {"list", "-page-size", "-1"}, {"list", "-timeout", "0"},
		{"detail"}, {"sign"}, {"sign", "-rwid", "task"},
		{"sign", "-rwid", "task", "-pcid", "batch", "-address", "fixture", "-latitude", "20"},
		{"sign", "-rwid", "task", "-pcid", "batch", "-address", "fixture", "-longitude", "10"},
		{"sign", "-rwid", "task", "-pcid", "batch", "-address", "fixture", "-longitude", "NaN", "-latitude", "20"},
		{"sign", "-rwid", "task", "-pcid", "batch", "-address", "fixture", "-longitude", "10", "-latitude", "91"},
		{"sign", "-rwid", "task", "-pcid", "batch", "-address", "fixture", "-longitude", "10", "-latitude", "20", "-outside-range"},
		{"sign", "-rwid", "task", "-pcid", "batch", "-address", "fixture", "-longitude", "10", "-latitude", "20", "-remark", "fixture"},
		{"list", "extra"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			full := append([]string{args[0], "-base-url", base, "-session", session}, args[1:]...)
			if err := run(full, strings.NewReader(""), io.Discard, io.Discard); err == nil {
				t.Fatal("invalid command accepted")
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid command sent %d requests", calls.Load())
	}
}

func TestCLIHelpAndExplicitLoginCommands(t *testing.T) {
	for _, args := range [][]string{{"-help"}, {"help"}, {"help", "sign"}, {"login", "-help"}, {"check", "-help"}, {"list", "-help"}, {"detail", "-help"}, {"sign", "-help"}} {
		var output bytes.Buffer
		if err := run(args, strings.NewReader(""), &output, &output); err != nil || output.Len() == 0 {
			t.Fatalf("help %v: %v", args, err)
		}
	}
	for _, args := range [][]string{{"unknown"}, {"help", "help"}, {"help", "sign", "extra"}} {
		if err := run(args, strings.NewReader(""), io.Discard, io.Discard); err == nil {
			t.Fatalf("unknown command accepted: %v", args)
		}
	}
	t.Setenv("EASYCHECK_USERNAME", "test001")
	t.Setenv("EASYCHECK_PASSWORD", "Probe-Only-2026")
	var posts atomic.Int32
	server := loginFixture(t, false, 200, 200, &posts)
	defer server.Close()
	for _, command := range []string{"check", "login"} {
		args := []string{command, "-base-url", server.URL + "/qdgj/", "-session", filepath.Join(t.TempDir(), "session.json")}
		if err := run(args, strings.NewReader(""), io.Discard, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	if posts.Load() != 1 {
		t.Fatalf("check/login submitted %d logins", posts.Load())
	}
}
