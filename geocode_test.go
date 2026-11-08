package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalisePostcode(t *testing.T) {
	cases := map[string]string{
		"wf31ab":   "WF3 1AB",
		"WF3 1AB":  "WF3 1AB",
		" ls1 4dy": "LS1 4DY",
		"sw1a2aa":  "SW1A 2AA",
	}
	for in, want := range cases {
		got, ok := normalisePostcode(in)
		if !ok || got != want {
			t.Fatalf("%q -> %q,%v want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "12345", "NOTAPOSTCODE", "W1"} {
		if _, ok := normalisePostcode(bad); ok {
			t.Fatalf("%q should be rejected", bad)
		}
	}
}

func testGeocoder(t *testing.T) *geocoder {
	t.Helper()
	lg, err := NewAppLogger(t.TempDir(), "ERROR")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lg.Close)
	return newGeocoder(lg)
}

func TestPostcodeLookup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/postcodes/WF3%201AB") && !strings.HasSuffix(r.URL.Path, "/postcodes/WF3 1AB") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Write([]byte(`{"status":200,"result":{"postcode":"WF3 1AB","latitude":53.7264,"longitude":-1.5744,"admin_ward":"Ardsley","admin_district":"Wakefield"}}`))
	}))
	defer srv.Close()
	old := postcodesEndpoint
	postcodesEndpoint = srv.URL
	defer func() { postcodesEndpoint = old }()

	g := testGeocoder(t)
	res, err := g.Lookup(context.Background(), "", "wf3 1ab")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if res.Latitude != 53.7264 || res.Longitude != -1.5744 {
		t.Fatalf("wrong coordinates: %+v", res)
	}
	if res.Precision != "postcode" || res.Source != "postcodes.io" {
		t.Fatalf("wrong provenance: %+v", res)
	}
}

func TestHouseNumberUsesOpenStreetMapThenFallsBack(t *testing.T) {
	var sawUA, sawStreet string
	osm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawUA = r.Header.Get("User-Agent")
		sawStreet = r.URL.Query().Get("street")
		w.Write([]byte(`[{"lat":"53.726412","lon":"-1.574399","display_name":"12, Example Street, Wakefield","address":{"house_number":"12"}}]`))
	}))
	defer osm.Close()
	pc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":{"postcode":"WF3 1AB","latitude":53.7264,"longitude":-1.5744}}`))
	}))
	defer pc.Close()
	on, op := nominatimEndpoint, postcodesEndpoint
	nominatimEndpoint, postcodesEndpoint = osm.URL, pc.URL
	defer func() { nominatimEndpoint, postcodesEndpoint = on, op }()

	g := testGeocoder(t)
	res, err := g.Lookup(context.Background(), "12", "WF3 1AB")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if res.Precision != "building" || res.Latitude != 53.726412 {
		t.Fatalf("expected the building match: %+v", res)
	}
	if !strings.Contains(sawUA, "SDS200") {
		t.Fatalf("Nominatim needs an identifying User-Agent, got %q", sawUA)
	}
	if sawStreet != "12" {
		t.Fatalf("house number not sent: %q", sawStreet)
	}
	_ = sawStreet

	// With OpenStreetMap failing, the postcode centre is still returned.
	nominatimEndpoint = "http://127.0.0.1:1"
	res, err = g.Lookup(context.Background(), "12", "WF3 1AB")
	if err != nil {
		t.Fatalf("fallback: %v", err)
	}
	if res.Precision != "postcode" || res.Source != "postcodes.io" {
		t.Fatalf("expected the postcode fallback: %+v", res)
	}
}

func TestUnknownPostcodeIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"status":404,"error":"Postcode not found"}`))
	}))
	defer srv.Close()
	old := postcodesEndpoint
	postcodesEndpoint = srv.URL
	defer func() { postcodesEndpoint = old }()

	if _, err := testGeocoder(t).Lookup(context.Background(), "", "WF3 1AB"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected a not-found error, got %v", err)
	}
	if _, err := testGeocoder(t).Lookup(context.Background(), "", "banana"); err == nil {
		t.Fatal("expected a rejection for a non-postcode")
	}
}
