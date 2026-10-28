package geocode

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewRejectsInvalidURL(t *testing.T) {
	for _, u := range []string{"", "photon.komoot.io", "ftp://photon.example", "https://", "https://photon.example/?x=1"} {
		if _, err := New(u); err == nil {
			t.Errorf("New(%q): expected error", u)
		}
	}
}

func TestGeocode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/photon/api" || r.URL.Query().Get("limit") != "1" {
			t.Errorf("unexpected request %s", r.URL)
		}
		switch r.URL.Query().Get("q") {
		case "Alexanderplatz & Co":
			w.Write([]byte(`{"features":[{"geometry":{"coordinates":[13.4132,52.5219]}}]}`))
		case "invalid":
			w.Write([]byte(`{"features":[{"geometry":{"coordinates":[200,52]}}]}`))
		default:
			w.Write([]byte(`{"features":[]}`))
		}
	}))
	defer srv.Close()

	c, err := New(srv.URL + "/photon/")
	if err != nil {
		t.Fatal(err)
	}
	lat, lon, err := c.Geocode(context.Background(), "Alexanderplatz & Co")
	if err != nil || lat != 52.5219 || lon != 13.4132 {
		t.Errorf("got %v, %v, %v", lat, lon, err)
	}
	if _, _, err := c.Geocode(context.Background(), "nothing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if _, _, err := c.Geocode(context.Background(), "invalid"); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want invalid coordinates", err)
	}
}
