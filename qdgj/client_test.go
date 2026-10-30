package qdgj

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureSession(base string) *Session {
	return &Session{BaseURL: base + "/qdgj/", UserAgent: MobileUserAgent, Authorization: "fixture-token",
		Cookies:       []*http.Cookie{{Name: "customsid", Value: "fixture-cookie"}},
		LoginResponse: json.RawMessage(`{"meta":{"statusCode":200},"data":{"userId":"fixture-user","orgId":"fixture-org","tokenId":"fixture-token","userType":"student","extra":{"retained":true}}}`)}
}

func restoredClient(t *testing.T, base string) *Client {
	t.Helper()
	c, err := NewClientFromSession(fixtureSession(base), Config{BaseURL: base + "/qdgj/"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func assertAuth(t *testing.T, r *http.Request, path string) {
	t.Helper()
	if r.Method != http.MethodPost || r.URL.Path != "/qdgj/"+path || r.URL.Query().Get("_t") == "" {
		t.Errorf("wrong request: %s %s", r.Method, r.URL)
	}
	if r.Header.Get("Content-Type") != "application/json;charset=UTF-8" || r.Header.Get("Authorization") != "fixture-token" || r.Header.Get("loginUserId") != "fixture-user" || r.Header.Get("loginUserOrgId") != "fixture-org" {
		t.Error("missing JSON/session headers")
	}
	if r.Header.Get("Sec-CH-UA-Mobile") != "?1" || !strings.Contains(r.UserAgent(), "Mobile") {
		t.Error("missing mobile headers")
	}
	if cookie, err := r.Cookie("customsid"); err != nil || cookie.Value != "fixture-cookie" {
		t.Error("missing session cookie")
	}
}

func TestLoginWithOptionalCaptchaThenRestoreAndQuery(t *testing.T) {
	t.Setenv("GODEBUG", "rsa1024min=1")
	for _, captcha := range []bool{false, true} {
		t.Run(fmt.Sprint(captcha), func(t *testing.T) {
			posts, solves := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/qdgj/":
					http.SetCookie(w, &http.Cookie{Name: "sid", Value: "initial", Path: "/qdgj/"})
					fmt.Fprintf(w, `<script>encryptor.setPublicKey("%s")</script>`, fixturePublicKey)
				case "/qdgj/ng/captcha/status":
					if cookie, err := r.Cookie("sid"); err != nil || cookie.Value != "initial" {
						t.Error("entry cookie lost")
					}
					fmt.Fprintf(w, `{"meta":{"statusCode":200},"data":%t}`, captcha)
				case "/qdgj/captchaServlet":
					http.SetCookie(w, &http.Cookie{Name: "challenge", Value: "challenge-1", Path: "/qdgj/"})
					w.Write([]byte("\x89PNG\r\n\x1a\nfixture"))
				case "/qdgj/login":
					posts++
					if err := r.ParseForm(); err != nil {
						t.Error(err)
					}
					if r.PostForm.Get("username") != "fixture-user" || r.PostForm.Get("digest") == "" || r.PostForm.Get("password") == "secret" {
						t.Error("invalid encrypted login form")
					}
					if captcha {
						if cookie, err := r.Cookie("challenge"); err != nil || cookie.Value != "challenge-1" || r.PostForm.Get("captcha") != "AB12" {
							t.Error("captcha cookie or answer lost")
						}
					}
					http.SetCookie(w, &http.Cookie{Name: "customsid", Value: "fixture-cookie", Path: "/qdgj/"})
					w.Write(fixtureSession("").LoginResponse)
				case "/qdgj/" + queryPath:
					assertAuth(t, r, queryPath)
					fmt.Fprint(w, `{"code":200,"data":{"list":[]}}`)
				default:
					t.Errorf("unexpected route %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "session.json")
			config := Config{BaseURL: server.URL + "/qdgj/",
				Username: "fixture-user", Password: "secret", SessionFile: path,
				CaptchaSolver: func(ctx context.Context, image []byte) (string, error) {
					solves++
					if !strings.HasPrefix(string(image), "\x89PNG") {
						t.Error("callback did not receive captcha image")
					}
					return " AB12 ", nil
				}}
			c, err := NewClient(config)
			if err != nil {
				t.Fatal(err)
			}
			// 修改调用方配置，验证对象保存的配置副本。
			config.Username, config.Password, config.SessionFile = "changed", "changed", "unused"
			saved, err := c.Login(context.Background())
			if err != nil || posts != 1 || solves != map[bool]int{true: 1, false: 0}[captcha] {
				t.Fatalf("login: err=%v posts=%d solves=%d", err, posts, solves)
			}
			if saved.Authorization != "fixture-token" {
				t.Fatal("Login did not return the authenticated session")
			}
			if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := c.SaveSession(); err != nil {
				t.Fatal(err)
			}
			info, _ := os.Stat(path)
			if info.Mode().Perm() != 0600 {
				t.Fatal("session mode is not 0600")
			}
			loaded, err := LoadSession(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(loaded.LoginResponse), `"extra"`) {
				t.Fatal("unmodeled login fields were lost")
			}
			restored, err := NewClient(Config{BaseURL: loaded.BaseURL, SessionFile: path})
			if err != nil {
				t.Fatal(err)
			}
			if err := restored.LoadSession(); err != nil {
				t.Fatal(err)
			}
			if _, err := restored.ListTasks(context.Background(), ListOptions{}); err != nil {
				t.Fatal(err)
			}
			if posts != 1 {
				t.Fatal("restore or query resubmitted login")
			}
		})
	}
}

func TestMissingCaptchaSolverDoesNotSubmitLogin(t *testing.T) {
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
		}
		if r.URL.Path == "/" {
			fmt.Fprintf(w, `<script>encryptor.setPublicKey("%s")</script>`, fixturePublicKey)
		}
		if strings.HasSuffix(r.URL.Path, "/status") {
			fmt.Fprint(w, `{"meta":{"statusCode":200},"data":true}`)
		}
	}))
	defer server.Close()
	c, _ := NewClient(Config{BaseURL: server.URL, Username: "fixture", Password: "secret"})
	_, err := c.Login(context.Background())
	if !errors.Is(err, ErrCaptchaRequired) || posts != 0 {
		t.Fatalf("err=%v posts=%d", err, posts)
	}
}

