package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"easycheck/qdgj"
)

func activeTaskFixture(batch string) qdgj.Task {
	return qdgj.Task{
		TaskID: "task-" + batch, BatchID: batch, Name: "fixture " + batch,
		Method: "定位签到", Status: "进行中", CheckInStatus: "未签到",
		StartTime: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		EndTime:   time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano),
	}
}

func writeTaskPage(t *testing.T, w http.ResponseWriter, tasks []qdgj.Task, page, pages int) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(map[string]any{
		"code": 200, "data": map[string]any{"list": tasks, "pageNum": page, "pageCount": pages},
	}); err != nil {
		t.Error(err)
	}
}

func automaticSignCLIFixture(t *testing.T, submit http.HandlerFunc) (string, string) {
	t.Helper()
	return taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/qdgj/api/sm-registration/qdgjQdrw/queryQdrw" {
			var query struct {
				TaskID     string          `json:"rwid"`
				BatchID    string          `json:"pcid"`
				Pagination json.RawMessage `json:"pagination"`
			}
			if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
				t.Error(err)
			}
			if query.TaskID != "" || query.Pagination != nil && query.BatchID != "" || query.Pagination == nil && query.BatchID != "batch" {
				t.Errorf("wrong automatic query: %+v", query)
			}
			writeTaskPage(t, w, []qdgj.Task{activeTaskFixture("batch")}, 1, 1)
			return
		}
		if r.URL.Path != "/qdgj/api/sm-registration/qdgjQdrw/startSign" {
			t.Errorf("unexpected automatic sign request: %s", r.URL.Path)
		}
		submit(w, r)
	})
}

func automaticSignArgs(base, session string) []string {
	return []string{"sign", "--base-url", base, "--session", session,
		"--address", "fixture", "--longitude", "10.5", "--latitude", "20.5", "--json"}
}

