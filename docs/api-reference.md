# Railbridge REST API Reference

> **Complete API Specification, Request/Response Payloads & RFC 9457 Errors**

## Reader Contract

- **Audience:** Frontend and backend developers integrating applications with Railbridge.
- **Assumed Knowledge:** Standard HTTP request structures, JSON encoding, and Bearer token authorization.
- **Outcome:** You will be able to authenticate, create hosted checkout sessions, verify MFS transactions, dispatch SMS, send transactional emails, and configure email forwarding.

---

## 1. Global Conventions

- **Base URL (Staging / Alwaysdata):** `https://railbridge.alwaysdata.net`
- **Base URL (Local Sandbox):** `http://localhost:8080`
- **Content-Type:** `application/json`
- **Authorization:** `Authorization: Bearer <API_KEY>`
  - Production Keys: `rb_live_...`
  - Sandbox Test Keys: `rb_test_...`
  - Android Bridge Device Tokens: `rb_dev_...`
- **Monetary Unit:** **Minor Units** (`amount_minor BIGINT`). 150.00 BDT is passed as `15000`.

---

## 2. RFC 9457 Error Contract

All error responses return HTTP status codes in the $4\text{xx}$ or $5\text{xx}$ range with `application/problem+json`:

```json
{
  "type": "https://railbridge.alwaysdata.net/errors/invalid_amount",
  "title": "Bad Request",
  "status": 400,
  "detail": "amount_minor must be a positive integer greater than 0"
}
```

---

## 3. Payment Rail Endpoints

### 3.1 Create Payment Session

Creates a new hosted checkout session. Supports `Idempotency-Key` to safely prevent duplicate charges.

```http
POST /v1/payments/sessions HTTP/1.1
Host: railbridge.alwaysdata.net
Authorization: Bearer rb_live_8f7b2c4e1d0a9f8e7d6c5b4a3f2e1d0c
Content-Type: application/json
Idempotency-Key: pay_ord_9901_init

{
  "external_reference_id": "ORD-9901",
  "amount_minor": 15000,
  "currency": "BDT",
  "customer_phone": "+8801812345678",
  "return_url": "https://my-store.com/checkout/complete?order=ORD-9901",
  "metadata": {
    "customer_id": "cust_441",
    "tier": "pro"
  }
}
```

**Response (`201 Created`):**
```json
{
  "id": "4e64930e-198e-4582-b8c9-b2651acb8c07",
  "status": "pending",
  "amount_minor": 15000,
  "currency": "BDT",
  "checkout_url": "https://railbridge.alwaysdata.net/checkout/4e64930e-198e-4582-b8c9-b2651acb8c07",
  "expires_at": "2026-11-21T18:30:00Z",
  "created_at": "2026-11-21T18:00:00Z"
}
```

---

### 3.2 Get Payment Session Details

Retrieves current status, ledger metadata, and verification details.

```http
GET /v1/payments/sessions/4e64930e-198e-4582-b8c9-b2651acb8c07 HTTP/1.1
Host: railbridge.alwaysdata.net
Authorization: Bearer rb_live_8f7b2c4e1d0a9f8e7d6c5b4a3f2e1d0c
```

**Response (`200 OK`):**
```json
{
  "id": "4e64930e-198e-4582-b8c9-b2651acb8c07",
  "tenant_id": "8f7b2c4e-0000-0000-0000-000000000000",
  "external_reference_id": "ORD-9901",
  "idempotency_key": "pay_ord_9901_init",
  "amount_minor": 15000,
  "currency": "BDT",
  "status": "verified",
  "channel": "bkash",
  "customer_phone": "+8801812345678",
  "transaction_id": "9K72B8X9M1",
  "return_url": "https://my-store.com/checkout/complete?order=ORD-9901",
  "metadata": {
    "customer_id": "cust_441",
    "tier": "pro"
  },
  "expires_at": "2026-11-21T18:30:00Z",
  "verified_at": "2026-11-21T18:02:15Z",
  "created_at": "2026-11-21T18:00:00Z",
  "updated_at": "2026-11-21T18:02:15Z"
}
```

---

### 3.3 Submit Customer TrxID Verification

Called by customer checkout or backend to reconcile a transaction.

```http
POST /v1/payments/sessions/4e64930e-198e-4582-b8c9-b2651acb8c07/verify HTTP/1.1
Host: railbridge.alwaysdata.net
Authorization: Bearer rb_live_8f7b2c4e1d0a9f8e7d6c5b4a3f2e1d0c
Content-Type: application/json

{
  "channel": "bkash",
  "transaction_id": "9K72B8X9M1",
  "customer_phone": "01812345678"
}
```

