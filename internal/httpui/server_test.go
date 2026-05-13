package httpui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"pwled/internal/config"
)

func newTestServer() (*httptest.Server, *config.Store, *int) {
	persisted := 0
	st := config.NewStore(config.Default(), func(c config.Config) error {
		persisted++
		return nil
	})
	srv := New(st)
	return httptest.NewServer(srv), st, &persisted
}

func TestGetConfigReturnsCurrent(t *testing.T) {
	ts, st, _ := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/config")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got config.Config
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(got, *st.Load()) {
		t.Errorf("returned config differs from store")
	}
}

func TestPutConfigAppliesAndPersists(t *testing.T) {
	ts, st, persisted := newTestServer()
	defer ts.Close()

	next := config.Default()
	next.AGC.Mode = config.AGCVivid
	next.Beat.MinIntervalMs = 400
	body, _ := json.Marshal(next)

	req, _ := http.NewRequest("PUT", ts.URL+"/api/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if got := st.Load(); got.AGC.Mode != config.AGCVivid || got.Beat.MinIntervalMs != 400 {
		t.Errorf("store not updated: %+v", got)
	}
	if *persisted != 1 {
		t.Errorf("expected 1 persist call, got %d", *persisted)
	}
}

func TestPutConfigRejectsInvalid(t *testing.T) {
	ts, st, persisted := newTestServer()
	defer ts.Close()
	prev := *st.Load()

	bad := config.Default()
	bad.AGC.Mode = "garbage"
	body, _ := json.Marshal(bad)

	req, _ := http.NewRequest("PUT", ts.URL+"/api/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 422 {
		t.Fatalf("expected 422, got %d", resp.StatusCode)
	}
	var body2 struct {
		Error  string            `json:"error"`
		Fields map[string]string `json:"fields"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body2.Fields["agc.mode"] == "" {
		t.Errorf("expected agc.mode error, got %v", body2.Fields)
	}
	if !reflect.DeepEqual(*st.Load(), prev) {
		t.Error("invalid PUT must not mutate store")
	}
	if *persisted != 0 {
		t.Errorf("invalid PUT must not persist; calls=%d", *persisted)
	}
}

func TestResetConfig(t *testing.T) {
	ts, st, _ := newTestServer()
	defer ts.Close()

	// Mutate first.
	mod := config.Default()
	mod.AGC.Mode = config.AGCLazy
	if err := st.Apply(mod); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, err := http.Post(ts.URL+"/api/config/reset", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if !reflect.DeepEqual(*st.Load(), config.Default()) {
		t.Errorf("store not reset: %+v", st.Load())
	}
}

// TestGetConfigUsesSnakeCaseKeys pins the wire format the JS panel reads:
// keys must match the snake_case the schema in index.html addresses.
func TestGetConfigUsesSnakeCaseKeys(t *testing.T) {
	ts, _, _ := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/config")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	wantTop := []string{"squelch", "gain", "agc", "bands", "limiter", "smoothing", "beat", "watchdog", "presets"}
	for _, k := range wantTop {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing top-level key %q in %v", k, keys(raw))
		}
	}
	agc, _ := raw["agc"].(map[string]any)
	for _, k := range []string{"enabled", "mode", "target_peak", "release_seconds"} {
		if _, ok := agc[k]; !ok {
			t.Errorf("missing agc key %q in %v", k, keys(agc))
		}
	}
	smoothing, _ := raw["smoothing"].(map[string]any)
	for _, k := range []string{"sample_attack_ms", "sample_release_ms", "band_attack_ms", "band_release_ms"} {
		if _, ok := smoothing[k]; !ok {
			t.Errorf("missing smoothing key %q in %v", k, keys(smoothing))
		}
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestPutConfigRejectsUnknownFields(t *testing.T) {
	ts, _, _ := newTestServer()
	defer ts.Close()

	body := []byte(`{"squelch":{"enabled":true,"threshold":0.005},"bogus":1}`)
	req, _ := http.NewRequest("PUT", ts.URL+"/api/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for unknown field, got %d", resp.StatusCode)
	}
}
