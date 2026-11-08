package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeTool records calls so the loop can be checked without a scanner.
func fakeTool(calls *[]map[string]any) AITool {
	return AITool{
		Name:        "set_volume",
		Description: "set volume",
		Parameters:  schema(map[string]any{"level": intProp("0-29", 0, 29)}, "level"),
		Writes:      true,
		Run: func(args map[string]any) (any, error) {
			*calls = append(*calls, args)
			return map[string]any{"volume": args["level"]}, nil
		},
	}
}

func runnerFor(t *testing.T, cfg AIConfig, tools []AITool) *aiRunner {
	t.Helper()
	lg, err := NewAppLogger(t.TempDir(), "ERROR")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lg.Close)
	return newAIRunner(cfg, tools, lg)
}

func TestGoogleToolLoop(t *testing.T) {
	var calls []map[string]any
	turn := 0
	var seen []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "G-KEY" {
			t.Errorf("missing API key header: %v", r.Header)
		}
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		seen = append(seen, body)
		turn++
		if turn == 1 {
			// first turn: ask for the tool, with a Gemini 3 thought signature
			w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"set_volume","args":{"level":5}},"thoughtSignature":"SIG1"}]}}]}`))
			return
		}
		w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"Volume is now 5."}]}}]}`))
	}))
	defer srv.Close()
	old := googleEndpointForTest
	googleEndpointForTest = srv.URL
	defer func() { googleEndpointForTest = old }()

	r := runnerFor(t, AIConfig{GoogleAPIKey: "G-KEY", GoogleModel: "test-model"}, []AITool{fakeTool(&calls)})
	res, err := r.Run(context.Background(), "set volume to 5", "sys")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Provider != "google" || res.Reply != "Volume is now 5." {
		t.Fatalf("unexpected result %+v", res)
	}
	if len(calls) != 1 || calls[0]["level"].(float64) != 5 {
		t.Fatalf("tool not called correctly: %v", calls)
	}
	if len(res.Steps) != 1 || res.Steps[0].Tool != "set_volume" || res.Steps[0].Error != "" {
		t.Fatalf("steps wrong: %+v", res.Steps)
	}
	// The second request must echo the thought signature and the result.
	b, _ := json.Marshal(seen[1])
	if !strings.Contains(string(b), "SIG1") {
		t.Fatalf("thought signature not echoed: %s", b)
	}
	if !strings.Contains(string(b), "functionResponse") {
		t.Fatalf("function result not sent back: %s", b)
	}
}

func TestFallbackToOpenRouterWhenGoogleFails(t *testing.T) {
	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"message":"quota"}}`))
	}))
	defer google.Close()
	var orBody map[string]any
	openrouter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer OR-KEY" {
			t.Errorf("missing bearer: %v", r.Header)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &orBody)
		w.Write([]byte(`{"choices":[{"message":{"content":"Squelch is 2."}}]}`))
	}))
	defer openrouter.Close()
	og, oo := googleEndpointForTest, openRouterEndpointForTest
	googleEndpointForTest, openRouterEndpointForTest = google.URL, openrouter.URL
	defer func() { googleEndpointForTest, openRouterEndpointForTest = og, oo }()

	r := runnerFor(t, AIConfig{GoogleAPIKey: "G", OpenRouterAPIKey: "OR-KEY"}, []AITool{})
	res, err := r.Run(context.Background(), "what is the squelch", "sys")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Provider != "openrouter" || res.Reply != "Squelch is 2." {
		t.Fatalf("expected OpenRouter answer, got %+v", res)
	}
	if res.Fallback == "" || !strings.Contains(res.Fallback, "google") {
		t.Fatalf("fallback not reported: %q", res.Fallback)
	}
	if orBody["model"] != defaultOpenRouterModel {
		t.Fatalf("model not sent: %v", orBody["model"])
	}
}

func TestOpenRouterToolCallParsing(t *testing.T) {
	var calls []map[string]any
	turn := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		turn++
		if turn == 1 {
			w.Write([]byte(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"set_volume","arguments":"{\"level\":12}"}}]}}]}`))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), `"tool_call_id":"call_1"`) {
			t.Errorf("tool result not linked to call id: %s", raw)
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"Done."}}]}`))
	}))
	defer srv.Close()
	oo := openRouterEndpointForTest
	openRouterEndpointForTest = srv.URL
	defer func() { openRouterEndpointForTest = oo }()

	r := runnerFor(t, AIConfig{OpenRouterAPIKey: "K"}, []AITool{fakeTool(&calls)})
	res, err := r.Run(context.Background(), "volume 12", "sys")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(calls) != 1 || calls[0]["level"].(float64) != 12 || res.Reply != "Done." {
		t.Fatalf("unexpected: calls=%v res=%+v", calls, res)
	}
}

func TestNoProviderConfigured(t *testing.T) {
	r := runnerFor(t, AIConfig{}, []AITool{})
	if _, err := r.Run(context.Background(), "hello", "sys"); err == nil {
		t.Fatal("expected an error when no provider is configured")
	}
}

func TestToolErrorsAreReportedToModel(t *testing.T) {
	failing := AITool{
		Name: "set_volume", Description: "x", Parameters: schema(map[string]any{}),
		Run: func(map[string]any) (any, error) { return nil, io.ErrUnexpectedEOF },
	}
	turn := 0
	var second string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		turn++
		if turn == 1 {
			w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"c1","type":"function","function":{"name":"set_volume","arguments":"{}"}}]}}]}`))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		second = string(raw)
		w.Write([]byte(`{"choices":[{"message":{"content":"The scanner did not answer."}}]}`))
	}))
	defer srv.Close()
	oo := openRouterEndpointForTest
	openRouterEndpointForTest = srv.URL
	defer func() { openRouterEndpointForTest = oo }()

	r := runnerFor(t, AIConfig{OpenRouterAPIKey: "K"}, []AITool{failing})
	res, err := r.Run(context.Background(), "set volume", "sys")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Steps[0].Error == "" {
		t.Fatal("tool error not recorded in steps")
	}
	if !strings.Contains(second, "unexpected EOF") {
		t.Fatalf("tool error not passed to the model: %s", second)
	}
}

