package client

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// trackingBody wraps an io.ReadCloser and records whether Close was called.
type trackingBody struct {
	http.Response
	closed atomic.Bool
}

// roundTripperFunc allows using a plain function as http.RoundTripper.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func newTestApi(serverUrl string) *Api {
	return NewApiClient(serverUrl, Config{Token: "test-token"}, "", "test")
}

// TestJobservTailErrorNoNilDeref verifies that a network error from RawGet
// causes JobservTail to return cleanly instead of dereferencing a nil resp.
func TestJobservTailErrorNoNilDeref(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Close the connection immediately to force a client-side error.
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("server does not support hijacking")
		}
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	defer srv.Close()

	api := newTestApi(srv.URL)
	// Must not panic.
	api.JobservTail(srv.URL + "/test")
}

// TestJobservTailBodyClosed verifies that the response body is closed after
// each polling iteration.
func TestJobservTailBodyClosed(t *testing.T) {
	var closed atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return a non-200 to make JobservTail exit after one iteration.
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	api := newTestApi(srv.URL)
	// Wrap the transport to intercept the response body.
	orig := api.GetHttpClient().Transport
	if orig == nil {
		orig = http.DefaultTransport
	}
	api.GetHttpClient().Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		resp, err := orig.RoundTrip(req)
		if err != nil || resp == nil {
			return resp, err
		}
		origBody := resp.Body
		resp.Body = &closeTracker{ReadCloser: origBody, closed: &closed}

		return resp, nil
	})

	api.JobservTail(srv.URL + "/test")

	if !closed.Load() {
		t.Error("response body was not closed after JobservTail returned")
	}
}

// closeTracker wraps an io.ReadCloser and sets a flag when Close is called.
type closeTracker struct {
	io.ReadCloser
	closed *atomic.Bool
}

func (c *closeTracker) Close() error {
	c.closed.Store(true)
	return c.ReadCloser.Close()
}
