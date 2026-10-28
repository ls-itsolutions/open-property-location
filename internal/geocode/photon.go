// Package geocode resolves free-text addresses with a Photon instance.
package geocode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"
)

const (
	requestTimeout  = 10 * time.Second
	maxResponseSize = 1 << 20
	userAgent       = "open-property-location (+https://github.com/ls-itsolutions/open-property-location)"
)

// ErrNotFound is returned when Photon has no result for the address.
var ErrNotFound = errors.New("address not found")

type Client struct {
	endpoint *url.URL
	http     *http.Client
}

// New returns a client for the Photon instance at baseURL,
// for example https://photon.komoot.io.
func New(baseURL string) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" {
		return nil, fmt.Errorf("invalid Photon URL %q", baseURL)
	}
	return &Client{
		endpoint: u.JoinPath("api"),
		http:     &http.Client{Timeout: requestTimeout},
	}, nil
}

// Geocode returns the coordinates of the best match for address.
func (c *Client) Geocode(ctx context.Context, address string) (lat, lon float64, err error) {
	u := *c.endpoint
	u.RawQuery = url.Values{"q": {address}, "limit": {"1"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// Unwrap the *url.Error so the address in the URL does not end up in logs.
		return 0, 0, fmt.Errorf("photon request: %w", errors.Unwrap(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("photon returned status %d", resp.StatusCode)
	}

	var body struct {
		Features []struct {
			Geometry struct {
				Coordinates []float64 `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseSize)).Decode(&body); err != nil {
		return 0, 0, fmt.Errorf("decode photon response: %w", err)
	}
	if len(body.Features) == 0 {
		return 0, 0, ErrNotFound
	}
	coords := body.Features[0].Geometry.Coordinates
	if len(coords) < 2 || math.Abs(coords[1]) > 90 || math.Abs(coords[0]) > 180 {
		return 0, 0, errors.New("photon returned invalid coordinates")
	}
	return coords[1], coords[0], nil
}
