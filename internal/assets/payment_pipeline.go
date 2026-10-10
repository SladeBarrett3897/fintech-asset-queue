package assets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/example/fintech-asset-queue/internal/infrai"
)

const BucketName = "fintech-payment-assets"

type PaymentAsset struct {
	EventID   string `json:"event_id"`
	PaymentID string `json:"payment_id"`
	ObjectKey string `json:"object_key"`
	RiskScore int    `json:"risk_score"`
	Kind      string `json:"kind"`
}

type Decision struct {
	Action       string `json:"action"`
	Notification string `json:"notification"`
}

type AuditEvent struct {
	EventID      string         `json:"event_id"`
	PaymentID    string         `json:"payment_id"`
	Action       string         `json:"action"`
	Notification string         `json:"notification"`
	OccurredAt   string         `json:"occurred_at"`
	Output       map[string]any `json:"output,omitempty"`
}

type Gateway interface {
	Publish(context.Context, any, string) error
	Consume(context.Context) ([]infrai.QueueMessage, error)
	PresignGet(context.Context, string, string, string) (string, error)
	PresignPut(context.Context, string, string, string, string) (string, error)
	ProcessImage(context.Context, string, string) (map[string]any, error)
}

func Decide(asset PaymentAsset) Decision {
	if asset.RiskScore >= 70 {
		return Decision{Action: "manual_review", Notification: "risk_team_asset_ready"}
	}
	return Decision{Action: "process", Notification: "payment_asset_processing_queued"}
}

type Handler struct{ gateway Gateway }

func NewHandler(gateway Gateway) http.Handler {
	h := &Handler{gateway: gateway}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /payment-assets/upload-url", h.uploadURL)
	mux.HandleFunc("POST /payment-assets/complete", h.complete)
	return mux
}

func (h *Handler) uploadURL(w http.ResponseWriter, r *http.Request) {
	var input struct {
		EventID     string `json:"event_id"`
		ObjectKey   string `json:"object_key"`
		ContentType string `json:"content_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.EventID == "" || input.ObjectKey == "" || input.ContentType == "" {
		http.Error(w, "invalid upload request", http.StatusBadRequest)
		return
	}
	uploadURL, err := h.gateway.PresignPut(r.Context(), BucketName, input.ObjectKey, input.ContentType, "upload:"+input.EventID)
	if err != nil {
		writeGatewayError(w, err, "upload signing failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"upload_url": uploadURL, "method": "PUT", "object_key": input.ObjectKey})
}

func (h *Handler) complete(w http.ResponseWriter, r *http.Request) {
	var asset PaymentAsset
	if err := json.NewDecoder(r.Body).Decode(&asset); err != nil || asset.EventID == "" || asset.PaymentID == "" || asset.ObjectKey == "" {
		http.Error(w, "invalid payment asset event", http.StatusBadRequest)
		return
	}
	decision := Decide(asset)
	payload := struct {
		PaymentAsset
		Decision Decision `json:"decision"`
	}{asset, decision}
	if err := h.gateway.Publish(r.Context(), payload, "payment-asset:"+asset.EventID); err != nil {
		writeGatewayError(w, err, "asset enqueue failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "queued", "event_id": asset.EventID, "decision": decision})
}

func writeGatewayError(w http.ResponseWriter, err error, fallback string) {
	var apiErr *infrai.Error
	if errors.As(err, &apiErr) && apiErr.Status >= 400 && apiErr.Status < 500 {
		http.Error(w, apiErr.Code, apiErr.Status)
		return
	}
	http.Error(w, fallback, http.StatusBadGateway)
}

func RunWorker(ctx context.Context, gateway Gateway) ([]AuditEvent, error) {
	messages, err := gateway.Consume(ctx)
	if err != nil {
		return nil, err
	}
	results := make([]AuditEvent, 0, len(messages))
	for _, message := range messages {
		var job struct {
			PaymentAsset
			Decision Decision `json:"decision"`
		}
		if err := json.Unmarshal(message.Payload, &job); err != nil {
			return nil, fmt.Errorf("decode queue message: %w", err)
		}
		audit := AuditEvent{EventID: job.EventID, PaymentID: job.PaymentID, Action: job.Decision.Action, Notification: job.Decision.Notification, OccurredAt: time.Now().UTC().Format(time.RFC3339)}
		if job.Decision.Action == "process" {
			// Storage signs the source and image processing fetches it directly; this service never copies the bytes.
			source, err := gateway.PresignGet(ctx, BucketName, job.ObjectKey, "source:"+job.EventID)
			if err != nil {
				return nil, err
			}
			audit.Output, err = gateway.ProcessImage(ctx, source, "render:"+job.EventID)
			if err != nil {
				return nil, err
			}
		}
		results = append(results, audit)
	}
	return results, nil
}
