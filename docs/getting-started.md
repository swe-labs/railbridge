# Getting Started with Railbridge

**Reader Contract:** This tutorial is for backend engineers (Go, Node.js, Python, PHP) who need to integrate Mobile Financial Services (bKash, Nagad, Rocket), transactional SMS, and custom domain email forwarding. After reading this guide, you will be able to launch a local development sandbox, create payment checkout sessions, and verify incoming transactions end-to-end.

---

## 1. Prerequisites & Installation

Railbridge compiles into a single static binary with zero external runtime dependencies.

### Option A: Local Sandbox Mode (SQLite WAL)

```bash
# Clone the repository
git clone https://github.com/swe-labs/railbridge.git
cd railbridge

# Launch the development sandbox with automatic schema initialization
go run ./cmd/railbridge -dev
```

Output:
```
{"time":"...","level":"INFO","msg":"Starting Railbridge Engine","version":"v1.0.0","dev_mode":true,"role":"all"}
{"time":"...","level":"INFO","msg":"Applying schema migration","version":"001_initial_schema.sql","dialect":"sqlite"}
{"time":"...","level":"INFO","msg":"Railbridge HTTP listener active","address":"0.0.0.0:8080","url":"http://localhost:8080"}
{"time":"...","level":"INFO","msg":"Sandbox credentials active","api_key":"rb_test_sandbox_secret_key_12345"}
```

### Option B: Cloud Production Mode (PostgreSQL 16+)

```bash
export DATABASE_URL="postgres://railbridge:secret@postgresql-railbridge.alwaysdata.net:5432/railbridge_db?sslmode=require"
export APP_BASE_URL="https://railbridge.alwaysdata.net"
export MASTER_ENCRYPTION_KEY="0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
export EMAIL_DRIVER="smtp"
export SMTP_HOST="smtp-railbridge.alwaysdata.net"
export SMTP_PORT="587"
export SMTP_USER="business@swe-labs.com"
export SMTP_PASS="secret_password"

./railbridge
```

---

## 2. Authenticating API Requests

All tenant-scoped API endpoints require an `Authorization` header containing a valid API key:

```http
Authorization: Bearer rb_live_...
# or for sandbox:
Authorization: Bearer rb_test_sandbox_secret_key_12345
```

Android SIM Bridge devices authenticate using dedicated device tokens:

```http
Authorization: Bearer rb_dev_...
```

---

## 3. The Payment Lifecycle in 3 Steps

### Step 1: Create a Payment Session
Send a `POST /v1/payments/sessions` request from your backend:

```bash
curl -X POST http://localhost:8080/v1/payments/sessions \
  -H "Authorization: Bearer rb_test_sandbox_secret_key_12345" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: idemp_order_1001" \
  -d '{
    "external_reference_id": "ORD-1001",
    "amount_minor": 25000,
    "currency": "BDT",
    "customer_phone": "01712345678",
    "return_url": "https://your-store.example/checkout/complete"
  }'
```

Response:
```json
{
  "id": "c9284fa2-1b12-4ce8-8f81-54bce31b990a",
  "status": "pending",
  "amount_minor": 25000,
  "currency": "BDT",
  "checkout_url": "http://localhost:8080/checkout/c9284fa2-1b12-4ce8-8f81-54bce31b990a",
  "expires_at": "2026-11-21T15:30:00Z",
  "created_at": "2026-11-21T15:00:00Z"
}
```

### Step 2: Customer Completes Payment
Redirect the customer to `checkout_url`. The customer opens their bKash/Nagad app, performs a **Send Money** of 250.00 BDT to the merchant number, enters the TrxID (e.g. `9K72B8X9M1`), and submits.

### Step 3: Automated Telco Verification
When the carrier SMS arrives on the Android SIM bridge, it ingests into `/v1/ingest/android-sms`:

```bash
curl -X POST http://localhost:8080/v1/ingest/android-sms \
  -H "Authorization: Bearer rb_dev_sandbox_device_token_12345" \
  -H "Content-Type: application/json" \
  -d '{
    "sender": "16247",
    "body": "You have received Tk 250.00 from 01712345678. Fee Tk 0.00. Balance Tk 15,230.12. TrxID 9K72B8X9M1 at 21/11/2026 15:02"
  }'
```

Output:
```json
{
  "status": "verified",
  "action": "session_matched",
  "transaction_id": "9K72B8X9M1",
  "session_id": "c9284fa2-1b12-4ce8-8f81-54bce31b990a"
}
```

Railbridge executes an atomic transaction, marks the session verified, and immediately enqueues a signed HMAC-SHA256 webhook to your registered `webhook_url`.

---

## 4. Sending Transactional SMS

```bash
curl -X POST http://localhost:8080/v1/sms/messages \
  -H "Authorization: Bearer rb_test_sandbox_secret_key_12345" \
  -H "Content-Type: application/json" \
  -d '{
    "to": "01812345678",
    "message": "Your one-time passcode is 492019. Valid for 5 minutes."
  }'
```

Output:
```json
{
  "status": "queued",
  "id": "e45a2b88-912c-4b55-a012-98bfca22191b",
  "recipient": "+8801812345678",
  "driver": "greenweb"
}
```

---

## 5. Official Go SDK Quickstart

```bash
go get github.com/swe-labs/railbridge/pkg/railbridge
```

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/swe-labs/railbridge/pkg/railbridge"
)

func main() {
	client := railbridge.NewClient("rb_live_a1b2c3d4e5f60718293a4b5c6d7e8f90",
		railbridge.WithBaseURL("https://railbridge.alwaysdata.net"),
	)

	session, err := client.CreatePaymentSession(context.Background(), railbridge.CreateSessionRequest{
		ExternalReferenceID: "INV-2026-001",
		AmountMinor:         50000, // 500.00 BDT
		Currency:            "BDT",
		CustomerPhone:       "01712345678",
		ReturnURL:           "https://myshop.com/orders/success",
	})
	if err != nil {
		log.Fatalf("failed to create session: %v", err)
	}

	fmt.Printf("Payment session ready: %s\nCustomer URL: %s\n", session.ID, session.CheckoutURL)
}
```
