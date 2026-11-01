package qdgj

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetTaskByBatchID(t *testing.T) {
	const raw = `{"code":200,"data":{"list":[{"rwid":"other-task","pcid":"other-batch"},{"rwid":"task","pcid":"batch","qdrw":"fixture batch"}]}}`
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		assertAuth(t, r, queryPath)
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["rwid"] != nil || body["pagination"] != nil || string(body["userid"]) != `"fixture-user"` {
			t.Errorf("wrong batch-only query: %v (%v)", body, err)
		}
		if calls == 1 && string(body["pcid"]) != `"batch"` {
			t.Errorf("batch ID missing from query: %v", body)
		}
		fmt.Fprint(w, raw)
	}))
	defer server.Close()
	client := restoredClient(t, server.URL)
	detail, err := client.GetTask(context.Background(), "", "batch")
	if err != nil {
		t.Fatal(err)
	}
	if detail.TaskID != "task" || detail.BatchID != "batch" || detail.Name != "fixture batch" || string(detail.RawResponse) != raw {
		t.Fatalf("wrong batch selected or raw response lost: %+v", detail)
	}
	if _, err := client.GetTask(context.Background(), "", "missing"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("missing batch: %v", err)
	}
	if calls != 2 {
		t.Fatalf("batch lookup retried: %d requests", calls)
	}
}

func TestGetTaskRejectsEmptyIdentifiersBeforeNetwork(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer server.Close()
	client := restoredClient(t, server.URL)
	for _, ids := range [][2]string{{"", ""}, {" ", "\t"}} {
		if _, err := client.GetTask(context.Background(), ids[0], ids[1]); err == nil {
			t.Error("empty identifiers accepted")
		}
	}
	if calls != 0 {
		t.Fatalf("invalid lookup sent %d requests", calls)
	}
}

func TestSignInResolvesTaskIDFromBatch(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		t.Run(fmt.Sprintf("allowed-location=%t", allowed), func(t *testing.T) {
			queries, submissions := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/qdgj/" + queryPath:
					queries++
					assertAuth(t, r, queryPath)
					var body map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["rwid"] != nil || string(body["pcid"]) != `"batch"` || body["pagination"] != nil || string(body["userid"]) != `"fixture-user"` {
						t.Errorf("wrong batch-only query: %v (%v)", body, err)
					}
					fmt.Fprint(w, `{"code":200,"data":{"list":[{"rwid":"wrong-task","pcid":"wrong-batch"},{"rwid":"resolved-task","pcid":"batch","qddds":[{"qddd":"fixture allowed address","jdzb":10.5,"wdzb":20.5,"yxbj":500}]}]}}`)
				case "/qdgj/" + signPath:
					submissions++
					assertAuth(t, r, signPath)
					var body signPayload
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if queries != 1 || body.TaskID != "resolved-task" || body.BatchID != "batch" || body.UserID != "fixture-user" {
						t.Errorf("sign did not resolve the matching task: %+v (queries=%d)", body, queries)
					}
					if allowed {
						if body.Address != fixtureMapAddress(body.Longitude, body.Latitude) {
							t.Errorf("allowed location lost: %+v", body)
						}
					} else if body.Address != "fixture custom address" || body.Longitude != 10.5 || body.Latitude != 20.5 {
						t.Errorf("custom location changed: %+v", body)
					}
					fmt.Fprint(w, `{"code":200,"data":{}}`)
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
				}
			}))
			defer server.Close()
			client := restoredGeocodingClient(t, server.URL)
			request := SignInRequest{BatchID: "batch", UseAllowedLocation: allowed}
			if !allowed {
				request.Location = Location{Address: "fixture custom address", Longitude: 10.5, Latitude: 20.5}
			}
			if _, err := client.SignIn(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			if queries != 1 || submissions != 1 {
				t.Fatalf("wrong request count: queries=%d submissions=%d", queries, submissions)
			}
		})
	}
}

func TestSignInBatchLookupFailureStopsSubmission(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response string
		wantErr  error
		wantText string
	}{
		{name: "query failure", response: `{"code":503,"meta":{"message":"fixture unavailable"}}`, wantText: "fixture unavailable"},
		{name: "missing batch", response: `{"code":200,"data":{"list":[]}}`, wantErr: ErrTaskNotFound},
		{name: "wrong batch", response: `{"code":200,"data":{"list":[{"rwid":"task","pcid":"other-batch"}]}}`, wantErr: ErrTaskNotFound},
		{name: "missing task ID", response: `{"code":200,"data":{"list":[{"pcid":"batch"}]}}`, wantText: "缺少任务 ID"},
		{name: "blank task ID", response: `{"code":200,"data":{"list":[{"rwid":" ","pcid":"batch"}]}}`, wantText: "缺少任务 ID"},
	} {
		for _, allowed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/allowed-location=%t", tc.name, allowed), func(t *testing.T) {
				queries, submissions := 0, 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/qdgj/"+queryPath {
						queries++
						fmt.Fprint(w, tc.response)
					} else {
						submissions++
						fmt.Fprint(w, `{"code":200,"data":{}}`)
					}
				}))
				defer server.Close()
				client := restoredClient(t, server.URL)
				request := SignInRequest{BatchID: "batch", UseAllowedLocation: allowed}
				if !allowed {
					request.Location = Location{Address: "fixture custom address", Longitude: 10.5, Latitude: 20.5}
				}
				_, err := client.SignIn(context.Background(), request)
				if err == nil || queries != 1 || submissions != 0 {
					t.Fatalf("lookup failure ignored: queries=%d submissions=%d err=%v", queries, submissions, err)
				}
				if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
					t.Errorf("error identity lost: %v", err)
				}
				if tc.wantText != "" && !strings.Contains(err.Error(), tc.wantText) {
					t.Errorf("wrong lookup error: %v", err)
				}
			})
		}
	}
}
