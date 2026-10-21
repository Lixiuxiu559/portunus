package protocol

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchUpstreamModelsCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := FetchUpstreamModels(ctx, ProviderOpenAI, srv.URL, "secret")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestFetchUpstreamModelsSafeErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"status", 401, "super-secret"},
		{"invalid-json", 200, "super-secret"},
		{"too-large", 200, strings.Repeat("x", maxModelResponseBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); io.WriteString(w, tc.body) }))
			defer srv.Close()
			_, err := FetchUpstreamModels(context.Background(), ProviderGemini, srv.URL, "super-secret")
			if err == nil || strings.Contains(err.Error(), "super-secret") || strings.Contains(err.Error(), srv.URL) {
				t.Fatalf("unexpected error %v", err)
			}
		})
	}
	_, err := FetchUpstreamModels(context.Background(), ProviderGemini, ":bad%super-secret", "super-secret")
	if err == nil || strings.Contains(err.Error(), "super-secret") {
		t.Fatal(err)
	}
}

func TestFetchUpstreamModelsEscapesKeyAndRejectsRedirect(t *testing.T) {
	var redirected bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true }))
	defer destination.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "a&b=c?d" {
			t.Errorf("key incorrectly encoded: %s", r.URL.RawQuery)
		}
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	_, err := FetchUpstreamModels(context.Background(), ProviderGemini, srv.URL, "a&b=c?d")
	if err == nil || redirected {
		t.Fatalf("redirect followed: %v", err)
	}
}
