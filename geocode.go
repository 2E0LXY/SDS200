package main

// Address lookup for the scanner's location setting.
//
// The scanner stores a latitude, longitude and a range in miles (LCR). Typing
// coordinates by hand is awkward, so a UK house number and postcode are turned
// into coordinates here:
//
//	postcodes.io   - UK postcode to a coordinate. Free, no key, no rate limit
//	                 worth worrying about. Used on its own, and as the fallback.
//	Nominatim (OSM) - used when a house number is given, because it can place
//	                 the specific building rather than the postcode centroid.
//	                 Requires a real User-Agent and is rate-limited to roughly
//	                 one request a second, so it is only called when the house
//	                 number can actually improve the result.
//
// Both are called from the machine running this app, not from the scanner.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Overridable in tests.
var (
	postcodesEndpoint = "https://api.postcodes.io"
	nominatimEndpoint = "https://nominatim.openstreetmap.org"
)

const geocodeUserAgent = "SDS200-WebApp/" + "0.8 (+https://github.com/2E0LXY/SDS200)"

// GeoResult is one resolved address.
type GeoResult struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Label     string  `json:"label"`
	Source    string  `json:"source"`
	Precision string  `json:"precision"` // "building" or "postcode"
}

var ukPostcode = regexp.MustCompile(`^[A-Z]{1,2}[0-9][A-Z0-9]?\s*[0-9][A-Z]{2}$`)

// normalisePostcode upper-cases and spaces a UK postcode as "WF3 1AB".
func normalisePostcode(s string) (string, bool) {
	up := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	if len(up) < 5 || len(up) > 7 {
		return "", false
	}
	spaced := up[:len(up)-3] + " " + up[len(up)-3:]
	return spaced, ukPostcode.MatchString(spaced)
}

type geocoder struct {
	client *http.Client
	log    *AppLogger
}

func newGeocoder(log *AppLogger) *geocoder {
	return &geocoder{client: &http.Client{Timeout: 12 * time.Second}, log: log}
}

// Lookup resolves a house number (optional) and UK postcode to coordinates.
func (g *geocoder) Lookup(ctx context.Context, house, postcode string) (*GeoResult, error) {
	pc, ok := normalisePostcode(postcode)
	if !ok {
		return nil, fmt.Errorf("%q is not a UK postcode", strings.TrimSpace(postcode))
	}
	house = strings.TrimSpace(house)
	if house != "" {
		// Structured search first; a bare house number often needs the street
		// name, so fall back to a free-form query of "<house>, <postcode>".
		for _, free := range []bool{false, true} {
			r, err := g.nominatim(ctx, house, pc, free)
			if err == nil && r.Precision == "building" {
				return r, nil
			}
			if err != nil && g.log != nil {
				g.log.Debug("GEOCODE", "Building lookup failed", map[string]any{"free_form": free, "error": err.Error()})
			}
		}
	}
	return g.postcodesIO(ctx, pc)
}

func (g *geocoder) postcodesIO(ctx context.Context, pc string) (*GeoResult, error) {
	u := postcodesEndpoint + "/postcodes/" + url.PathEscape(pc)
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	res, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("postcode lookup: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode == 404 {
		return nil, fmt.Errorf("postcode %s was not found", pc)
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("postcode lookup returned HTTP %d", res.StatusCode)
	}
	var body struct {
		Result struct {
			Postcode  string   `json:"postcode"`
			Latitude  *float64 `json:"latitude"`
			Longitude *float64 `json:"longitude"`
			Ward      string   `json:"admin_ward"`
			District  string   `json:"admin_district"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("postcode lookup: bad JSON: %w", err)
	}
	if body.Result.Latitude == nil || body.Result.Longitude == nil {
		return nil, fmt.Errorf("postcode %s has no coordinates", pc)
	}
	label := strings.TrimSpace(strings.Join(nonEmpty(body.Result.Ward, body.Result.District, body.Result.Postcode), ", "))
	return &GeoResult{
		Latitude:  *body.Result.Latitude,
		Longitude: *body.Result.Longitude,
		Label:     label,
		Source:    "postcodes.io",
		Precision: "postcode",
	}, nil
}

func (g *geocoder) nominatim(ctx context.Context, house, pc string, freeForm bool) (*GeoResult, error) {
	q := url.Values{}
	q.Set("format", "jsonv2")
	if freeForm {
		q.Set("q", house+", "+pc+", United Kingdom")
	} else {
		q.Set("street", house)
		q.Set("postalcode", pc)
	}
	q.Set("countrycodes", "gb")
	q.Set("addressdetails", "1")
	q.Set("limit", "1")
	req, err := http.NewRequestWithContext(ctx, "GET", nominatimEndpoint+"/search?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	// Nominatim's usage policy requires an identifying User-Agent.
	req.Header.Set("User-Agent", geocodeUserAgent)
	req.Header.Set("Accept", "application/json")
	res, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("address lookup: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("address lookup returned HTTP %d", res.StatusCode)
	}
	var hits []struct {
		Lat         string `json:"lat"`
		Lon         string `json:"lon"`
		DisplayName string `json:"display_name"`
		Address     struct {
			HouseNumber string `json:"house_number"`
		} `json:"address"`
	}
	if err := json.Unmarshal(raw, &hits); err != nil {
		return nil, fmt.Errorf("address lookup: bad JSON: %w", err)
	}
	if len(hits) == 0 {
		return nil, fmt.Errorf("no match for %s, %s", house, pc)
	}
	var lat, lon float64
	if _, err := fmt.Sscanf(hits[0].Lat, "%g", &lat); err != nil {
		return nil, fmt.Errorf("address lookup: bad latitude %q", hits[0].Lat)
	}
	if _, err := fmt.Sscanf(hits[0].Lon, "%g", &lon); err != nil {
		return nil, fmt.Errorf("address lookup: bad longitude %q", hits[0].Lon)
	}
	precision := "postcode"
	if hits[0].Address.HouseNumber != "" {
		precision = "building"
	}
	label := hits[0].DisplayName
	if len(label) > 120 {
		label = label[:120] + "…"
	}
	return &GeoResult{Latitude: lat, Longitude: lon, Label: label, Source: "openstreetmap", Precision: precision}, nil
}

func nonEmpty(xs ...string) []string {
	out := []string{}
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			out = append(out, strings.TrimSpace(x))
		}
	}
	return out
}
