package cli

import (
	"bytes"
	"context"
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
	"time"

	"easycheck/qdgj"
)

func configureCronLoginEnv(t *testing.T) {
	t.Helper()
	clearNotificationEnv(t)
	t.Setenv("GODEBUG", "rsa1024min=1")
	t.Setenv("EASYCHECK_USERNAME", "fixture-user")
	t.Setenv("EASYCHECK_PASSWORD", "fixture-password")
	t.Setenv("EASYCHECK_LOGIN_RETRIES", "0")
	t.Setenv("EASYCHECK_LOGIN_RETRY_INTERVAL", "1ms")
}

func TestCronLogsInAtEachTriggerAndReplacesSession(t *testing.T) {
	for _, initial := range []string{"missing", "expired", "invalid"} {
		t.Run(initial, func(t *testing.T) {
			configureCronLoginEnv(t)
			path := filepath.Join(t.TempDir(), "session.json")
			var calls, logins, queries, signs atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch r.URL.Path {
				case "/qdgj/":
					if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
						t.Error("new round reused an old login session")
					}
					http.SetCookie(w, &http.Cookie{Name: "sid", Value: "initial", Path: "/qdgj/"})
					fmt.Fprintf(w, `<script>encryptor.setPublicKey("%s")</script>`, fixtureLoginPublicKey)
				case "/qdgj/ng/captcha/status":
					fmt.Fprint(w, `{"meta":{"statusCode":200},"data":false}`)
				case "/qdgj/login":
					if err := r.ParseForm(); err != nil || r.PostForm.Get("username") != "fixture-user" {
						t.Errorf("invalid login form: %v", err)
					}
					round := logins.Add(1)
					http.SetCookie(w, &http.Cookie{Name: "sid", Value: fmt.Sprintf("cookie-%d", round), Path: "/qdgj/"})
					fmt.Fprintf(w, `{"meta":{"statusCode":200},"data":{"userId":"fixture-user","tokenId":"token-%d"}}`, round)
				default:
					round := logins.Load()
					if r.Header.Get("Authorization") != fmt.Sprintf("token-%d", round) || r.Header.Get("loginUserId") != "fixture-user" {
						t.Error("query or sign used a stale token")
					}
					if cookie, err := r.Cookie("sid"); err != nil || cookie.Value != fmt.Sprintf("cookie-%d", round) {
						t.Error("query or sign used a stale cookie")
					}
					saved, err := qdgj.LoadSession(path)
					if err != nil || saved.Authorization != fmt.Sprintf("token-%d", round) || len(saved.Cookies) != 1 || saved.Cookies[0].Value != fmt.Sprintf("cookie-%d", round) {
						t.Errorf("new session was not saved before task requests: %v", err)
					}
					switch r.URL.Path {
					case "/qdgj/api/sm-registration/qdgjQdrw/queryQdrw":
						queries.Add(1)
						writeTaskPage(t, w, []qdgj.Task{activeTaskFixture("batch")}, 1, 1)
					case "/qdgj/api/sm-registration/qdgjQdrw/startSign":
						signs.Add(1)
						fmt.Fprint(w, `{"code":200,"data":{}}`)
					default:
						t.Errorf("unexpected request: %s", r.URL.Path)
						http.NotFound(w, r)
					}
				}
			}))
			defer server.Close()
			base := server.URL + "/qdgj/"
			switch initial {
			case "expired":
				saved := &qdgj.Session{
					BaseURL: base, UserAgent: qdgj.MobileUserAgent, Authorization: "expired-token",
					Cookies:       []*http.Cookie{{Name: "sid", Value: "expired-cookie"}},
					LoginResponse: json.RawMessage(`{"data":{"userId":"fixture-user"}}`),
				}
				if err := saved.Save(path); err != nil {
					t.Fatal(err)
				}
			case "invalid":
				if err := os.WriteFile(path, []byte("invalid session"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var output bytes.Buffer
			var reports int
			outputWriter := callbackWriter(func(p []byte) {
				output.Write(p)
				reports++
				if reports == 2 {
					cancel()
				}
			})
			diagnostics := callbackWriter(func(p []byte) {
				if bytes.Contains(p, []byte("cron 签到已启动")) && calls.Load() != 0 {
					t.Error("cron logged in before the first trigger")
				}
			})
			args := append(automaticSignArgs(base, path), "--cron", "@every 1s")
			err := Run(ctx, args, strings.NewReader(""), outputWriter, diagnostics)
			if !errors.Is(err, context.Canceled) || logins.Load() != 2 || queries.Load() != 4 || signs.Load() != 2 {
				t.Fatalf("per-trigger login failed: logins=%d queries=%d signs=%d err=%v", logins.Load(), queries.Load(), signs.Load(), err)
			}
			decoder := json.NewDecoder(&output)
			for i := 0; i < 2; i++ {
				var report automaticSignReport
				if err := decoder.Decode(&report); err != nil || report.Succeeded != 1 || report.Failed != 0 {
					t.Fatalf("login output corrupted the sign report: %+v (%v)", report, err)
				}
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("wrong session permissions: %v (%v)", info, err)
			}
		})
	}
}

