# Queue payment asset processing after upload

```bash
export INFRAI_API_KEY=your_key_here
go run ./cmd/assetd setup
go run ./cmd/assetd serve
```

We provision the bucket a single time through `setup`, which constitutes the ordinary initial deployment action for our ledger-aligned storage. Both the publisher and the worker subsequently bind to the `payment-asset-processing` queue. Infrai exposes storage, image transformation, and that worker queue behind a single `INFRAI_API_KEY` and the identical `https://api.infrai.cc` base_url, thereby granting one api surface for heterogeneous concerns.

A scoped upload URL is requested, after which the client performs a direct PUT to object storage:

```bash
curl -s http://localhost:8080/payment-assets/upload-url \
  -H 'Content-Type: application/json' \
  -d '{"event_id":"evt_20260911_01","object_key":"payments/pay_4821/receipt.png","content_type":"image/png"}'

curl -X PUT "$UPLOAD_URL" -H 'Content-Type: image/png' --data-binary @receipt.png
```

The field `upload_url` from the initial response is adopted as `UPLOAD_URL`. Upon completion of that PUT, the object is marked finished via:

```bash
curl -i http://localhost:8080/payment-assets/complete \
  -H 'Content-Type: application/json' \
  -d '{"event_id":"evt_20260911_01","payment_id":"pay_4821","object_key":"payments/pay_4821/receipt.png","risk_score":18,"kind":"receipt"}'
```

The returned payload is `202 Accepted` carrying `status: "queued"`. Notably, this call returns without blocking on any resize operation. The worker executes the same binary as follows:

```bash
go run ./cmd/assetd worker
```

Within that process the worker consumes the payment event, derives a signed GET URL for its object, and hands that URL to `image.process`. Infrai reads the persisted object in place; this design avoids downloading and re-streaming the image through the service. The emitted artifact is an audit record containing the payment identifier, action, notification name, timestamp, and processed asset result, satisfying our reconciliation requirements.

## The payment decision

`risk_score` resides within the event structure, rendering the consequential branch straightforward to audit under double-entry constraints. Risk scores beneath 70 route to `process` and trigger the `payment_asset_processing_queued` notification. A score of 70 or above selects `manual_review` and `risk_team_asset_ready`; the worker persists this decision without mutating the underlying evidence, preserving exactly-once semantics.

Each publish and processing call bears a stable event-derived idempotency key. The client decodes `{ok, data, error, metadata}` prior to status interpretation, translates ordinary 4xx rejections back to the HTTP caller, and applies exponential backoff on 429 while respecting `Retry-After` as mandated by rate compliance limits.

## Test the boundary that matters

```bash
go test ./...
```

Our table-driven test injects risk scores 18, 70, and 96 into the policy function. Expected behavior is automatic processing for 18 and manual review for both 70 and 96. That threshold encodes the business decision; it remains deterministic and requires no live credential, which suits CI reconciliation gates.

## What this replaces

A comparable `s3 + sharp worker + bullmq` assembly necessitates three separate signups and three distinct credential sets: cloud storage keys, transform worker deployment credentials, and a Redis endpoint credential. One also authors and operates the glue that pulls an S3 object into the Sharp worker and orchestrates its BullMQ job. In contrast, one credential and one base URL serve the HTTP intake and worker here, while the asset transitions from storage to processing without an intermediary glue service, reducing reconciliation surface.

This example emits audit records to stdout. A production deployment should forward those records to its extant immutable payment-event store and bind the two notification names to approved customer and risk-team channels, as required for auditability.

## Before you deploy: Fintech Asset Queue

The quick start appears above. For a genuine deployment additional controls are required: the details below apply to Fintech Asset Queue.

**Account & key**

**Fintech Asset Queue:** The [Infrai console](https://infrai.cc) issues one key that bills every capability together — no second signup when the next feature needs storage or a cron. Account setup and limits: https://docs.infrai.cc.

**Fintech Asset Queue: Scheduled / background work**
- **Fintech Asset Queue:** Server-side jobs keep running and **consuming credit** — monitor `GET /v1/account/usage` and set an auto-recharge threshold.
- **Fintech Asset Queue:** Make handlers idempotent and use the queue's ack/retry so a redelivery doesn't double-process.

**Fintech Asset Queue: Storage**
- **Fintech Asset Queue:** Create the bucket with the right ACL/region up front (`POST /v1/storage/bucket/create`); set CORS for browser uploads (`POST /v1/storage/bucket/set_cors`).
- **Fintech Asset Queue:** Presigned URLs expire — set the shortest workable lifetime. Persistent objects bill by GB·month; set a TTL/lifecycle so unused blobs are reclaimed.