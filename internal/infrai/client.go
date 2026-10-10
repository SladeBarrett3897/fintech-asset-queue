package infrai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Client struct {
	baseURL string
	key     string
	http    HTTPDoer
	sleep   func(time.Duration)
}

type Error struct {
	Code   string
	Status int
	Detail any
}

func (e *Error) Error() string { return fmt.Sprintf("Infrai request rejected: %s", e.Code) }

type envelope struct {
	OK       bool            `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Error    json.RawMessage `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type QueueMessage struct {
	MessageID string          `json:"message_id"`
	Payload   json.RawMessage `json:"payload"`
}

const QueueName = "payment-asset-processing"

func New(baseURL, key string, transport HTTPDoer) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), key: key, http: transport, sleep: time.Sleep}
}

func (c *Client) CreateBucket(ctx context.Context, name string) error {
	return c.call(ctx, "POST", "/v1/storage/bucket/create", map[string]any{"name": name}, "bucket:"+name, nil)
}

func (c *Client) PresignGet(ctx context.Context, bucket, key, requestID string) (string, error) {
	var data struct {
		URL string `json:"url"`
	}
	path := "/v1/storage/object/presign/" + url.PathEscape(bucket) + "/" + url.PathEscape(key)
	err := c.call(ctx, "POST", path, map[string]any{"op": "get", "expires_seconds": 900, "idempotency_key": requestID}, requestID, &data)
	return data.URL, err
}

func (c *Client) PresignPut(ctx context.Context, bucket, key, contentType, requestID string) (string, error) {
	var data struct {
		URL string `json:"url"`
	}
	path := "/v1/storage/object/presign/" + url.PathEscape(bucket) + "/" + url.PathEscape(key)
	err := c.call(ctx, "POST", path, map[string]any{
		"op": "put", "expires_seconds": 900, "content_type": contentType,
		"max_bytes": 10 * 1024 * 1024, "idempotency_key": requestID,
	}, requestID, &data)
	return data.URL, err
}

func (c *Client) Publish(ctx context.Context, payload any, requestID string) error {
	// Capability idiom: infrai.queue.publish
	return c.call(ctx, "POST", "/v1/queue/publish", map[string]any{"queue": QueueName, "payload": payload}, requestID, nil)
}

func (c *Client) Consume(ctx context.Context) ([]QueueMessage, error) {
	var data struct {
		Items []QueueMessage `json:"items"`
	}
	err := c.call(ctx, "POST", "/v1/queue/consume", map[string]any{"queue": QueueName, "max_messages": 10, "visibility_timeout": 120}, "", &data)
	return data.Items, err
}

func (c *Client) ProcessImage(ctx context.Context, imageURL, requestID string) (map[string]any, error) {
	body := map[string]any{
		"image":  imageURL,
		"ops":    []any{map[string]any{"op": "resize", "params": map[string]any{"width": 1600, "height": 1600, "fit": "inside"}}},
		"format": "webp", "store": true, "idempotency_key": requestID,
	}
	var data map[string]any
	err := c.call(ctx, "POST", "/v1/image/process", body, requestID, &data)
	return data, err
}

func (c *Client) call(ctx context.Context, method, path string, body any, requestID string, out any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(encoded))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
		req.Header.Set("Content-Type", "application/json")
		if requestID != "" {
			req.Header.Set("Idempotency-Key", requestID)
		}
		res, err := c.http.Do(req)
		if err != nil {
			return err
		}
		raw, readErr := io.ReadAll(res.Body)
		res.Body.Close()
		if readErr != nil {
			return readErr
		}

		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return fmt.Errorf("decode Infrai envelope: %w", err)
		}
		if res.StatusCode == http.StatusTooManyRequests && attempt < 3 {
			c.sleep(retryDelay(res.Header.Get("Retry-After"), attempt))
			continue
		}
		if res.StatusCode >= 500 {
			return fmt.Errorf("Infrai transport status %d", res.StatusCode)
		}
		if !env.OK {
			var detail apiError
			_ = json.Unmarshal(env.Error, &detail)
			return &Error{Code: detail.Code, Status: res.StatusCode, Detail: json.RawMessage(env.Error)}
		}
		if out != nil {
			return json.Unmarshal(env.Data, out)
		}
		return nil
	}
	return fmt.Errorf("request retry budget exhausted")
}

func retryDelay(header string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(header); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return time.Duration(1<<attempt) * 500 * time.Millisecond
}
