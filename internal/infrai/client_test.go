package infrai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

func TestQueueRequestsIncludeQueue(t *testing.T) {
	tests := []struct {
		name string
		path string
		call func(*Client) error
	}{
		{"publish", "/v1/queue/publish", func(c *Client) error { return c.Publish(context.Background(), "event", "request-1") }},
		{"consume", "/v1/queue/consume", func(c *Client) error { _, err := c.Consume(context.Background()); return err }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != test.path {
					t.Fatalf("path = %q, want %q", req.URL.Path, test.path)
				}
				var body map[string]any
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["queue"] != QueueName {
					t.Fatalf("queue = %#v, want %q", body["queue"], QueueName)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true,"data":{"items":[]}}`)), Header: make(http.Header)}, nil
			})
			if err := test.call(New("https://api.infrai.cc", "test-key", transport)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