func TestTaskQueriesPreservePaginationAndNullableCoordinates(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		assertAuth(t, r, queryPath)
		var body taskQuery
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.UserID != "fixture-user" {
			t.Error("wrong userid")
		}
		if calls == 1 && (body.Pagination == nil || body.Pagination.Page != 2 || body.Pagination.PageSize != 5 || body.TaskID != "") {
			t.Error("wrong list pagination")
		}
		if calls == 2 && (body.Pagination != nil || body.TaskID != "task" || body.BatchID != "batch") {
			t.Error("wrong detail query")
		}
		fmt.Fprint(w, `{"code":200,"data":{"list":[{"rwid":"task","pcid":"batch","status":"进行中","zt":"未签到","qdtp":null,"qddd":null,"jdzb":null,"wdzb":"20.5","qddds":[{"jdzb":"10.5","wdzb":20.5,"yxbj":"1000"}]}],"pageNum":2,"pageSize":5,"pageCount":3,"total":11}}`)
	}))
	defer server.Close()
	c := restoredClient(t, server.URL)
	list, err := c.ListTasks(context.Background(), ListOptions{Page: 2, PageSize: 5})
	if err != nil {
		t.Fatal(err)
	}
	if list.Total != 11 || list.PageCount != 3 || len(list.Tasks) != 1 || !json.Valid(list.RawResponse) {
		t.Fatal("pagination or raw response lost")
	}
	detail, err := c.GetTask(context.Background(), "task", "batch")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Longitude != nil || detail.PhotoID != nil || detail.Latitude == nil || float64(*detail.Latitude) != 20.5 || detail.AllowedLocations[0].Radius == nil || float64(*detail.AllowedLocations[0].Radius) != 1000 {
		t.Fatal("nullable/string/number fields not decoded")
	}
	if _, err := c.GetTask(context.Background(), "missing", "batch"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("missing task: %v", err)
	}
	if calls != 3 {
		t.Fatalf("queries retried: %d", calls)
	}
}