func TestAutomaticSignDiscoversAllCurrentTasksAcrossPages(t *testing.T) {
	for _, filtered := range []bool{false, true} {
		t.Run(fmt.Sprintf("filtered=%t", filtered), func(t *testing.T) {
			var pages []int
			var details, signed []string
			base, session := taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/qdgj/api/sm-registration/qdgjQdrw/queryQdrw":
					var query struct {
						BatchID    string                        `json:"pcid"`
						TaskID     string                        `json:"rwid"`
						Pagination *struct{ Page, PageSize int } `json:"pagination"`
					}
					if err := json.NewDecoder(r.Body).Decode(&query); err != nil || query.TaskID != "" {
						t.Errorf("bad discovery query: %+v (%v)", query, err)
					}
					if query.Pagination != nil {
						if len(details) != 0 || len(signed) != 0 || query.BatchID != "" || query.Pagination.PageSize != 100 {
							t.Error("must finish unfiltered pagination before looking up or signing a batch")
						}
						page := query.Pagination.Page
						pages = append(pages, page)
						if page == 1 {
							done := activeTaskFixture("done")
							done.CheckInStatus = "已签到"
							future := activeTaskFixture("future")
							future.StartTime = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
							ended := activeTaskFixture("ended")
							ended.EndTime = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
							inactive := activeTaskFixture("inactive")
							inactive.Status = "未开始"
							qr := activeTaskFixture("qr")
							qr.Method = "二维码签到"
							writeTaskPage(t, w, []qdgj.Task{activeTaskFixture("first"), done, future, ended, inactive, qr, activeTaskFixture("changed"), activeTaskFixture("closed")}, page, 2)
						} else if page == 2 {
							writeTaskPage(t, w, []qdgj.Task{activeTaskFixture("first"), activeTaskFixture("second")}, page, 2)
						} else {
							t.Errorf("unexpected list page: %d", page)
						}
						return
					}
					if len(pages) != 2 {
						t.Error("did not finish pagination before detail lookup")
					}
					details = append(details, query.BatchID)
					task := activeTaskFixture(query.BatchID)
					if query.BatchID == "changed" {
						task.CheckInStatus = "已签到"
					} else if query.BatchID == "closed" {
						task.EndTime = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
					}
					writeTaskPage(t, w, []qdgj.Task{task}, 1, 1)
				case "/qdgj/api/sm-registration/qdgjQdrw/startSign":
					var body struct {
						BatchID string `json:"pcid"`
						TaskID  string `json:"rwid"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TaskID != "task-"+body.BatchID {
						t.Errorf("wrong signing payload: %+v (%v)", body, err)
					}
					signed = append(signed, body.BatchID)
					fmt.Fprint(w, `{"code":200,"data":{}}`)
				default:
					t.Errorf("unexpected endpoint: %s", r.URL.Path)
				}
			})
			args := automaticSignArgs(base, session)
			var output bytes.Buffer
			if filtered {
				// cron 的可选批次筛选复用此执行器；手动 --pcid 保留单次提交行为。
				client, err := qdgj.NewClient(qdgj.Config{BaseURL: base, SessionFile: session})
				if err != nil {
					t.Fatal(err)
				}
				if err := client.LoadSession(); err != nil {
					t.Fatal(err)
				}
				a := &app{output: &output, diagnostics: io.Discard}
				_, err = a.signCurrentTasks(context.Background(), client, qdgj.SignInRequest{BatchID: "second", Location: qdgj.Location{Address: "fixture", Longitude: 10.5, Latitude: 20.5}}, responseOptions{json: true})
				if err != nil {
					t.Fatal(err)
				}
			} else if err := run(args, strings.NewReader(""), &output, io.Discard); err != nil {
				t.Fatal(err)
			}
			var report automaticSignReport
			if err := json.Unmarshal(output.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(pages) != "[1 2]" || report.Failed != 0 {
				t.Fatalf("wrong pages or failures: pages=%v report=%+v", pages, report)
			}
			if !filtered && (fmt.Sprint(signed) != "[first second]" || fmt.Sprint(details) != "[first changed closed second]" || report.Matched != 4 || report.Succeeded != 2 || report.Skipped != 7) {
				t.Fatalf("wrong automatic selection: signed=%v details=%v report=%+v", signed, details, report)
			}
			if filtered && (fmt.Sprint(signed) != "[second]" || fmt.Sprint(details) != "[second]" || report.Matched != 1 || report.Succeeded != 1) {
				t.Fatalf("batch filter ignored: signed=%v details=%v report=%+v", signed, details, report)
			}
		})
	}
}

func TestAutomaticSignNoActiveTasksIsSuccessfulNoOp(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprint(empty), func(t *testing.T) {
			var queries atomic.Int32
			base, session := taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
				queries.Add(1)
				if r.URL.Path != "/qdgj/api/sm-registration/qdgjQdrw/queryQdrw" {
					t.Error("no active tasks must not submit")
				}
				tasks := []qdgj.Task{}
				if !empty {
					task := activeTaskFixture("already-signed")
					task.CheckInStatus = "已签到"
					tasks = append(tasks, task)
				}
				writeTaskPage(t, w, tasks, 1, 1)
			})
			var output bytes.Buffer
			args := automaticSignArgs(base, session)
			args = args[:len(args)-1] // 人类可读的空结果。
			if err := run(args, strings.NewReader(""), &output, io.Discard); err != nil || queries.Load() != 1 || !strings.Contains(output.String(), "当前没有处于签到窗口") {
				t.Fatalf("empty discovery: queries=%d output=%s err=%v", queries.Load(), &output, err)
			}
		})
	}
}

func TestAutomaticSignContinuesAfterBatchFailure(t *testing.T) {
	var signed []string
	base, session := taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/queryQdrw") {
			writeTaskPage(t, w, []qdgj.Task{activeTaskFixture("bad"), activeTaskFixture("good")}, 1, 1)
			return
		}
		var body struct {
			BatchID string `json:"pcid"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		signed = append(signed, body.BatchID)
		if body.BatchID == "bad" {
			fmt.Fprint(w, `{"code":409,"meta":{"message":"fixture denied"}}`)
		} else {
			fmt.Fprint(w, `{"code":200,"data":{}}`)
		}
	})
	var output bytes.Buffer
	err := run(automaticSignArgs(base, session), strings.NewReader(""), &output, io.Discard)
	var apiError *qdgj.APIError
	var report automaticSignReport
	if !errors.As(err, &apiError) || apiError.Code != 409 || json.Unmarshal(output.Bytes(), &report) != nil || report.Succeeded != 1 || report.Failed != 1 || fmt.Sprint(signed) != "[bad good]" {
		t.Fatalf("failed batch blocked others or was retried: signed=%v output=%s err=%v", signed, &output, err)
	}
}

func TestAutomaticSignInvalidWindowDoesNotSubmitThatBatch(t *testing.T) {
	var submissions atomic.Int32
	base, session := taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/queryQdrw") {
			task := activeTaskFixture("malformed")
			task.EndTime = ""
			writeTaskPage(t, w, []qdgj.Task{task}, 1, 1)
			return
		}
		submissions.Add(1)
	})
	var output bytes.Buffer
	err := run(automaticSignArgs(base, session), strings.NewReader(""), &output, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "签到结束时间") || submissions.Load() != 0 {
		t.Fatalf("invalid window signed: signs=%d output=%s err=%v", submissions.Load(), &output, err)
	}
}

