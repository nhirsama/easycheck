package cli

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
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

const fixtureLoginPublicKey = "MFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBAM0lIa8taIYX0beck8CCKoublJ0oiSsPPtcPdtjuhRK4lVduwmqL0hlRiyBgAiF5K9VRmUagZGgBCoCZ6BdvVB0CAwEAAQ=="

func TestLoginConfigurationPriority(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	publicKey := base64.StdEncoding.EncodeToString(der)
	for _, mode := range []string{"arguments only", "arguments override environment", "environment only", "mixed", "stdin overrides environment"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			envSession, argSession := filepath.Join(dir, "env-session.json"), filepath.Join(dir, "arg-session.json")
			wantUsername, wantPassword, wantSession := "env-user", "env-password", envSession
			var posts, fallbackCalls atomic.Int32
			fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fallbackCalls.Add(1)
				http.Error(w, "unexpected environment target", http.StatusBadRequest)
			}))
			defer fallback.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/qdgj/":
					fmt.Fprintf(w, `<script>encryptor.setPublicKey("%s")</script>`, publicKey)
				case "/qdgj/ng/captcha/status":
					fmt.Fprint(w, `{"meta":{"statusCode":200},"data":false}`)
				case "/qdgj/login":
					posts.Add(1)
					if err := r.ParseForm(); err != nil {
						t.Error(err)
					}
					if r.PostForm.Get("username") != wantUsername {
						t.Error("login used the wrong username source")
					}
					ciphertext, err := base64.StdEncoding.DecodeString(r.PostForm.Get("password"))
					if err != nil {
						t.Error(err)
					}
					plaintext, err := rsa.DecryptPKCS1v15(rand.Reader, key, ciphertext)
					if err != nil || string(plaintext) != wantPassword {
						t.Error("login used the wrong password source")
					}
					fmt.Fprint(w, `{"meta":{"statusCode":200},"data":{"userId":"fixture-user","tokenId":"fixture-token"}}`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			t.Setenv("EASYCHECK_BASE_URL", server.URL+"/qdgj/")
			t.Setenv("EASYCHECK_USERNAME", "env-user")
			t.Setenv("EASYCHECK_PASSWORD", "env-password")
			t.Setenv("EASYCHECK_SESSION_FILE", envSession)
			args, input := []string{"login"}, ""
			switch mode {
			case "arguments only", "arguments override environment":
				if mode == "arguments only" {
					for _, name := range []string{"EASYCHECK_BASE_URL", "EASYCHECK_USERNAME", "EASYCHECK_PASSWORD", "EASYCHECK_SESSION_FILE"} {
						t.Setenv(name, "")
					}
				} else {
					t.Setenv("EASYCHECK_BASE_URL", fallback.URL+"/qdgj/")
				}
				args = append(args, "--base-url", server.URL+"/qdgj/", "--username", "arg-user", "--password", "arg-password", "--session", argSession)
				wantUsername, wantPassword, wantSession = "arg-user", "arg-password", argSession
			case "mixed":
				args = append(args, "--username", "arg-user")
				wantUsername = "arg-user"
			case "stdin overrides environment":
				args = append(args, "--password-stdin")
				input, wantPassword = "stdin-password\n", "stdin-password"
			}
			var output, diagnostics bytes.Buffer
			if err := run(args, strings.NewReader(input), &output, &diagnostics); err != nil {
				t.Fatal(err)
			}
			if posts.Load() != 1 || fallbackCalls.Load() != 0 {
				t.Fatal("login used the wrong base URL or submitted more than once")
			}
			if _, err := os.Stat(wantSession); err != nil {
				t.Fatal("session was not saved to the selected path")
			}
			if wantSession != envSession {
				if _, err := os.Stat(envSession); !os.IsNotExist(err) {
					t.Fatal("login wrote to the overridden environment session path")
				}
			}
			for _, secret := range []string{wantPassword, "fixture-token"} {
				if strings.Contains(output.String()+diagnostics.String(), secret) {
					t.Fatal("login disclosed a credential")
				}
			}
		})
	}
}

func TestLoginRejectsEmptyOverridesAndPasswordConflict(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	t.Setenv("EASYCHECK_BASE_URL", server.URL+"/qdgj/")
	t.Setenv("EASYCHECK_USERNAME", "env-user")
	t.Setenv("EASYCHECK_PASSWORD", "env-password")
	t.Setenv("EASYCHECK_SESSION_FILE", filepath.Join(t.TempDir(), "env-session.json"))
	for _, args := range [][]string{
		{"--base-url", ""}, {"--session", ""}, {"--username", ""}, {"--password", ""},
		{"--password", "arg-password", "--password-stdin"}, {"--password-stdin"},
	} {
		if err := run(append([]string{"login"}, args...), strings.NewReader("\n"), io.Discard, io.Discard); err == nil {
			t.Fatal("empty override or conflicting password sources were accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid configuration sent a request")
	}
}

func TestLoginHelpHidesPasswordArgument(t *testing.T) {
	const password = "private-arg-password"
	var output, diagnostics bytes.Buffer
	if err := run([]string{"login", "--password", password, "--help"}, strings.NewReader(""), &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String()+diagnostics.String(), password) || !strings.Contains(output.String(), "--password PASSWORD") {
		t.Fatal("help must show the password option and hide its value")
	}
}

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
	for _, command := range []string{"login", "list", "detail", "sign"} {
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

func TestCheckCommandAndFlagAreRemoved(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()
	t.Setenv("EASYCHECK_BASE_URL", server.URL+"/qdgj/")
	t.Setenv("EASYCHECK_USERNAME", "fixture-user")
	t.Setenv("EASYCHECK_PASSWORD", "fixture-password")
	for _, args := range [][]string{{"check"}, {"check", "--help"}, {"help", "check"}, {"-check"}, {"login", "--check"}} {
		err := run(args, strings.NewReader(""), io.Discard, io.Discard)
		var usage *usageError
		if !errors.As(err, &usage) {
			t.Errorf("removed check entry accepted: %v (%v)", args, err)
		}
	}
	var output bytes.Buffer
	if err := run([]string{"--help"}, strings.NewReader(""), &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 || strings.Contains(output.String(), "检查手机端入口和验证码状态") {
		t.Fatalf("removed check entry sent requests or remains in help: calls=%d output=%s", calls.Load(), &output)
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