func TestExplicitSignInPayloadAndBusinessFailureAreNotRetried(t *testing.T) {
	for _, outside := range []bool{false, true} {
		t.Run(fmt.Sprint(outside), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				assertAuth(t, r, signPath)
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if string(body["userid"]) != `"fixture-user"` || string(body["qdtp"]) != "null" || string(body["jdzb"]) != "10.5" {
					t.Error("wrong user/photo/coordinates")
				}
				if outside {
					if string(body["sfxdfwwqd"]) != `"1"` || string(body["bz"]) != `"fixture remark"` {
						t.Error("outside branch fields missing")
					}
				} else if body["sfxdfwwqd"] != nil || body["bz"] != nil {
					t.Error("unexpected outside branch")
				}
				if calls == 1 {
					fmt.Fprint(w, `{"code":200,"data":{"accepted":true}}`)
				} else {
					fmt.Fprint(w, `{"code":409,"meta":{"message":"already signed"}}`)
				}
			}))
			defer server.Close()
			c := restoredClient(t, server.URL)
			request := SignInRequest{TaskID: "task", BatchID: "batch", Location: Location{Address: "fixture address", Longitude: 10.5, Latitude: 20.5}, OutsideRange: outside}
			if outside {
				request.Remark = "fixture remark"
			}
			result, err := c.SignIn(context.Background(), request)
			if err != nil || result.Code != 200 || !json.Valid(result.RawResponse) {
				t.Fatalf("sign result: %v", err)
			}
			_, err = c.SignIn(context.Background(), request)
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Code != 409 || calls != 2 {
				t.Fatalf("failure retried or untyped: calls=%d err=%v", calls, err)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNoSignReplayOrRedirectAndValidationBeforeNetwork(t *testing.T) {
	calls := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.GetBody != nil {
			t.Error("sign request is replayable")
		}
		return &http.Response{StatusCode: 307, Header: http.Header{"Location": {"/other"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	c, err := NewClientFromSession(fixtureSession("https://fixture.invalid"), Config{BaseURL: "https://fixture.invalid/qdgj/", HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	valid := SignInRequest{TaskID: "task", BatchID: "batch", Location: Location{Address: "fixture", Longitude: 10.5, Latitude: 20.5}}
	_, err = c.SignIn(context.Background(), valid)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.HTTPStatus != 307 || calls != 1 {
		t.Fatalf("redirect followed: calls=%d err=%v", calls, err)
	}
	for _, invalid := range []SignInRequest{
		{}, {TaskID: "task", BatchID: "batch"},
		{TaskID: "task", BatchID: "batch", UseAllowedLocation: true, Location: valid.Location},
		{TaskID: "task", BatchID: "batch", UseAllowedLocation: true, OutsideRange: true},
		{TaskID: "task", BatchID: "batch", Location: Location{Address: "fixture", Longitude: math.NaN()}},
		{TaskID: "task", BatchID: "batch", Location: valid.Location, OutsideRange: true},
		{TaskID: "task", BatchID: "batch", Location: valid.Location, OutsideRange: true, Remark: strings.Repeat("😀", 51)},
	} {
		if _, err := c.SignIn(context.Background(), invalid); err == nil {
			t.Error("invalid sign request accepted")
		}
	}
	if calls != 1 {
		t.Fatal("invalid request reached network")
	}
}

func TestContextAndSessionIsolation(t *testing.T) {
	c, err := NewClient(Config{BaseURL: "https://fixture.invalid/qdgj/"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListTasks(context.Background(), ListOptions{}); !errors.Is(err, ErrNotAuthenticated) {
		t.Fatalf("unauthenticated query: %v", err)
	}
	if _, err := NewClientFromSession(fixtureSession("https://fixture.invalid"), Config{BaseURL: "https://other.invalid"}); err == nil {
		t.Fatal("session allowed on different host")
	}
	if _, err := NewClientFromSession(nil, Config{}); !errors.Is(err, ErrNotAuthenticated) {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("canceled request reached server") }))
	defer server.Close()
	c = restoredClient(t, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ListTasks(ctx, ListOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	snapshot, _ := c.Session()
	snapshot.LoginResponse[0] = 'x'
	snapshot.Cookies[0].Value = "changed"
	next, _ := c.Session()
	if !json.Valid(next.LoginResponse) || next.Cookies[0].Value != "fixture-cookie" {
		t.Fatal("session snapshot aliases client state")
	}
}