func TestAutomaticSignPaginationFailureNeverSubmits(t *testing.T) {
	for _, mode := range []string{"query error", "wrong page", "repeated page", "empty page"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			base, session := taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if !strings.HasSuffix(r.URL.Path, "/queryQdrw") {
					t.Error("must not submit after incomplete discovery")
				}
				var query struct {
					Pagination *struct{ Page int } `json:"pagination"`
				}
				if err := json.NewDecoder(r.Body).Decode(&query); err != nil || query.Pagination == nil {
					t.Errorf("expected paginated discovery, got %+v (%v)", query, err)
					return
				}
				page := query.Pagination.Page
				if page == 1 {
					writeTaskPage(t, w, []qdgj.Task{activeTaskFixture("first")}, 1, 3)
					return
				}
				switch mode {
				case "query error":
					fmt.Fprint(w, `{"code":503,"meta":{"message":"fixture list failure"}}`)
				case "wrong page":
					writeTaskPage(t, w, []qdgj.Task{activeTaskFixture("second")}, 1, 3)
				case "repeated page":
					writeTaskPage(t, w, []qdgj.Task{activeTaskFixture("first")}, page, 3)
				case "empty page":
					writeTaskPage(t, w, []qdgj.Task{}, page, 3)
				}
			})
			var output bytes.Buffer
			err := run(automaticSignArgs(base, session), strings.NewReader(""), &output, io.Discard)
			if err == nil || calls.Load() != 2 || output.Len() != 0 {
				t.Fatalf("pagination error ignored: calls=%d output=%s err=%v", calls.Load(), &output, err)
			}
		})
	}
}

func TestAutomaticSignPaginationWithoutPageCount(t *testing.T) {
	for _, total := range []int{0, 2} {
		t.Run(fmt.Sprintf("total=%d", total), func(t *testing.T) {
			var queries, signs atomic.Int32
			base, session := taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/startSign") {
					signs.Add(1)
					fmt.Fprint(w, `{"code":200,"data":{}}`)
					return
				}
				var query struct {
					BatchID    string              `json:"pcid"`
					Pagination *struct{ Page int } `json:"pagination"`
				}
				if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
					t.Error(err)
				}
				tasks := []qdgj.Task{}
				if query.Pagination != nil {
					queries.Add(1)
					if query.Pagination.Page <= 2 {
						tasks = append(tasks, activeTaskFixture(fmt.Sprint(query.Pagination.Page)))
					}
				} else {
					tasks = append(tasks, activeTaskFixture(query.BatchID))
				}
				json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"list": tasks, "total": total}})
			})
			if err := run(automaticSignArgs(base, session), strings.NewReader(""), io.Discard, io.Discard); err != nil {
				t.Fatal(err)
			}
			wantQueries := int32(3)
			if total > 0 {
				wantQueries = 2
			}
			if signs.Load() != 2 || queries.Load() != wantQueries {
				t.Fatalf("pagination fallback failed: lists=%d signs=%d", queries.Load(), signs.Load())
			}
		})
	}
}

func TestAutomaticSignCancellationAfterListDoesNotSubmit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	base, session := taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		cancel()
		writeTaskPage(t, w, []qdgj.Task{activeTaskFixture("batch")}, 1, 1)
	})
	err := Run(ctx, automaticSignArgs(base, session), strings.NewReader(""), io.Discard, io.Discard)
	if !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Fatalf("cancellation after list ignored: calls=%d err=%v", calls.Load(), err)
	}
}

func TestAutomaticSignSavesReportWithoutChangingSession(t *testing.T) {
	base, session := automaticSignCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code":200,"data":{"accepted":true}}`)
	})
	before, err := os.ReadFile(session)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "result.json")
	var output bytes.Buffer
	args := append(automaticSignArgs(base, session), "--out", path)
	if err := run(args, strings.NewReader(""), &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(path)
	var compact bytes.Buffer
	if compactErr := json.Compact(&compact, output.Bytes()); err != nil || compactErr != nil || !bytes.Equal(saved, compact.Bytes()) {
		t.Fatalf("report not saved: %s (%v)", saved, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("report permissions not private: %v", err)
	}
	after, err := os.ReadFile(session)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("automatic sign modified session: %v", err)
	}
}