func TestCronLoginRetriesThenRecoversOnNextTrigger(t *testing.T) {
	configureCronLoginEnv(t)
	path := filepath.Join(t.TempDir(), "session.json")
	var logins, queries atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/qdgj/":
			fmt.Fprintf(w, `<script>encryptor.setPublicKey("%s")</script>`, fixtureLoginPublicKey)
		case "/qdgj/ng/captcha/status":
			fmt.Fprint(w, `{"meta":{"statusCode":200},"data":false}`)
		case "/qdgj/login":
			if logins.Add(1) <= 2 {
				fmt.Fprint(w, `{"meta":{"statusCode":401},"data":{}}`)
				return
			}
			fmt.Fprint(w, `{"meta":{"statusCode":200},"data":{"userId":"fixture-user","tokenId":"new-token"}}`)
		case "/qdgj/api/sm-registration/qdgjQdrw/queryQdrw":
			queries.Add(1)
			if logins.Load() != 3 || r.Header.Get("Authorization") != "new-token" {
				t.Error("cron used an old session after login failed")
			}
			writeTaskPage(t, w, []qdgj.Task{}, 1, 1)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	saved := &qdgj.Session{
		BaseURL: server.URL + "/qdgj/", UserAgent: qdgj.MobileUserAgent, Authorization: "old-token",
		LoginResponse: json.RawMessage(`{"data":{"userId":"fixture-user"}}`),
	}
	if err := saved.Save(path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var diagnostics bytes.Buffer
	diagnosticWriter := callbackWriter(func(p []byte) {
		diagnostics.Write(p)
		if bytes.Contains(p, []byte("本轮签到失败")) {
			if saved, err := qdgj.LoadSession(path); err != nil || saved.Authorization != "old-token" {
				t.Errorf("failed login replaced the saved session: %v", err)
			}
		}
	})
	args := append(automaticSignArgs(saved.BaseURL, path), "--cron", "@every 1s", "--retries", "1", "--retry-interval", "10ms")
	err := Run(ctx, args, strings.NewReader(""), callbackWriter(func([]byte) { cancel() }), diagnosticWriter)
	if !errors.Is(err, context.Canceled) || logins.Load() != 3 || queries.Load() != 1 || !strings.Contains(diagnostics.String(), "将在 10ms 后重试") || !strings.Contains(diagnostics.String(), "本轮签到失败") {
		t.Fatalf("cron did not recover after login retries: logins=%d queries=%d diagnostics=%s err=%v", logins.Load(), queries.Load(), &diagnostics, err)
	}
}

func TestCronAutoLoginWaitsWithoutSessionAndValidatesConfig(t *testing.T) {
	configureCronLoginEnv(t)
	path := filepath.Join(t.TempDir(), "missing-session.json")
	args := []string{"sign", "--base-url", "https://example.invalid/qdgj/", "--session", path, "--cron", "0 8 * * *"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := Run(ctx, args, strings.NewReader(""), io.Discard, callbackWriter(func([]byte) { cancel() }))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cron required a session before its first login: %v", err)
	}
	for _, options := range [][]string{{"--retries", "-1"}, {"--retry-interval", "0s"}} {
		var usage *usageError
		err := Run(ctx, append(args, options...), strings.NewReader(""), io.Discard, io.Discard)
		if !errors.As(err, &usage) {
			t.Fatalf("invalid cron login retry options accepted: %v", err)
		}
	}
	for _, missing := range []string{"EASYCHECK_USERNAME", "EASYCHECK_PASSWORD"} {
		t.Run(missing, func(t *testing.T) {
			t.Setenv(missing, "")
			var usage *usageError
			err := Run(ctx, args, strings.NewReader(""), io.Discard, io.Discard)
			if !errors.As(err, &usage) || !strings.Contains(err.Error(), "须同时设置") {
				t.Fatalf("partial login credentials accepted: %v", err)
			}
		})
	}
}

func TestCronAutoLoginPreparationFailureSkipsTasks(t *testing.T) {
	for _, captcha := range []bool{false, true} {
		t.Run(fmt.Sprintf("captcha=%t", captcha), func(t *testing.T) {
			configureCronLoginEnv(t)
			var logins, taskRequests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/qdgj/":
					fmt.Fprintf(w, `<script>encryptor.setPublicKey("%s")</script>`, fixtureLoginPublicKey)
				case "/qdgj/ng/captcha/status":
					fmt.Fprintf(w, `{"meta":{"statusCode":200},"data":%t}`, captcha)
				case "/qdgj/login":
					logins.Add(1)
					fmt.Fprint(w, `{"meta":{"statusCode":200},"data":{"userId":"fixture-user","tokenId":"new-token"}}`)
				default:
					taskRequests.Add(1)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			// 缺失的目录使会话保存失败；开启验证码时应在提交登录前失败。
			path := filepath.Join(t.TempDir(), "missing-directory", "session.json")
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			var diagnostics bytes.Buffer
			diagnosticWriter := callbackWriter(func(p []byte) {
				diagnostics.Write(p)
				if bytes.Contains(p, []byte("本轮签到失败")) {
					cancel()
				}
			})
			args := append(automaticSignArgs(server.URL+"/qdgj/", path), "--cron", "@every 1s")
			err := Run(ctx, args, strings.NewReader(""), io.Discard, diagnosticWriter)
			wantLogins, wantError := int32(1), "登录成功，但保存会话失败"
			if captcha {
				wantLogins, wantError = 0, "登录需要验证码"
			}
			if !errors.Is(err, context.Canceled) || logins.Load() != wantLogins || taskRequests.Load() != 0 || !strings.Contains(diagnostics.String(), wantError) {
				t.Fatalf("failed session preparation did not stop tasks: logins=%d tasks=%d diagnostics=%s err=%v", logins.Load(), taskRequests.Load(), &diagnostics, err)
			}
		})
	}
}