**Response (`200 OK` - Match found in buffer):**
```json
{
  "status": "verified",
  "session_id": "4e64930e-198e-4582-b8c9-b2651acb8c07",
  "transaction_id": "9K72B8X9M1",
  "verified_at": "2026-11-21T18:02:15Z",
  "matched_source": "inbox_buffer"
}
```

**Response (`200 OK` - Pending incoming SMS):**
```json
{
  "status": "processing",
  "session_id": "4e64930e-198e-4582-b8c9-b2651acb8c07",
  "transaction_id": "9K72B8X9M1",
  "message": "Transaction submitted. Awaiting automated telco verification."
}
```

---

## 4. SMS Rail Endpoints

### 4.1 Queue Outbound SMS

Dispatches transactional SMS or OTPs with automated E.164 normalization and circuit-breaker failover.

```http
POST /v1/sms/messages HTTP/1.1
Host: railbridge.alwaysdata.net
Authorization: Bearer rb_live_8f7b2c4e1d0a9f8e7d6c5b4a3f2e1d0c
Content-Type: application/json
Idempotency-Key: otp_user_9918_v1

{
  "to": "01712345678",
  "message": "Your SWE Labs verification code is 4821."
}
```

**Response (`202 Accepted`):**
```json
{
  "job_id": "cc1d09c4-2960-49ad-b962-e2ee26782afc",
  "status": "sent",
  "recipient": "+8801712345678",
  "provider_used": "greenweb",
  "sent_at": "2026-11-21T18:05:00Z"
}
```

---

## 5. Email Rail Endpoints

### 5.1 Send Transactional Email

```http
POST /v1/emails/messages HTTP/1.1
Host: railbridge.alwaysdata.net
Authorization: Bearer rb_live_8f7b2c4e1d0a9f8e7d6c5b4a3f2e1d0c
Content-Type: application/json
Idempotency-Key: receipt_inv_441

{
  "to": "customer@example.com",
  "subject": "Payment Receipt for ORD-9901",
  "html_body": "<p>Payment of <strong>BDT 150.00</strong> confirmed.</p>"
}
```

**Response (`202 Accepted`):**
```json
{
  "job_id": "a9b2c3d4-1122-3344-5566-778899aabbcc",
  "status": "sent",
  "recipient": "customer@example.com",
  "provider_used": "smtp",
  "sent_at": "2026-11-21T18:05:30Z"
}
```

---

### 5.2 Register Custom Domain Email Alias

```http
POST /v1/emails/aliases HTTP/1.1
Host: railbridge.alwaysdata.net
Authorization: Bearer rb_live_8f7b2c4e1d0a9f8e7d6c5b4a3f2e1d0c
Content-Type: application/json

{
  "source_address": "business@swe-labs.com",
  "target_address": "founder@gmail.com"
}
```

**Response (`201 Created`):**
```json
{
  "status": "created",
  "source_address": "business@swe-labs.com",
  "target_address": "founder@gmail.com"
}
```

---

### 5.3 Ingest Routed Inbound Email (Cloudflare Worker)

```http
POST /v1/emails/inbound HTTP/1.1
Host: railbridge.alwaysdata.net
Content-Type: application/json

{
  "from": "investor@venture.com",
  "to": "business@swe-labs.com",
  "subject": "Term Sheet Follow-up",
  "text": "Hello, please review the revised terms."
}
```

**Response (`200 OK`):**
```json
{
  "status": "routed",
  "source_address": "business@swe-labs.com",
  "target_address": "founder@gmail.com"
}
```

---

## 6. Android SIM Bridge Hardware Endpoints

### 6.1 Ingest Operator SMS

```http
POST /v1/ingest/android-sms HTTP/1.1
Host: railbridge.alwaysdata.net
Authorization: Bearer rb_dev_sandbox_device_token_12345
Content-Type: application/json

{
  "sender": "16247",
  "body": "You have received Tk 150.00 from 01812345678. Fee Tk 0.00. Balance Tk 10,230.12. TrxID 9K72B8X9M1 at 21/11/2026 17:35"
}
```

**Response (`200 OK`):**
```json
{
  "action": "session_matched",
  "status": "verified",
  "session_id": "4e64930e-198e-4582-b8c9-b2651acb8c07",
  "transaction_id": "9K72B8X9M1",
  "amount_minor": 15000
}
```

---

### 6.2 Android Bridge Heartbeat Ping

```http
POST /v1/ingest/android-ping HTTP/1.1
Host: railbridge.alwaysdata.net
Authorization: Bearer rb_dev_sandbox_device_token_12345
```

**Response (`200 OK`):**
```json
{
  "status": "online",
  "device_id": "sandbox-device-001",
  "device_name": "Warehouse bKash Android Node",
  "synced_at": "2026-11-21T18:10:00Z"
}
```
