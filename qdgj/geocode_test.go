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
	"net/url"
	"strconv"
	"strings"
	"testing"
)

const fixtureMapConfig = `{"code":200,"data":[{"csbz":"ZHXG_QDGJ_DTKEY","csz":"fixture-map-key","zt":"1"},{"csbz":"ZHXG_QDGJ_DTLX","csz":"3","zt":"1"}]}`

func fixtureMapAddress(longitude, latitude float64) string {
	return "fixture resolved address " + strconv.FormatFloat(latitude, 'f', -1, 64) + "," + strconv.FormatFloat(longitude, 'f', -1, 64)
}

func mapJSONResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}
}

func assertMapRequest(t *testing.T, req *http.Request) {
	t.Helper()
	if req.Method != http.MethodGet || req.URL.Scheme != "https" || req.URL.Host != "apis.map.qq.com" || req.URL.Path != "/ws/geocoder/v1/" {
		t.Error("wrong reverse geocoding endpoint or method")
	}
	for _, header := range []string{"Cookie", "Authorization", "loginUserId", "loginUserOrgId"} {
		if req.Header.Get(header) != "" {
			t.Errorf("site credentials leaked to map service in %s", header)
		}
	}
	if req.URL.Query().Get("output") != "json" || req.Header.Get("Referer") == "" {
		t.Error("map request is missing output format or site referrer")
	}
}

func restoredGeocodingClient(t *testing.T, base string) *Client {
	t.Helper()
	client := restoredClient(t, base)
	client.http.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == client.base.Host && req.URL.Path == "/qdgj/"+mapConfigPath {
			assertAuth(t, req, mapConfigPath)
			return mapJSONResponse(req, http.StatusOK, fixtureMapConfig), nil
		}
		if req.URL.Host == "apis.map.qq.com" {
			assertMapRequest(t, req)
			body, _ := json.Marshal(map[string]any{"status": 0, "result": map[string]string{"address": "fixture resolved address " + req.URL.Query().Get("location")}})
			return mapJSONResponse(req, http.StatusOK, string(body)), nil
		}
		return http.DefaultTransport.RoundTrip(req)
	})
	return client
}

func TestReverseGeocodeUsesSiteConfigAndIsolatesCredentials(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
		key    string
	}{
		{name: "task override", config: `{"code":200,"data":[{"csbz":"ZHXG_XTGL_DTKEY","csz":"wrong-key","zt":"1"},{"csbz":"ZHXG_XTGL_DTLX","csz":"1","zt":"1"},{"csbz":"ZHXG_QDGJ_DTKEY","csz":"fixture-map-key","zt":"1"},{"csbz":"ZHXG_QDGJ_DTLX","csz":"3","zt":"1"}]}`, key: "fixture-map-key"},
		{name: "global fallback", config: `{"code":200,"data":[{"csbz":"ZHXG_QDGJ_DTKEY","csz":"disabled-key","zt":"0"},{"csbz":"ZHXG_QDGJ_DTLX","csz":"1","zt":"0"},{"csbz":"ZHXG_XTGL_DTKEY","csz":"fallback-key","zt":"1"},{"csbz":"ZHXG_XTGL_DTLX","csz":"3","zt":"1"}]}`, key: "fallback-key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configs, lookups := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				configs++
				assertAuth(t, req, mapConfigPath)
				var names []string
				if err := json.NewDecoder(req.Body).Decode(&names); err != nil || len(names) != 4 {
					t.Errorf("invalid map configuration query: %v (%v)", names, err)
				}
				fmt.Fprint(w, tc.config)
			}))
			defer server.Close()
			client := restoredClient(t, server.URL)
			mapURL, _ := url.Parse("https://apis.map.qq.com/")
			client.http.Jar.SetCookies(mapURL, []*http.Cookie{{Name: "must-not-leak", Value: "fixture-secret"}})
			client.http.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Host == client.base.Host {
					return http.DefaultTransport.RoundTrip(req)
				}
				lookups++
				assertMapRequest(t, req)
				if req.URL.Query().Get("key") != tc.key || req.URL.Query().Get("location") != "20.5,10.5" {
					t.Error("wrong map key or latitude/longitude order")
				}
				return mapJSONResponse(req, http.StatusOK, `{"status":0,"result":{"address":" fixture coordinate address "}}`), nil
			})
			address, err := client.ReverseGeocode(context.Background(), 10.5, 20.5)
			if err != nil || address != "fixture coordinate address" || configs != 1 || lookups != 1 {
				t.Fatalf("reverse geocoding failed: address=%q configs=%d lookups=%d err=%v", address, configs, lookups, err)
			}
		})
	}
}