func TestToolSchemasAreWellFormed(t *testing.T) {
	a := &App{}
	names := map[string]bool{}
	for _, tl := range a.aiTools() {
		if tl.Name == "" || tl.Description == "" || tl.Run == nil {
			t.Fatalf("incomplete tool %+v", tl.Name)
		}
		if names[tl.Name] {
			t.Fatalf("duplicate tool %s", tl.Name)
		}
		names[tl.Name] = true
		if tl.Parameters["type"] != "object" {
			t.Fatalf("%s: parameters must be an object schema", tl.Name)
		}
		if _, err := json.Marshal(tl.Parameters); err != nil {
			t.Fatalf("%s: schema not serialisable: %v", tl.Name, err)
		}
		props, _ := tl.Parameters["properties"].(map[string]any)
		for _, req := range toStrings(tl.Parameters["required"]) {
			if _, ok := props[req]; !ok {
				t.Fatalf("%s: required field %q is not in properties", tl.Name, req)
			}
		}
	}
	for _, want := range []string{"get_status", "set_volume", "set_hold", "tune_frequency", "set_range", "browse_memory", "waterfall"} {
		if !names[want] {
			t.Fatalf("tool %s missing", want)
		}
	}
}

func toStrings(v any) []string {
	out := []string{}
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		for _, e := range x {
			out = append(out, e.(string))
		}
	}
	return out
}

func TestServiceTypeMatching(t *testing.T) {
	all := []serviceType{}
	for i := 0; i < 47; i++ {
		name := ""
		if i < 37 {
			name = presetServiceTypes[i]
		} else {
			name = "Custom " + string(rune('0'+i-36))
		}
		all = append(all, serviceType{Slot: i, Name: name})
	}
	nameOf := func(idx []int) []string {
		out := []string{}
		for _, i := range idx {
			out = append(out, all[i].Name)
		}
		return out
	}
	cases := map[string][]string{
		"Fire Dispatch":     {"Fire Dispatch"},
		"fire dispatch":     {"Fire Dispatch"},
		"Civilian Aircraft": {"Aircraft"},
		"airband":           {"Aircraft"},
		"police":            {"Law Dispatch", "Law Tac", "Law Talk"},
		"ambulance":         {"EMS Dispatch", "EMS-Tac", "EMS-Talk"},
		"railway":           {"Railroad"},
		"council":           {"Public Works"},
	}
	for in, want := range cases {
		idx, err := matchServiceTypes(in, all)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		got := nameOf(idx)
		if len(got) != len(want) {
			t.Fatalf("%q: got %v want %v", in, got, want)
		}
		for _, w := range want {
			found := false
			for _, g := range got {
				if g == w {
					found = true
				}
			}
			if !found {
				t.Fatalf("%q: got %v want %v", in, got, want)
			}
		}
	}
	if idx, err := matchServiceTypes("fire", all); err != nil || len(idx) != 3 {
		t.Fatalf("partial match: %v %v", idx, err)
	}
	if _, err := matchServiceTypes("submarines", all); err == nil || !strings.Contains(err.Error(), "Aircraft") {
		t.Fatalf("unknown name should list the real ones, got %v", err)
	}
}

func TestNormaliseMenuText(t *testing.T) {
	cases := map[string]string{
		"  Set Scan Selection ":        "set scan selection",
		"Aircraft                :---": "aircraft",
		"Srch/CloCall Opt":             "srch/clocall opt",
	}
	for in, want := range cases {
		if got := normaliseMenuText(in); got != want {
			t.Fatalf("%q -> %q want %q", in, got, want)
		}
	}
}
