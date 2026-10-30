package qdgj

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func keyResourceResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func TestLoginDiscoversAndRefreshesPublicKey(t *testing.T) {
	var keys []*rsa.PrivateKey
	var encoded []string
	for range 2 {
		key, err := rsa.GenerateKey(rand.Reader, 1024)
		if err != nil {
			t.Fatal(err)
		}
		der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
		encoded = append(encoded, base64.StdEncoding.EncodeToString(der))
	}
	posts := 0
	var requests []string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Header.Get("Sec-CH-UA-Mobile") != "?1" || !strings.Contains(r.UserAgent(), "Mobile") {
			t.Error("missing mobile headers")
		}
		if r.URL.Path != "/app/" {
			if cookie, err := r.Cookie("entry"); err != nil || cookie.Value != "fixture" {
				t.Error("entry cookie lost")
			}
		}
		switch r.URL.Path {
		case "/app/":
			resp := keyResourceResponse(r, http.StatusOK, `<script src="js/vendor.js"></script><script src="app.js"></script>`)
			resp.Header.Add("Set-Cookie", "entry=fixture; Path=/app/")
			return resp, nil
		case "/app/app.js":
			return keyResourceResponse(r, http.StatusOK, `n.u=function(e){return "js/chunk/"+e+".js"};var routes=[{url:"/mobile/login",component:r.lazy(function(){return n.e(42).then(n.bind(n,100))})}]`), nil
		case "/app/js/chunk/42.js":
			return keyResourceResponse(r, http.StatusOK, fmt.Sprintf(`var n=new JSEncrypt;n.setPublicKey("%s");n.encrypt(e.password)`, encoded[posts])), nil
		case "/app/ng/captcha/status":
			return keyResourceResponse(r, http.StatusOK, `{"meta":{"statusCode":200},"data":false}`), nil
		case "/app/login":
			if r.Method != http.MethodPost {
				t.Fatal("wrong login method")
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			form, err := url.ParseQuery(string(body))
			if err != nil {
				t.Fatal(err)
			}
			ciphertext, err := base64.StdEncoding.DecodeString(form.Get("password"))
			if err != nil {
				t.Fatal(err)
			}
			plaintext, err := rsa.DecryptPKCS1v15(rand.Reader, keys[posts], ciphertext)
			if err != nil || !bytes.Equal(plaintext, frontendPasswordBytes("密fixture")) {
				t.Fatalf("password did not use the current page key: %v", err)
			}
			if form.Get("username") != "fixture-user" || form.Get("digest") == "" {
				t.Error("missing login form fields")
			}
			posts++
			return keyResourceResponse(r, http.StatusOK, string(fixtureSession("").LoginResponse)), nil
		default:
			t.Fatalf("unexpected request: %s", r.URL)
			return nil, errors.New("unexpected request")
		}
	})
	client, err := NewClient(Config{BaseURL: "https://example.invalid/app/", Username: "fixture-user", Password: "密fixture", HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := client.Login(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"GET /app/", "GET /app/app.js", "GET /app/js/chunk/42.js", "GET /app/ng/captcha/status", "POST /app/login"}
	want = append(want, want...)
	if posts != 2 || !reflect.DeepEqual(requests, want) {
		t.Fatalf("unexpected login requests: %v", requests)
	}
}

func TestExtractLoginPublicKeyFormats(t *testing.T) {
	key, err := parseLoginPublicKey(fixturePublicKey)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	pkcs1 := base64.StdEncoding.EncodeToString(x509.MarshalPKCS1PublicKey(key))
	for name, source := range map[string]string{
		"inline":        fmt.Sprintf(`<script>n.setPublicKey("%s")</script>`, fixturePublicKey),
		"field":         fmt.Sprintf(`const loginPublicKey = '%s'`, fixturePublicKey),
		"json":          fmt.Sprintf(`{"publicKey":"%s"}`, fixturePublicKey),
		"pem":           fmt.Sprintf("n.setPublicKey(`%s`)", pemKey),
		"escaped PEM":   fmt.Sprintf(`n.setPublicKey("%s")`, strings.ReplaceAll(pemKey, "\n", `\n`)),
		"PKCS1":         fmt.Sprintf(`const PUBLIC_KEY="%s"`, pkcs1),
		"escaped slash": fmt.Sprintf(`n.setPublicKey("%s")`, strings.ReplaceAll(fixturePublicKey, "/", `\/`)),
		"duplicate":     fmt.Sprintf(`n.setPublicKey("%s");n.setPublicKey("%s")`, fixturePublicKey, fixturePublicKey),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := extractLoginPublicKey([]byte(source))
			if err != nil || got != fixturePublicKey {
				t.Fatalf("could not extract normalized key: %v", err)
			}
		})
	}
}