func TestReverseGeocodeRejectsInvalidCoordinatesBeforeNetwork(t *testing.T) {
	calls := 0
	client := restoredClient(t, "https://fixture.invalid")
	client.http.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected network request")
	})
	for _, loc := range []Location{{Longitude: math.NaN()}, {Latitude: math.Inf(1)}, {Longitude: 181}, {Longitude: -181}, {Latitude: 91}, {Latitude: -91}} {
		if _, err := client.ReverseGeocode(context.Background(), loc.Longitude, loc.Latitude); err == nil {
			t.Errorf("invalid coordinate accepted: %+v", loc)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid coordinates sent %d requests", calls)
	}
}

func TestReverseGeocodeFailuresDoNotSubmitOrLeakMapKey(t *testing.T) {
	for _, tc := range []struct {
		name       string
		config     string
		mapBody    string
		httpStatus int
		transport  error
	}{
		{name: "config failure", config: `{"code":503,"meta":{"message":"fixture unavailable"}}`},
		{name: "invalid config", config: `{"code":200,"data":{}}`},
		{name: "empty config", config: `{"code":200,"data":[]}`},
		{name: "unsupported map", config: `{"code":200,"data":[{"csbz":"ZHXG_QDGJ_DTLX","csz":"1","zt":"1"}]}`},
		{name: "missing key", config: `{"code":200,"data":[{"csbz":"ZHXG_QDGJ_DTLX","csz":"3","zt":"1"}]}`},
		{name: "http error", httpStatus: http.StatusForbidden, mapBody: `{"status":403}`},
		{name: "redirect", httpStatus: http.StatusTemporaryRedirect},
		{name: "map failure", mapBody: `{"status":311,"message":"invalid key fixture-map-key"}`},
		{name: "missing status", mapBody: `{"result":{"address":"fixture"}}`},
		{name: "null status", mapBody: `{"status":null,"result":{"address":"fixture"}}`},
		{name: "missing address", mapBody: `{"status":0,"result":{}}`},
		{name: "blank address", mapBody: `{"status":0,"result":{"address":" "}}`},
		{name: "invalid JSON", mapBody: `<html>fixture</html>`},
		{name: "oversized response", mapBody: strings.Repeat("x", maxResponseSize+1)},
		{name: "transport failure", transport: errors.New("fixture network failure")},
		{name: "canceled transport", transport: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests, lookups, submissions := 0, 0, 0
			client := restoredClient(t, "https://fixture.invalid")
			client.http.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				if req.URL.Host == client.base.Host && req.URL.Path == "/qdgj/"+mapConfigPath {
					body := tc.config
					if body == "" {
						body = fixtureMapConfig
					}
					return mapJSONResponse(req, http.StatusOK, body), nil
				}
				if req.URL.Host == "apis.map.qq.com" {
					lookups++
					assertMapRequest(t, req)
					if tc.transport != nil {
						return nil, tc.transport
					}
					status := tc.httpStatus
					if status == 0 {
						status = http.StatusOK
					}
					response := mapJSONResponse(req, status, tc.mapBody)
					response.Header.Set("Location", "https://redirect.invalid/")
					return response, nil
				}
				submissions++
				return mapJSONResponse(req, http.StatusOK, `{"code":200,"data":{}}`), nil
			})
			_, err := client.SignIn(context.Background(), SignInRequest{TaskID: "task", BatchID: "batch", Location: Location{Longitude: 10.5, Latitude: 20.5}})
			if err == nil || submissions != 0 || lookups > 1 || requests > 2 {
				t.Fatalf("lookup failure ignored or retried: requests=%d maps=%d submissions=%d err=%v", requests, lookups, submissions, err)
			}
			if strings.Contains(err.Error(), "fixture-map-key") || strings.Contains(err.Error(), "key=") {
				t.Fatal("map key leaked in error")
			}
			if tc.transport != nil && !errors.Is(err, tc.transport) {
				t.Errorf("transport error chain lost: %v", err)
			}
		})
	}
}
