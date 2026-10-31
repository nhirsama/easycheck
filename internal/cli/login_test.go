package cli

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
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

const fixtureLoginPublicKey = "MFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBAM0lIa8taIYX0beck8CCKoublJ0oiSsPPtcPdtjuhRK4lVduwmqL0hlRiyBgAiF5K9VRmUagZGgBCoCZ6BdvVB0CAwEAAQ=="

func TestHelpHidesEnvironmentConfig(t *testing.T) {
	values := map[string]string{
		"EASYCHECK_BASE_URL":     "https://private-site.example.invalid/qdgj/",
		"EASYCHECK_USERNAME":     "private-fixture-user",
		"EASYCHECK_PASSWORD":     "private-fixture-password",
		"EASYCHECK_SESSION_FILE": "private-fixture-session.json",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
	for _, command := range []string{"login", "check", "list", "detail", "sign"} {
		for _, argument := range []string{"-help", "-unknown-option"} {
			t.Run(command+"/"+argument, func(t *testing.T) {
				var output, diagnostics bytes.Buffer
				err := run([]string{command, argument}, strings.NewReader(""), &output, &diagnostics)
				if (err != nil) != (argument == "-unknown-option") {
					t.Fatalf("unexpected argument result: %v", err)
				}
				text := output.String() + diagnostics.String()
				if err != nil {
					text += err.Error()
				}
				for key, value := range values {
					if strings.Contains(text, value) {
						t.Errorf("help disclosed %s", key)
					}
				}
				if argument == "-help" {
					for _, option := range []string{"--base-url", "--session", "--timeout"} {
						if !strings.Contains(text, option) {
							t.Errorf("help is missing %s", option)
						}
					}
				}
			})
		}
	}
}

func TestLoginFlow(t *testing.T) {
	t.Setenv("GODEBUG", "rsa1024min=1")
	for _, captcha := range []bool{false, true} {
		t.Run(fmt.Sprintf("captcha=%t", captcha), func(t *testing.T) {
			t.Setenv("EASYCHECK_USERNAME", "test001")
			t.Setenv("EASYCHECK_PASSWORD", "not-used-with-stdin")
			var posts atomic.Int32
			server := loginFixture(t, captcha, 200, 200, &posts)
			defer server.Close()
			dir := t.TempDir()
			sessionPath, captchaPath := filepath.Join(dir, "session.json"), filepath.Join(dir, "captcha.png")
			// 覆盖已有文件后，会话文件权限为 0600。
			if err := os.WriteFile(sessionPath, []byte("old"), 0644); err != nil {
				t.Fatal(err)
			}
			var output, diagnostics bytes.Buffer
			err := run([]string{"-base-url", server.URL + "/qdgj", "-session", sessionPath, "-captcha-file", captchaPath, "-password-stdin"}, strings.NewReader("Probe-Only-2026\nAB12\n"), &output, &diagnostics)
			if err != nil {
				t.Fatal(err)
			}
			if posts.Load() != 1 || !strings.Contains(output.String(), "登录成功") {
				t.Fatalf("unexpected result: posts=%d output=%s", posts.Load(), &output)
			}
			raw, err := os.ReadFile(sessionPath)
			if err != nil {
				t.Fatal(err)
			}
			var saved qdgj.Session
			if err := json.Unmarshal(raw, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.Authorization != "fixture-token" || len(saved.Cookies) != 1 || saved.Cookies[0].Value != "authenticated" {
				t.Fatalf("incomplete saved session: %+v", saved)
			}
			info, err := os.Stat(sessionPath)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("session permissions: %v, %v", info, err)
			}
			if strings.Contains(string(raw), "Probe-Only-2026") || strings.Contains(output.String(), "fixture-token") {
				t.Fatal("plaintext password or token leaked into output")
			}
			_, err = os.Stat(captchaPath)
			if captcha && err != nil || !captcha && !os.IsNotExist(err) {
				t.Fatalf("captcha file state: %v", err)
			}
		})
	}
}

func TestLoginFailures(t *testing.T) {
	t.Setenv("GODEBUG", "rsa1024min=1")
	t.Setenv("EASYCHECK_USERNAME", "test001")
	t.Setenv("EASYCHECK_PASSWORD", "Probe-Only-2026")
	for _, status := range [][2]int{{200, 401}, {401, 401}} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var posts atomic.Int32
			server := loginFixture(t, false, status[0], status[1], &posts)
			defer server.Close()
			path := filepath.Join(t.TempDir(), "session.json")
			err := run([]string{"-base-url", server.URL + "/qdgj/", "-session", path}, strings.NewReader(""), io.Discard, io.Discard)
			if err == nil || posts.Load() != 1 {
				t.Fatalf("expected failure and one request: err=%v posts=%d", err, posts.Load())
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("failed login should not write a session")
			}
		})
	}
}

