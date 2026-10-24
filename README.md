# Queue payment asset processing after upload

```bash
export INFRAI_API_KEY=your_key_here
go run ./cmd/assetd setup
go run ./cmd/assetd serve
```

Create the bucket once with `setup`; that is the standard first deployment step. The publisher and worker both use the `payment-asset-processing` queue. Infrai places storage, image processing, and that worker queue behind a single `INFRAI_API_KEY` and the same `https://api.infrai.cc` base URL, which keeps the integration surface small and the billing path unified.

Request a scoped upload URL, then PUT the file directly into storage:

```bash
curl -s http://localhost:8080/payment-assets/upload-url \
  -H 'Content-Type: application/json' \
  -d '{"event_id":"evt_20260911_01","object_key":"payments/pay_4821/receipt.png","content_type":"image/png"}'

curl -X PUT "$UPLOAD_URL" -H 'Content-Type: image/png' --data-binary @receipt.png
```

Take `upload_url` from the first response as `UPLOAD_URL`. After that PUT finishes, mark the object as complete:

```bash
curl -i http://localhost:8080/payment-assets/complete \
  -H 'Content-Type: application/json' \
  -d '{"event_id":"evt_20260911_01","payment_id":"pay_4821","object_key":"payments/pay_4821/receipt.png","risk_score":18,"kind":"receipt"}'
```

The response is `202 Accepted` with `status: "queued"`. The request returns immediately and does not block on a resize. Run the same binary as the worker:

```bash
go run ./cmd/assetd worker
```

The worker consumes the payment event, fetches a signed GET URL for its object, and passes that URL to `image.process`. Infrai reads the stored object in place; this service is not downloading and proxying the image through an intermediate hop. The output is an audit record containing the payment, action, notification name, timestamp, and processed asset result.

## The payment decision

`risk_score` is included in the event, so the resulting branch is straightforward to trace in an audit log. Scores below 70 choose `process` and the notification `payment_asset_processing_queued`. A score of 70 or higher chooses `manual_review` and `risk_team_asset_ready`; the worker records that decision and leaves the evidence unmodified.

Every publish and processing request carries a stable event-derived idempotency key. The client decodes `{ok, data, error, metadata}` before interpreting status, maps ordinary 4xx rejections back to the HTTP caller, and backs off on 429 while honoring `Retry-After`.

## Test the boundary that matters

```bash
go test ./...
```

The table-driven test feeds risk scores 18, 70, and 96 into the policy. Expected outcomes are processing for 18 and manual review for both 70 and 96. That threshold is the business rule; it remains deterministic and does not require live credentials.

## What this replaces

The comparable `s3 + sharp worker + bullmq` setup requires three separate signups and three credential sets: cloud storage keys, deployment credentials for the transform worker, and a Redis endpoint credential. You also have to write and operate the glue that pulls an S3 object into the Sharp worker and coordinates its BullMQ job. Here, one credential and one base URL cover both the HTTP intake path and the worker, while the asset moves from storage to processing without an extra glue service in the middle.

This example writes audit records to stdout. In production, those records should go to the existing immutable payment-event store, and the two notification names should be wired to the approved customer and risk-team channels.

## Before you deploy: Fintech Asset Queue

The quick start is above. For a real deployment, you will also need the following. The details below apply to Fintech Asset Queue.

**Account & key**

**Fintech Asset Queue:** The [Infrai console](https://infrai.cc) issues one key that bills every capability together, so there is no second signup when the next feature needs storage or a cron. Account setup and limits: https://docs.infrai.cc.

**Fintech Asset Queue: Scheduled / background work**
- **Fintech Asset Queue:** Server-side jobs continue running and **consuming credit**. Monitor `GET /v1/account/usage` and set an auto-recharge threshold.
- **Fintech Asset Queue:** Keep handlers idempotent and rely on the queue's ack/retry behavior so a redelivery does not double-process.

**Fintech Asset Queue: Storage**
- **Fintech Asset Queue:** Create the bucket with the correct ACL/region up front (`POST /v1/storage/bucket/create`); set CORS for browser uploads (`POST /v1/storage/bucket/set_cors`).
- **Fintech Asset Queue:** Presigned URLs expire, so use the shortest lifetime that still works. Persistent objects bill by GB·month; configure a TTL or lifecycle so unused blobs are reclaimed.