func TestLoginKeyDiscoveryFailures(t *testing.T) {
	key, err := parseLoginPublicKey(fixturePublicKey)
	if err != nil {
		t.Fatal(err)
	}
	key.E = 3
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatal(err)
	}
	otherKey := base64.StdEncoding.EncodeToString(der)
	for name, tc := range map[string]struct {
		page         string
		pageStatus   int
		scriptStatus int
	}{
		"missing":         {page: "<html></html>", pageStatus: 200},
		"invalid":         {page: `n.setPublicKey("invalid key")`, pageStatus: 200},
		"ambiguous":       {page: fmt.Sprintf(`n.setPublicKey("%s");n.setPublicKey("%s")`, fixturePublicKey, otherKey), pageStatus: 200},
		"script failure":  {page: `<script src="login.js"></script>`, pageStatus: 200, scriptStatus: 404},
		"script redirect": {page: `<script src="login.js"></script>`, pageStatus: 200, scriptStatus: 307},
		"entry failure":   {pageStatus: 503},
	} {
		t.Run(name, func(t *testing.T) {
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet {
					t.Fatalf("key discovery submitted %s", r.Method)
				}
				switch r.URL.Path {
				case "/app/":
					return keyResourceResponse(r, tc.pageStatus, tc.page), nil
				case "/app/login.js":
					resp := keyResourceResponse(r, tc.scriptStatus, "")
					resp.Header.Set("Location", "/app/other.js")
					return resp, nil
				default:
					t.Fatalf("unexpected request: %s", r.URL)
					return nil, errors.New("unexpected request")
				}
			})
			client, err := NewClient(Config{BaseURL: "https://example.invalid/app/", Username: "fixture", Password: "secret", HTTPClient: &http.Client{Transport: transport}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Login(context.Background())
			if tc.pageStatus == http.StatusOK {
				if !errors.Is(err, ErrLoginPublicKeyNotFound) {
					t.Fatalf("missing discovery error: %v", err)
				}
			} else {
				var apiErr *APIError
				if !errors.As(err, &apiErr) || apiErr.HTTPStatus != tc.pageStatus {
					t.Fatalf("entry error lost: %v", err)
				}
			}
		})
	}
}

func TestFetchLoginPublicKeyResourceScopeAndReferences(t *testing.T) {
	var requests []string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.URL.String())
		switch r.URL.String() {
		case "https://example.invalid/app/":
			return keyResourceResponse(r, 200, `<script src="https://other.invalid/login.js"></script>
<script src="http://example.invalid/app/login.js"></script>
<script src="/application/login.js"></script>
<script src="../login.js"></script>
<script src="https://user@example.invalid/app/login.js"></script>
<script src="/app/style.css"></script>
<script src="missing.js"></script>
<script src='js/bootstrap.js?v=1&amp;kind=login'></script>`), nil
		case "https://example.invalid/app/missing.js":
			return keyResourceResponse(r, 404, ""), nil
		case "https://example.invalid/app/js/bootstrap.js?v=1&kind=login":
			return keyResourceResponse(r, 200, `import('./login.hash.js');import('./login.hash.js')`), nil
		case "https://example.invalid/app/js/login.hash.js":
			return keyResourceResponse(r, 200, fmt.Sprintf(`{"publicKey":"%s"}`, fixturePublicKey)), nil
		default:
			t.Fatalf("unexpected resource request: %s", r.URL)
			return nil, errors.New("unexpected request")
		}
	})
	client, err := NewClient(Config{BaseURL: "https://example.invalid/app/", HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.FetchLoginPublicKey(context.Background())
	if err != nil || got != fixturePublicKey || len(requests) != 4 {
		t.Fatalf("key discovery: requests=%v err=%v", requests, err)
	}
}

func TestLoginKeyDiscoveryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return keyResourceResponse(r, 200, `<script src="first.js"></script><script src="second.js"></script>`), nil
		}
		cancel()
		return nil, r.Context().Err()
	})
	client, err := NewClient(Config{BaseURL: "https://example.invalid/app/", Username: "fixture", Password: "secret", HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Login(ctx); !errors.Is(err, context.Canceled) || calls != 2 {
		t.Fatalf("cancellation lost: calls=%d err=%v", calls, err)
	}
}

func TestLoginKeyDiscoveryResourceLimits(t *testing.T) {
	for _, limit := range []string{"count", "individual size", "total size"} {
		t.Run(limit, func(t *testing.T) {
			calls := 0
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodGet {
					t.Fatalf("key discovery submitted %s", r.Method)
				}
				body := fmt.Sprintf(`<script src="%d.js"></script>`, calls)
				if calls > 1 {
					switch limit {
					case "individual size":
						body = strings.Repeat(" ", maxResponseSize+1)
					case "total size":
						body += strings.Repeat(" ", maxResponseSize-len(body))
					}
				}
				return keyResourceResponse(r, 200, body), nil
			})
			client, err := NewClient(Config{BaseURL: "https://example.invalid/app/", Username: "fixture", Password: "secret", HTTPClient: &http.Client{Transport: transport}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Login(context.Background())
			if !errors.Is(err, ErrLoginPublicKeyNotFound) {
				t.Fatalf("missing resource limit error: %v", err)
			}
			wantCalls := map[string]int{"count": 17, "individual size": 2, "total size": 5}[limit]
			if calls != wantCalls {
				t.Fatalf("resource limit: calls=%d want=%d", calls, wantCalls)
			}
		})
	}
}
