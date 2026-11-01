package cli

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
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"easycheck/qdgj"
)

func taskCLIFixture(t *testing.T, handler http.HandlerFunc) (string, string) {
	t.Helper()
	// 测试绝不使用运行者环境中的真实通知渠道。
	clearNotificationEnv(t)
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

func signCLIFixture(t *testing.T, handler http.HandlerFunc) (string, string) {
	t.Helper()
	return taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/qdgj/api/sm-registration/qdgjQdrw/queryQdrw" {
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["rwid"] != nil || string(body["pcid"]) != `"batch"` || body["pagination"] != nil {
				t.Errorf("wrong batch-only query: %v (%v)", body, err)
			}
			fmt.Fprint(w, `{"code":200,"data":{"list":[{"rwid":"task","pcid":"batch"}]}}`)
			return
		}
		if r.URL.Path != "/qdgj/api/sm-registration/qdgjQdrw/startSign" {
			t.Errorf("unexpected sign endpoint: %s", r.URL.Path)
		}
		handler(w, r)
	})
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
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["rwid"] != nil || string(body["pcid"]) != `"batch"` || body["pagination"] != nil {
			t.Errorf("wrong detail query: %v (%v)", body, err)
		}
		fmt.Fprint(w, raw)
	})
	var output bytes.Buffer
	if err := run([]string{"detail", "-base-url", base, "-session", session, "-pcid", "batch"}, strings.NewReader(""), &output, io.Discard); err != nil {
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
			base, session := signCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
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
			args := []string{"sign", "-base-url", base, "-session", session, "-pcid", "batch", "-address", "fixture address", "-longitude", "0", "-latitude", "0", "-out", out}
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
			base, session := signCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if success {
					fmt.Fprint(w, `{"code":200,"data":{}}`)
				} else {
					fmt.Fprint(w, `{"code":409,"meta":{"message":"fixture already signed"}}`)
				}
			})
			args := []string{"sign", "-base-url", base, "-session", session, "-pcid", "batch", "-address", "fixture", "-longitude", "10.5", "-latitude", "20.5", "-out", t.TempDir()}
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

type cliRoundTripFunc func(*http.Request) (*http.Response, error)