func TestCheckMode(t *testing.T) {
	t.Setenv("EASYCHECK_USERNAME", "")
	t.Setenv("EASYCHECK_PASSWORD", "")
	var posts atomic.Int32
	server := loginFixture(t, false, 200, 200, &posts)
	defer server.Close()
	var output bytes.Buffer
	if err := run([]string{"-base-url", server.URL + "/qdgj/", "-check"}, strings.NewReader(""), &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 0 || !strings.Contains(output.String(), "需要验证码：false") {
		t.Fatalf("check mode submitted login or returned wrong status: %s", &output)
	}
}

func loginFixture(t *testing.T, captcha bool, httpStatus, apiStatus int, posts *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.UserAgent(), "Android") || !strings.Contains(r.UserAgent(), "Mobile") {
			t.Error("request is missing the mobile user agent")
		}
		if r.Header.Get("Sec-CH-UA-Mobile") != "?1" || r.Header.Get("Sec-CH-UA-Platform") != `"Android"` {
			t.Error("request is missing Android mobile client hints")
		}
		if r.URL.Path != "/qdgj/" {
			if cookie, err := r.Cookie("sid"); err != nil || cookie.Value != "initial" {
				t.Errorf("session cookie not maintained: %v %v", cookie, err)
			}
			if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
				t.Error("missing X-Requested-With")
			}
		}
		switch r.URL.Path {
		case "/qdgj/":
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "initial", Path: "/qdgj/", HttpOnly: true})
			fmt.Fprintf(w, `<!doctype html><script>encryptor.setPublicKey("%s")</script>`, fixtureLoginPublicKey)
		case "/qdgj/ng/captcha/status":
			if r.Header.Get("appId") != "ly-zhxg" {
				t.Error("missing appId")
			}
			fmt.Fprintf(w, `{"meta":{"success":true,"statusCode":200},"data":%t}`, captcha)
		case "/qdgj/captchaServlet":
			http.SetCookie(w, &http.Cookie{Name: "challenge", Value: "challenge-1", Path: "/qdgj/"})
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte("\x89PNG\r\n\x1a\nfixture"))
		case "/qdgj/login":
			posts.Add(1)
			if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
				t.Error("wrong login method or content type")
			}
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			f := r.PostForm
			if f.Get("username") != "test001" || f.Get("encryptTransfer") != "true" || f.Get("systemId") != "ly-zhxg" {
				t.Errorf("wrong login fields: %v", f)
			}
			input := "username=" + f.Get("username") + ",password=" + f.Get("password") + ",timestamp=" + f.Get("timestamp")
			sum := sha1.Sum([]byte(input))
			if f.Get("digest") != hex.EncodeToString(sum[:]) {
				t.Error("wrong request digest")
			}
			if captcha {
				cookie, err := r.Cookie("challenge")
				if err != nil || cookie.Value != "challenge-1" || f.Get("captcha") != "AB12" {
					t.Errorf("captcha/session mismatch: %v %v", cookie, err)
				}
				http.SetCookie(w, &http.Cookie{Name: "challenge", Value: "", MaxAge: -1, Path: "/qdgj/"})
			} else if f.Has("captcha") {
				t.Error("unexpected captcha field")
			}
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "authenticated", Path: "/qdgj/", HttpOnly: true})
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(httpStatus)
			fmt.Fprintf(w, `{"meta":{"success":%t,"statusCode":%d,"message":"fixture"},"data":{"userId":"test-user","tokenId":"fixture-token"}}`, apiStatus == 200, apiStatus)
		default:
			http.NotFound(w, r)
		}
	}))
}