func (f cliRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSignCommandResolvesAddressFromCoordinates(t *testing.T) {
	for _, tc := range []struct {
		name      string
		args      []string
		automatic bool
	}{
		{name: "allowed place"},
		{name: "custom coordinates", args: []string{"--longitude", "10.5", "--latitude", "20.5"}},
		{name: "zero coordinates", args: []string{"--longitude", "0", "--latitude", "0"}},
		{name: "automatic allowed place", automatic: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var queries, configs, lookups, submissions atomic.Int32
			base, session := taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/qdgj/api/sm-registration/qdgjQdrw/queryQdrw":
					queries.Add(1)
					var body map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["rwid"] != nil {
						t.Errorf("wrong default location query: %v (%v)", body, err)
					}
					if !tc.automatic || body["pagination"] == nil {
						if string(body["pcid"]) != `"batch"` {
							t.Error("detail query is missing the discovered batch ID")
						}
					} else if body["pcid"] != nil {
						t.Error("automatic discovery must not require a batch ID")
					}
					task := activeTaskFixture("batch")
					task.TaskID = "task"
					var places []qdgj.AllowedLocation
					if err := json.Unmarshal([]byte(`[{"qddd":"fixture building","qdzx":"fixture campus","jdzb":10.5,"wdzb":20.5,"yxbj":1000}]`), &places); err != nil {
						t.Error(err)
						return
					}
					task.AllowedLocations = places
					writeTaskPage(t, w, []qdgj.Task{task}, 1, 1)
				case "/qdgj/api/zhxg-xtgl/openapi/getCsszBatch":
					configs.Add(1)
					fmt.Fprint(w, `{"code":200,"data":[{"csbz":"ZHXG_QDGJ_DTKEY","csz":"fixture-map-key","zt":"1"},{"csbz":"ZHXG_QDGJ_DTLX","csz":"3","zt":"1"}]}`)
				case "/qdgj/api/sm-registration/qdgjQdrw/startSign":
					submissions.Add(1)
					var body struct {
						TaskID    string  `json:"rwid"`
						BatchID   string  `json:"pcid"`
						Address   string  `json:"qddd"`
						Longitude float64 `json:"jdzb"`
						Latitude  float64 `json:"wdzb"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					expectedAddress := "fixture resolved address " + strconv.FormatFloat(body.Latitude, 'f', -1, 64) + "," + strconv.FormatFloat(body.Longitude, 'f', -1, 64)
					if body.TaskID != "task" || body.BatchID != "batch" || body.Address != expectedAddress || configs.Load() != 1 || lookups.Load() != 1 {
						t.Errorf("payload did not use reverse geocoding: %+v", body)
					}
					if (tc.name == "allowed place" || tc.automatic) && (body.Longitude < 10.48 || body.Longitude > 10.52 || body.Latitude < 20.48 || body.Latitude > 20.52 || (body.Longitude == 10.5 && body.Latitude == 20.5)) {
						t.Errorf("wrong sampled coordinates: %+v", body)
					}
					if tc.name == "custom coordinates" && (body.Longitude != 10.5 || body.Latitude != 20.5) || tc.name == "zero coordinates" && (body.Longitude != 0 || body.Latitude != 0) {
						t.Errorf("custom coordinates changed: %+v", body)
					}
					fmt.Fprint(w, `{"code":200,"data":{}}`)
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
				}
			})
			originalTransport := http.DefaultTransport
			http.DefaultTransport = cliRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "apis.map.qq.com" {
					return originalTransport.RoundTrip(r)
				}
				lookups.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/ws/geocoder/v1/" || r.URL.Query().Get("key") != "fixture-map-key" {
					t.Error("wrong reverse geocoding request")
				}
				for _, name := range []string{"Cookie", "Authorization", "loginUserId", "loginUserOrgId"} {
					if r.Header.Get(name) != "" {
						t.Errorf("site credential leaked to map provider: %s", name)
					}
				}
				body, _ := json.Marshal(map[string]any{"status": 0, "result": map[string]string{"address": "fixture resolved address " + r.URL.Query().Get("location")}})
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = originalTransport })
			args := []string{"sign", "-base-url", base, "-session", session}
			wantQueries := int32(1)
			if tc.automatic {
				wantQueries = 2
			} else {
				args = append(args, "--pcid", "batch")
			}
			args = append(args, tc.args...)
			var output bytes.Buffer
			err := run(args, strings.NewReader(""), &output, io.Discard)
			if err != nil || !strings.Contains(output.String(), "签到成功") || queries.Load() != wantQueries || submissions.Load() != 1 || configs.Load() != 1 || lookups.Load() != 1 {
				t.Fatalf("sign failed: queries=%d config=%d geocode=%d submissions=%d output=%s err=%v", queries.Load(), configs.Load(), lookups.Load(), submissions.Load(), &output, err)
			}
		})
	}
}

type closedOutput struct{}

func (closedOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestSignCommandOutputFailurePreservesAcceptedState(t *testing.T) {
	var calls atomic.Int32
	base, session := signCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"code":200,"data":{}}`)
	})
	args := []string{"sign", "-base-url", base, "-session", session, "-pcid", "batch", "-address", "fixture", "-longitude", "10.5", "-latitude", "20.5"}
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
		{"detail"}, {"detail", "-pcid", ""}, {"detail", "-pcid", " "},
		{"detail", "-rwid", "task", "-pcid", "batch"},
		{"sign", "-pcid", ""}, {"sign", "-pcid", " "},
		{"sign", "-pcid", "batch", "-address", "fixture", "-latitude", "20"},
		{"sign", "-pcid", "batch", "-address", "fixture", "-longitude", "10"},
		{"sign", "-pcid", "batch", "-address", "fixture", "-longitude", "NaN", "-latitude", "20"},
		{"sign", "-pcid", "batch", "-address", "fixture", "-longitude", "10", "-latitude", "91"},
		{"sign", "-pcid", "batch", "-address", "fixture", "-longitude", "10", "-latitude", "20", "-outside-range"},
		{"sign", "-pcid", "batch", "-address", "fixture", "-longitude", "10", "-latitude", "20", "-remark", "fixture"},
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
	for _, args := range [][]string{{"-help"}, {"help"}, {"help", "sign"}, {"login", "-help"}, {"list", "-help"}, {"detail", "-help"}, {"sign", "-help"}} {
		var output bytes.Buffer
		if err := run(args, strings.NewReader(""), &output, &output); err != nil || output.Len() == 0 {
			t.Fatalf("help %v: %v", args, err)
		}
		if strings.Contains(output.String(), "--rwid") || strings.Contains(output.String(), "--check") {
			t.Fatalf("help still exposes removed flags: %s", &output)
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
	args := []string{"login", "-base-url", server.URL + "/qdgj/", "-session", filepath.Join(t.TempDir(), "session.json")}
	if err := run(args, strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 1 {
		t.Fatalf("login submitted %d logins", posts.Load())
	}
}
