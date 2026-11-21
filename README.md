# Railbridge

> **Universal Multi-Tenant Payment, SMS & Email Infrastructure Hub by `swe-labs`**

[![Go Version](https://img.shields.io/badge/Go-1.27.4+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![Version](https://img.shields.io/badge/Release-v1.0.0-blue.svg)](https://github.com/swe-labs/railbridge/releases/tag/v1.0.0)
[![Architecture](<https://img.shields.io/badge/Architecture-Modular%20Monolith-2ea44f>)](#architecture)
[![Database](<https://img.shields.io/badge/DB-PostgreSQL%20%7C%20SQLite-336791>)](https://www.postgresql.org/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](#license)
[![Staging](https://img.shields.io/badge/Staging-railbridge.alwaysdata.net-orange)](https://railbridge.alwaysdata.net)

**Railbridge** (`github.com/swe-labs/railbridge`) is an independent, tenant-agnostic transactional infrastructure rails server written in Go (Go 1.27.4+). It compiles into a single static binary and centralizes four essential operations behind an idempotent REST API:

1. **Payment Orchestration:** Multi-channel mobile financial services (bKash, Nagad, Rocket) and card gateways with hosted checkouts, TrxID auto-matching, and replay protection.
2. **Transactional SMS Dispatch:** Unified SMS gateway with phone number normalization (E.164), commercial provider routing, and an Android SIM gateway bridge for zero API-cost operations.
3. **Transactional Email Pipeline:** Asynchronous queued email delivery with HTML template rendering, exponential backoff retries, and provider abstraction (SMTP, Resend, Brevo, Postmark).
4. **Custom Domain Inbound Email Forwarding:** Virtual alias routing system (e.g., routing `business@swe-labs.com` to personal inboxes such as Gmail, Outlook, Proton Mail, or Yahoo) with an inbound webhook parser and delivery audit trail.

---

## Technical Questions & Architectural Decisions

### 1. Alwaysdata Hosting (`railbridge.alwaysdata.net`)

Alwaysdata is well-suited for the initial deployment and staging environment:

- **Runtime Execution:** Alwaysdata provides native support for persistent "User Programs". You deploy a standalone, statically compiled Go binary (`CGO_ENABLED=0 GOOS=linux GOARCH=amd64`) that runs continuously as a managed system daemon.
- **Dynamic Port & IP Binding:** Alwaysdata injects standard environment variables (`$IP` and `$PORT`). Railbridge binds directly to these interfaces without requiring custom ingress proxies.
- **Database Support:** A managed PostgreSQL database is provisioned in the same data center environment, offering low latency for database queries and transactional outbox operations.
- **SSL/TLS Certificates:** Railbridge runs behind Alwaysdata's managed reverse proxy, which provisions and renews Let's Encrypt SSL/TLS certificates automatically for `railbridge.alwaysdata.net`.
- **Custom Domain Transition:** When transitioning to a production domain (such as `api.railbridge.swe-labs.com`), no code rewrites or server reconfigurations are needed. The migration requires:
  1. Adding a DNS `CNAME` record pointing `api.railbridge.swe-labs.com` to `railbridge.alwaysdata.net`.
  2. Adding the custom domain name under the Alwaysdata Sites configuration dashboard.
  3. Updating the `APP_BASE_URL` environment variable in the application configuration.

---

### 2. Horizontal Scaling & SMS Infrastructure

#### Horizontal Scaling Strategy

Railbridge is engineered as a stateless modular monolith. It avoids in-memory state for core operations:

- **Stateless Application Tier:** All HTTP sessions, tenant authorizations, and state machines are persisted in PostgreSQL. An individual Railbridge process holds no node-specific state.
- **Concurrency via `FOR UPDATE SKIP LOCKED`:** Background workers processing outbox events (webhook deliveries, SMS dispatches, email transmissions) poll the database using PostgreSQL row-level locking:
  ```sql
  SELECT id, payload
  FROM outbox_jobs
  WHERE status = 'pending' AND next_run_at <= NOW()
  ORDER BY next_run_at ASC
  LIMIT 10
  FOR UPDATE SKIP LOCKED;
  ```

  This mechanism guarantees that multiple Railbridge nodes can query the same jobs table concurrently without race conditions, lock contention, or duplicate deliveries.
- **Growth Path:** The platform can start as a single binary on Alwaysdata. As throughput demands increase, identical binary instances can be distributed across multiple VPS instances (such as DigitalOcean, Hetzner, or AWS EC2) behind an Nginx, HAProxy, or Cloudflare load balancer without altering code architecture.

#### SMS Strategy: Commercial Gateways vs. Android SIM Bridge

You do not need to purchase a commercial bulk SMS pack before launching an MVP. Railbridge provides two operational modes:

| Dimension                      | Mode 1: Android SIM Bridge (Zero-Cost Gateway)                                                                                                                                                                                                                         | Mode 2: Commercial BD SMS Gateways                                                                    |
| ------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| **Mechanism**            | An Android device running an SMS relay service connects via HTTPS to Railbridge. Outbound messages are pushed to the device via secure polling or push notification; the device dispatches SMS using an active telecom SIM (Grameenphone, Robi, Banglalink, Teletalk). | Outbound messages are dispatched via REST APIs to licensed aggregators (Greenweb, BulkSMSBD, MimSMS). |
| **Operational Cost**     | Standard operator bundle costs (~0.05–0.10 BDT per SMS). Zero aggregator setup fees.                                                                                                                                                                                  | Pre-purchased API packs (~0.25–0.45 BDT per SMS).                                                    |
| **Throughput**           | 10 to 30 SMS per minute (constrained by Android OS hardware and operator limits).                                                                                                                                                                                      | 500 to 2,000+ SMS per minute (direct operator aggregator links).                                      |
| **Sender ID**            | Standard mobile number (+88017..., +88018...).                                                                                                                                                                                                                         | Masked brand name (e.g.,`SWE-LABS`) or dedicated shortcode.                                         |
| **Inbound Verification** | **Native TrxID Capture:** The phone ingests incoming payment notification SMS from bKash (`16247`) and Nagad (`16167`), parses TrxID, and notifies Railbridge.                                                                                               | Requires manual entry or a dedicated webhook subscription from MFS aggregators.                       |
| **Recommended Stage**    | Prototyping, closed betas, internal tooling, and bootstrapping.                                                                                                                                                                                                        | Production scale, high-frequency customer OTPs, enterprise billing alerts.                            |

**Hybrid Failover Configuration:** Railbridge can be configured to use commercial gateways as the primary route while keeping the Android SIM Bridge configured as an automated fallback if the primary provider returns an HTTP 5xx error or has an exhausted balance.

---

### 3. Custom Domain Email Forwarding Architecture

Railbridge supports custom domain email aliasing and inbound forwarding, allowing addresses like `business@swe-labs.com`, `support@swe-labs.com`, and `billing@swe-labs.com` to deliver directly to personal mailboxes (Gmail, Outlook, Proton Mail, Yahoo).

#### Architecture & Delivery Path

```mermaid
flowchart TD
    Sender["External Email Sender"] --> MX["MX: swe-labs.com"]
    MX --> Router["Cloudflare Email Routing or Alwaysdata Mail"]
    Router -->|Direct Forward| Forward["Direct Forward: Personal Mailboxes (Gmail, Outlook, Proton, Yahoo)"]
    Router -->|Worker Hook| Worker["Cloudflare Email Worker"]
    Worker -->|Inbound Webhook| InboundAPI["Railbridge Core Inbound Ingestion"]
    InboundAPI --> Audit["Audit Log and Tenant Alias Resolver"]
    InboundAPI --> Notify["Trigger Webhooks and Outbox Events"]

    style Sender fill:#0f172a,stroke:#64748b,stroke-width:1.5px,color:#f8fafc
    style MX fill:#1e293b,stroke:#0284c7,stroke-width:2px,color:#f8fafc
    style Router fill:#1e293b,stroke:#38bdf8,stroke-width:2px,color:#f8fafc
    style Forward fill:#064e3b,stroke:#10b981,stroke-width:2px,color:#f8fafc
    style Worker fill:#312e81,stroke:#818cf8,stroke-width:2px,color:#f8fafc
    style InboundAPI fill:#1e293b,stroke:#f59e0b,stroke-width:2px,color:#f8fafc
    style Audit fill:#0f172a,stroke:#38bdf8,stroke-width:1.5px,color:#f8fafc
    style Notify fill:#0f172a,stroke:#10b981,stroke-width:1.5px,color:#f8fafc
```

1. **Inbound Spam & Port 25 Handling:** Cloudflare Email Routing acts as the public Mail Exchange (MX) for `swe-labs.com`. Cloudflare absorbs spam, validates SPF/DKIM records, and handles incoming connections on port 25 (a port frequently blocked on VPS hosting providers).
2. **Direct Delivery Mode:** Cloudflare forwards messages directly to the destination address (e.g., `founder@gmail.com`). Envelope headers are preserved using Sender Rewriting Scheme (SRS) to ensure destination mail providers do not reject messages due to strict DMARC policies.
3. **Programmable Webhook Routing:** For accounts requiring an audit trail or downstream processing, a lightweight Cloudflare Worker intercepts the incoming message and dispatches an HTTP POST request containing the parsed email payload to Railbridge's `/v1/emails/inbound` endpoint. Railbridge looks up the recipient in the `email_aliases` database table and processes tenant-configured actions.
4. **Sending Replies from Custom Addresses:** Users can send email appearing directly from `business@swe-labs.com` within standard personal webmail interfaces (such as Gmail's "Send mail as" configuration). Outgoing messages are authenticated using Railbridge's configured SMTP credentials (via Alwaysdata SMTP, Resend, Brevo, or Amazon SES) with aligned SPF (`v=spf1 include:... ~all`) and DKIM TXT records on `swe-labs.com`.

---

## Reader Contract

- **Audience:** Backend engineers, infrastructure operators, and systems architects deploying or integrating Railbridge.
- **Assumed Knowledge:** Familiarity with HTTP/REST APIs, relational databases (PostgreSQL/SQLite), and Go toolchain tools (Go 1.27.4+).
- **Outcome:** You will be able to compile the Railbridge binary, execute local migrations, configure multi-tenant credentials, process payments, dispatch SMS, send transactional emails, and run the service in production.

---

## Table of Contents

- [Documentation Suite](#documentation-suite)
- [Key Capabilities](#key-capabilities)
- [Architecture](#architecture)
- [SMS &amp; MFS Delivery Modes](#sms--mfs-delivery-modes)
- [Custom Domain Email Forwarding](#custom-domain-email-forwarding)
- [REST API Reference](#rest-api-reference)
- [Webhook Security &amp; Verification](#webhook-security--verification)
- [Database Schema](#database-schema)
- [Go SDK Quickstart (`railbridge-go`)](#go-sdk-quickstart-railbridge-go)
- [Deployment Runbook](#deployment-runbook)
  - [Environment Configuration](#environment-configuration)
  - [1. Local Development](#1-local-development)
  - [2. Deploying to Alwaysdata](#2-deploying-to-alwaysdata)
  - [3. Containerized Deployment with Docker](#3-containerized-deployment-with-docker)
- [Horizontal Scaling &amp; Outbox Processing](#horizontal-scaling--outbox-processing)
- [Verification &amp; Diagnostic Commands](#verification--diagnostic-commands)
- [License](#license)

---

## Documentation Suite

Exhaustive, production-grade technical guides are available in the [`docs/`](docs/) directory:

| Document                                                      | Focus & Coverage                                                                                                                                    |
| ------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- |
| [**System Architecture**](docs/architecture.md)          | Modular monolith topology, strict tenant boundary isolation, transactional outbox (`FOR UPDATE SKIP LOCKED`), and circuit breaker state machines. |
| [**REST API Reference**](docs/api-reference.md)          | Complete endpoint schemas, request/response payloads, minor currency units, and RFC 9457 error contracts.                                           |
| [**MFS Integration Guide**](docs/mfs-integration.md)     | Two-way asynchronous bKash/Nagad/Rocket reconciliation, asymmetric buffer matching, Android SIM Bridge hardware setup, and regex tables.            |
| [**Custom Domain Email Routing**](docs/email-routing.md) | Inbound virtual aliasing (`business@swe-labs.com` $\rightarrow$ Gmail/Outlook), Cloudflare Email Worker scripts, and outbound SMTP setup.       |
| [**Production Operations Runbook**](docs/deployment.md)  | Alwaysdata user program setup, Docker multi-stage distroless containers, PostgreSQL connection pooling, and multi-node clustering.                  |

---

## Key Capabilities

| Subsystem                    | Core Functionality                                                                                                                                                                   | Integrated Drivers & Protocols                                                                                            |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------- |
| **Payment Rail**       | Hosted multilingual checkout (English & Bengali), automated TrxID transaction matching, duplicate submission defense, replay protection, and cryptographically signed HMAC webhooks. | bKash (Tokenized API & Manual TrxID), Nagad (Merchant API & Manual TrxID), Rocket, SSLCommerz, Manual Bank Wire Transfer. |
| **SMS Rail**           | High-throughput notification engine, telephone number formatting (E.164), dynamic routing, and automated circuit breaker failover.                                                   | Greenweb, BulkSMSBD, MimSMS, Twilio, Infobip,**Android SIM Bridge Gateway**.                                        |
| **Email Rail**         | Asynchronous transactional mail queue, HTML template rendering, parameter injection, exponential backoff retries, and delivery tracking.                                             | Alwaysdata SMTP, Custom SMTP, Resend, Brevo, Postmark.                                                                    |
| **Email Routing Rail** | Custom domain alias routing (`business@swe-labs.com` to target inbox), delivery audit logs, and incoming webhook ingestion.                                                        | Cloudflare Email Routing / Email Workers, Alwaysdata Mail Forwarding, Inbound Webhook Dispatcher.                         |

### Engineering Guarantees

- **Multi-Tenant Isolation:** Tenant boundary enforcement using cryptographically hashed API keys (`rb_live_...` and `rb_test_...`), isolated tenant identifiers on all data rows, and encrypted tenant secrets.
- **Strict Idempotency Guard:** `Idempotency-Key` headers on all mutating POST requests. Repeated requests with identical keys return the initial cached response without executing duplicate operations.
- **Deterministic Sandbox Environment:** API requests authenticated with `rb_test_` keys route through mock adapters that simulate provider delays, network errors, and successful completions without invoking third-party billable APIs.
- **Crash-Resilient Transactional Outbox:** Outbound webhooks, SMS messages, and emails are persisted within the relational database in the same transaction as the triggering business state change. Background workers poll this queue; system halts or container restarts do not drop in-flight tasks.

---

## Architecture

```mermaid
flowchart TD
    subgraph Clients["CLIENT LAYER (Multi-Tenant Applications)"]
        ClientA["Client App A (E-Commerce)"]
        ClientB["Client App B (SaaS Platform)"]
        ClientC["Client App C (Mobile / ERP)"]
    end

    subgraph Edge["API EDGE & SECURITY GATEWAY"]
        HTTPS["HTTPS REST API (Port 443) / Auth: Bearer rb_live_... / rb_test_..."]
        Guard["Edge Security: AuthN · Tenant Context · Rate Limiter · Idempotency Guard"]
        HTTPS --> Guard
    end

    ClientA --> HTTPS
    ClientB --> HTTPS
    ClientC --> HTTPS

    subgraph Core["RAILBRIDGE CORE (Single Static Go Binary)"]
        direction TB
        subgraph Rails["MODULAR TRANSACTIONAL RAILS"]
            PayRail["Payment Rail: Hosted Checkout · bKash/Nagad/Rocket · TrxID Reconciliation"]
            SMSRail["SMS Rail: BD Aggregators · Android SIM Bridge · Circuit Breaker Failover"]
            EmailRail["Email Rail: SMTP Drivers · Cloud APIs · HTML Templates · Virtual Aliasing"]
        end

        subgraph DurableEngine["DURABLE ENGINE & ASYNC WORKERS"]
            Outbox["Transactional Outbox: Atomic SQL Persistence (Commit with State)"]
            WorkerPool["Worker Pool: Concurrent Polling (FOR UPDATE SKIP LOCKED)"]
            Dispatcher["Signed HMAC-SHA256 Webhook Dispatcher + Exponential DLQ"]
            Outbox --> WorkerPool --> Dispatcher
        end

        Rails --> Outbox
    end

    Guard --> PayRail
    Guard --> SMSRail
    Guard --> EmailRail

    subgraph Storage["DATABASE & INFRASTRUCTURE TIER"]
        DB["Relational Storage: PostgreSQL 16+ (Production) / SQLite WAL (Local Dev)"]
        Cluster["Deployment: Alwaysdata.net (Staging) / Multi-Node VPS Cluster (Prod)"]
        DB --- Cluster
    end

    DurableEngine --> DB

    style Clients fill:#0b1329,stroke:#1e293b,stroke-width:1px,color:#94a3b8
    style ClientA fill:#1e293b,stroke:#38bdf8,stroke-width:1.5px,color:#f8fafc
    style ClientB fill:#1e293b,stroke:#38bdf8,stroke-width:1.5px,color:#f8fafc
    style ClientC fill:#1e293b,stroke:#38bdf8,stroke-width:1.5px,color:#f8fafc
    style Edge fill:#0f172a,stroke:#0284c7,stroke-width:2px,color:#f8fafc
    style HTTPS fill:#1e293b,stroke:#38bdf8,stroke-width:1.5px,color:#f8fafc
    style Guard fill:#1e293b,stroke:#0ea5e9,stroke-width:1.5px,color:#f8fafc
    style Core fill:#0f172a,stroke:#3b82f6,stroke-width:2px,color:#f8fafc
    style Rails fill:#1e1b4b,stroke:#6366f1,stroke-width:1.5px,color:#f8fafc
    style PayRail fill:#1e293b,stroke:#10b981,stroke-width:1.5px,color:#f8fafc
    style SMSRail fill:#1e293b,stroke:#f59e0b,stroke-width:1.5px,color:#f8fafc
    style EmailRail fill:#1e293b,stroke:#06b6d4,stroke-width:1.5px,color:#f8fafc
    style DurableEngine fill:#1e293b,stroke:#8b5cf6,stroke-width:1.5px,color:#f8fafc
    style Outbox fill:#0f172a,stroke:#a855f7,stroke-width:1.5px,color:#f8fafc
    style WorkerPool fill:#0f172a,stroke:#a855f7,stroke-width:1.5px,color:#f8fafc
    style Dispatcher fill:#0f172a,stroke:#a855f7,stroke-width:1.5px,color:#f8fafc
    style Storage fill:#022c22,stroke:#059669,stroke-width:2px,color:#f8fafc
    style DB fill:#064e3b,stroke:#10b981,stroke-width:1.5px,color:#f8fafc
    style Cluster fill:#064e3b,stroke:#10b981,stroke-width:1.5px,color:#f8fafc
```

---

## SMS Delivery Modes

Railbridge supports two distinct outbound SMS transmission mechanisms that can be deployed independently or combined in an active-failover topology:

```mermaid
flowchart TD
    Req["Outbound SMS Dispatch Request"] --> Router["Provider Router & Circuit Breaker"]
    Router -->|Healthy Primary Route| Primary["Primary Route: Commercial BD Pack (Greenweb / BulkSMSBD / MimSMS)"]
    Primary -->|Delivered| Delivered["SMS Delivered to Cellular Network"]
    Primary -->|Gateway Timeout or Depleted Balance| Trip{"Circuit Breaker Tripped?"}
    Trip -->|Trip to Open| Fallback["Secondary Fallback: Android SIM Bridge Gateway (Zero API Pack)"]
    Router -->|Active Failover| Fallback
    Fallback --> SIM["Dispatched via Phone SIM Tray (GP, Robi, Banglalink, Teletalk)"]
    SIM --> Delivered

    style Req fill:#0f172a,stroke:#64748b,stroke-width:1.5px,color:#f8fafc
    style Router fill:#1e293b,stroke:#0284c7,stroke-width:2px,color:#f8fafc
    style Primary fill:#1e1b4b,stroke:#818cf8,stroke-width:2px,color:#f8fafc
    style Trip fill:#451a03,stroke:#f59e0b,stroke-width:2px,color:#f8fafc
    style Fallback fill:#3b0764,stroke:#d946ef,stroke-width:2px,color:#f8fafc
    style SIM fill:#1e293b,stroke:#38bdf8,stroke-width:1.5px,color:#f8fafc
    style Delivered fill:#064e3b,stroke:#10b981,stroke-width:2px,color:#f8fafc
```

1. **Android SIM Bridge (Zero API Cost Mode):**

   - An Android smartphone equipped with an active Bangladesh SIM card (Grameenphone, Robi, Banglalink, Teletalk) runs an agent application configured with a shared secret.
   - Railbridge queues outbound SMS into the outbox. The Android app polls or receives an event push, dispatches the SMS via the device's native telephony manager using standard SMS bundle allowances, and reports transmission status back to Railbridge.
   - For payment verification, the application monitors incoming SMS from financial shortcodes (`16247` for bKash, `16167` for Nagad). When an incoming payment notification arrives, the application extracts the Transaction ID (TrxID) and amount, posting it securely to `/v1/ingest/android-sms` to automatically match pending payment sessions.
2. **Commercial BD SMS Aggregators:**

   - Transmits messages directly to tier-1 Bangladesh aggregators (Greenweb, BulkSMSBD, MimSMS) via HTTP REST APIs.
   - Supports custom sender IDs (alphanumeric masking), higher concurrency, and network status reports.
3. **Hybrid Failover:**

   - If the primary commercial provider returns an HTTP status $\ge 500$, network timeout, or error indicating balance depletion, the dispatcher reroutes the message to the Android SIM Bridge within the outbox worker loop.

---

## Custom Domain Email Forwarding

Railbridge routes custom domain communications without requiring a dedicated mailbox server:

```mermaid
flowchart TD
    Inbound["Incoming Email to business@swe-labs.com"] --> CF["Cloudflare Email Routing (MX Edge)"]
    CF -->|Direct Delivery| Mailboxes["Personal Inboxes: Gmail · Outlook · Proton · Yahoo"]
    CF -->|Worker Transformation| Worker["Cloudflare Email Worker"]
    Worker -->|Inbound Webhook| IngestAPI["Railbridge Inbound Email Ingestion"]
    IngestAPI --> DBLog["Persist Message Audit Log (message_logs)"]
    IngestAPI --> AliasMap["Resolve Tenant Virtual Alias (email_aliases)"]
    AliasMap --> Event["Enqueue Outbox Notification Event"]

    style Inbound fill:#0f172a,stroke:#64748b,stroke-width:1.5px,color:#f8fafc
    style CF fill:#1e293b,stroke:#0284c7,stroke-width:2px,color:#f8fafc
    style Mailboxes fill:#064e3b,stroke:#10b981,stroke-width:2px,color:#f8fafc
    style Worker fill:#312e81,stroke:#818cf8,stroke-width:2px,color:#f8fafc
    style IngestAPI fill:#1e293b,stroke:#38bdf8,stroke-width:2px,color:#f8fafc
    style DBLog fill:#0f172a,stroke:#475569,stroke-width:1.5px,color:#f8fafc
    style AliasMap fill:#0f172a,stroke:#475569,stroke-width:1.5px,color:#f8fafc
    style Event fill:#064e3b,stroke:#10b981,stroke-width:1.5px,color:#f8fafc
```

### Configuration Steps

1. **DNS Records (Cloudflare):**
   - Configure domain MX records to point to Cloudflare's email receiving servers:
     - `mx1.cloudflare.net` (Priority 10)
     - `mx2.cloudflare.net` (Priority 20)
     - `mx3.cloudflare.net` (Priority 30)
   - Add TXT record for SPF: `v=spf1 include:_spf.mx.cloudflare.net ~all`.
2. **Virtual Routing Modes:**
   - **Direct Forwarding:** In the Cloudflare Email Routing console, create custom rules mapping `business@swe-labs.com` to your personal email address.
   - **Webhook Routing (Audit Trail):** Point a Cloudflare Email Worker to forward the raw MIME content or parsed JSON to:
     `POST https://railbridge.alwaysdata.net/v1/emails/inbound`
     Railbridge stores the message details in `message_logs` and matches it against `email_aliases` to determine secondary actions.
3. **Sending Outbound Mail from Personal Inbox:**
   - In Gmail, open **Settings > Accounts and Import > Send mail as**.
   - Add `business@swe-labs.com`.
   - Set SMTP Server to your configured Railbridge SMTP provider (e.g., `smtp-railbridge.alwaysdata.net`, Resend, or Brevo) on Port 587 with TLS.
   - Incoming mail arrives in your inbox; replies originate directly from your branded domain.

---

## REST API Reference

- **Base URL (Staging):** `https://railbridge.alwaysdata.net`
- **Authentication:** HTTP Header `Authorization: Bearer <API_KEY>`
  - Production Keys: `rb_live_...`
  - Sandbox Keys: `rb_test_...`
- **Error Format:** Conforms to RFC 9457 (`application/problem+json`)

| HTTP Method | Endpoint                              | Description                                            | Idempotent | Success Status   |
| ----------- | ------------------------------------- | ------------------------------------------------------ | ---------- | ---------------- |
| `POST`    | `/v1/payments/sessions`             | Create a hosted checkout session                       | Yes        | `201 Created`  |
| `GET`     | `/v1/payments/sessions/{id}`        | Retrieve payment session details & verification status | N/A        | `200 OK`       |
| `POST`    | `/v1/payments/sessions/{id}/verify` | Submit customer TrxID for reconciliation               | Yes        | `200 OK`       |
| `POST`    | `/v1/sms/messages`                  | Queue an SMS message or OTP for dispatch               | Yes        | `202 Accepted` |
| `POST`    | `/v1/emails/messages`               | Queue a transactional email for dispatch               | Yes        | `202 Accepted` |
| `POST`    | `/v1/emails/aliases`                | Register or update an email forwarding alias           | Yes        | `201 Created`  |
| `POST`    | `/v1/emails/inbound`                | Ingest parsed inbound email payload (Worker hook)      | Yes        | `200 OK`       |
| `POST`    | `/v1/ingest/android-sms`            | Ingest SMS data forwarded from Android SIM Bridge      | Yes        | `200 OK`       |
| `GET`     | `/healthz`                          | Application liveness probe                             | N/A        | `200 OK`       |
| `GET`     | `/readyz`                           | Application readiness & database connectivity probe    | N/A        | `200 OK`       |

### Sample: Create Payment Session

**Request:**

```http
POST /v1/payments/sessions HTTP/1.1
Host: railbridge.alwaysdata.net
Authorization: Bearer rb_live_8f7b2c4e1d0a9f8e7d6c5b4a3f2e1d0c
Content-Type: application/json
Idempotency-Key: pay_order_98124_init

{
  "external_reference_id": "ORD-98124",
  "amount_minor": 25000,
  "currency": "BDT",
  "customer_phone": "+8801812345678",
  "return_url": "https://client-store.com/checkout/complete?order=ORD-98124",
  "metadata": {
    "customer_id": "cust_4410",
    "cart_items": 2
  }
}
```

**Response (`201 Created`):**

```json
{
  "id": "7a3f89e2-9b2e-4b47-a8b3-1f8d4e9c20a1",
  "status": "pending",
  "amount_minor": 25000,
  "currency": "BDT",
  "checkout_url": "https://railbridge.alwaysdata.net/checkout/7a3f89e2-9b2e-4b47-a8b3-1f8d4e9c20a1",
  "expires_at": "2026-11-21T10:45:00Z",
  "created_at": "2026-11-21T10:15:00Z"
}
```

---

## Webhook Security & Verification

All outbound HTTP event notifications dispatched by Railbridge are signed using timestamped HMAC-SHA256 signatures.

### HTTP Headers Transmitted

```http
POST /webhooks/railbridge HTTP/1.1
Host: your-client-app.com
Content-Type: application/json
X-Railbridge-Event: payment.verified
X-Railbridge-Delivery: dlv_01J9X7K2M4A1B2C3D4E5
X-Railbridge-Signature: t=1763719200,v1=5257a869e7ecebeda32affa62cdca3fa51cad7e77a0e56ff536d0ce8e108d8bd
```

### Signature Verification Algorithm

1. Extract the Unix timestamp integer $t$ and signature hex string $v_1$ from the `X-Railbridge-Signature` header.
2. Form the signature payload string:
   $$
   \text{signed\_payload} = t + \text{"."} + \text{raw\_request\_body}
   $$
3. Compute the HMAC using SHA-256 with the tenant's webhook secret key:
   $$
   \text{expected\_signature} = \text{HMAC-SHA256}(\text{webhook\_secret}, \text{signed\_payload})
   $$
4. Compare $v_1$ against $\text{expected\_signature}$ using constant-time string comparison (`crypto/subtle.ConstantTimeCompare`) to prevent timing attacks.
5. Check timestamp freshness against the current system clock:
   $$
   |\text{current\_time} - t| \le 300\text{ seconds}
   $$

   Reject requests outside this window to prevent replay attacks.

### Retry Schedule & Dead-Letter Queue (DLQ)

If a client endpoint fails to return an HTTP status in the range $200\text{--}299$, Railbridge re-queues the delivery job using truncated exponential backoff across 6 attempts:

1. **Attempt 1:** 10 seconds
2. **Attempt 2:** 1 minute
3. **Attempt 3:** 5 minutes
4. **Attempt 4:** 30 minutes
5. **Attempt 5:** 2 hours
6. **Attempt 6:** 12 hours

If all 6 attempts fail, the record in `outbox_jobs` transitions to status `dead_letter` for operator inspection and manual replay.

---

## Database Schema

Production schema designed for **PostgreSQL 16+**. Currency amounts are stored as integers (`BIGINT`) representing minor currency units (e.g., 25000 equals 250.00 BDT) to eliminate floating-point representation and rounding defects.

```sql
-- 1. Tenants (Isolated Client Applications)
CREATE TABLE tenants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL,
    environment VARCHAR(10) NOT NULL DEFAULT 'live' CHECK (environment IN ('live', 'test')),
    api_key_prefix VARCHAR(16) NOT NULL,
    api_key_hash BYTEA NOT NULL UNIQUE, -- SHA-256 hash of API key
    webhook_url VARCHAR(500),
    webhook_secret_encrypted BYTEA,    -- AES-256-GCM encrypted secret
    branding_config JSONB NOT NULL DEFAULT '{}'::jsonb,
    rate_limit_rpm INTEGER NOT NULL DEFAULT 120,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 2. Payment Sessions & Ledger
CREATE TABLE payment_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    external_reference_id VARCHAR(128) NOT NULL,
    idempotency_key VARCHAR(128),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency CHAR(3) NOT NULL DEFAULT 'BDT',
    status VARCHAR(24) NOT NULL DEFAULT 'pending' 
        CHECK (status IN ('pending', 'processing', 'verified', 'failed', 'expired', 'refunded')),
    channel VARCHAR(32), -- bkash, nagad, rocket, card, bank
    customer_phone VARCHAR(24),
    transaction_id VARCHAR(80), -- MFS TrxID or gateway reference
    return_url VARCHAR(500) NOT NULL,
    webhook_url_override VARCHAR(500),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    provider_payload JSONB,
    expires_at TIMESTAMPTZ NOT NULL,
    verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, idempotency_key)
);

-- Replay defense: prevent duplicate use of a single TrxID across distinct sessions
CREATE UNIQUE INDEX idx_payments_unique_trx 
    ON payment_sessions (tenant_id, channel, transaction_id) 
    WHERE transaction_id IS NOT NULL;
CREATE INDEX idx_payment_sessions_tenant ON payment_sessions (tenant_id, created_at DESC);

-- 3. Unified Messaging Logs (SMS & Email Audit)
CREATE TABLE message_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    idempotency_key VARCHAR(128),
    rail VARCHAR(10) NOT NULL CHECK (rail IN ('sms', 'email')),
    recipient VARCHAR(255) NOT NULL,
    subject VARCHAR(255),
    template_id VARCHAR(100),
    provider_used VARCHAR(50),
    provider_msg_id VARCHAR(128),
    status VARCHAR(24) NOT NULL DEFAULT 'queued' 
        CHECK (status IN ('queued', 'sent', 'delivered', 'failed')),
    attempts SMALLINT NOT NULL DEFAULT 0,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at TIMESTAMPTZ,
    UNIQUE (tenant_id, rail, idempotency_key)
);
CREATE INDEX idx_message_logs_tenant ON message_logs (tenant_id, rail, created_at DESC);

-- 4. Custom Domain Email Forwarding Aliases
CREATE TABLE email_aliases (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    source_address VARCHAR(255) NOT NULL UNIQUE, -- e.g. business@swe-labs.com
    target_address VARCHAR(255) NOT NULL,        -- destination e.g. founder@gmail.com
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_email_aliases_tenant ON email_aliases (tenant_id);

-- 5. Durable Outbox & Worker Queue
CREATE TABLE outbox_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    job_type VARCHAR(32) NOT NULL CHECK (job_type IN ('webhook', 'sms', 'email')),
    payload JSONB NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending' 
        CHECK (status IN ('pending', 'running', 'completed', 'dead_letter')),
    attempts SMALLINT NOT NULL DEFAULT 0,
    max_attempts SMALLINT NOT NULL DEFAULT 6,
    next_run_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Partial index for high-performance outbox polling
CREATE INDEX idx_outbox_poll 
    ON outbox_jobs (next_run_at) 
    WHERE status = 'pending';
CREATE INDEX idx_outbox_tenant ON outbox_jobs (tenant_id, status);
```

---

## Go SDK Quickstart (`railbridge-go`)

The official Go SDK client provides typed access to the API endpoints and signature verification utilities.

### Installation

```bash
go get github.com/swe-labs/railbridge-go@v1.0.0
```

### Complete Implementation Example

```go
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/swe-labs/railbridge-go"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Initialize the client configured for production or Alwaysdata staging
	client := railbridge.NewClient(
		"rb_live_8f7b2c4e1d0a9f8e7d6c5b4a3f2e1d0c",
		railbridge.WithBaseURL("https://railbridge.alwaysdata.net"),
		railbridge.WithTimeout(8*time.Second),
	)

	// 1. Create a Hosted Checkout Session (Payment Rail)
	session, err := client.Payments.CreateSession(ctx, &railbridge.CreateSessionParams{
		ReferenceID:    "ORDER-4819",
		AmountMinor:    15000, // 150.00 BDT
		Currency:       "BDT",
		CustomerPhone:  "+8801812345678",
		ReturnURL:      "https://client-app.com/checkout/complete",
		IdempotencyKey: "pay-ORDER-4819",
	})
	if err != nil {
		log.Fatalf("failed to create payment session: %v", err)
	}
	fmt.Printf("Checkout URL generated: %s\n", session.CheckoutURL)

	// 2. Dispatch Transactional SMS (SMS Rail)
	smsResp, err := client.SMS.Send(ctx, &railbridge.SendSMSParams{
		To:             "+8801712345678",
		Message:        "Your verification code is 4821.",
		IdempotencyKey: "otp-ORDER-4819-attempt-1",
	})
	if err != nil {
		log.Fatalf("failed to send SMS: %v", err)
	}
	fmt.Printf("SMS Queued. Job ID: %s\n", smsResp.JobID)

	// 3. Send Transactional Email (Email Rail)
	emailResp, err := client.Email.Send(ctx, &railbridge.SendEmailParams{
		To:             "customer@example.com",
		Subject:        "Receipt for ORDER-4819",
		HTMLBody:       "<p>Your payment of <strong>BDT 150.00</strong> has been confirmed.</p>",
		IdempotencyKey: "receipt-ORDER-4819",
	})
	if err != nil {
		log.Fatalf("failed to send email: %v", err)
	}
	fmt.Printf("Email Queued. Job ID: %s\n", emailResp.JobID)
}

// WebhookHTTPHandler demonstrates signature verification on incoming callbacks
func WebhookHTTPHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 65536))
	if err != nil {
		http.Error(w, "Payload Too Large", http.StatusRequestEntityTooLarge)
		return
	}

	sigHeader := r.Header.Get("X-Railbridge-Signature")
	webhookSecret := os.Getenv("RAILBRIDGE_WEBHOOK_SECRET")

	event, err := railbridge.VerifyWebhook(body, sigHeader, webhookSecret)
	if err != nil {
		http.Error(w, fmt.Sprintf("Unauthorized: %v", err), http.StatusUnauthorized)
		return
	}

	// Process the authenticated event payload
	switch event.Type {
	case "payment.verified":
		log.Printf("Payment confirmed for Session ID: %s", event.Data.SessionID)
	case "sms.delivered":
		log.Printf("SMS confirmed delivered to: %s", event.Data.Recipient)
	}

	w.WriteHeader(http.StatusOK)
}
```

---

## Deployment Runbook

### Environment Configuration

Store these settings in a `.env` file in the working directory:

```ini
# Application Core Configuration
APP_ENV=production
APP_BASE_URL=https://railbridge.alwaysdata.net
PORT=8100
IP=0.0.0.0

# Database Storage: PostgreSQL (Production) or SQLite (Local Dev)
# SQLite:  sqlite://./data/railbridge.db?_journal_mode=WAL
# Postgres: postgres://user:password@postgresql-railbridge.alwaysdata.net:5432/railbridge_db
DATABASE_URL=postgres://user:password@postgresql-railbridge.alwaysdata.net:5432/railbridge_db

# Cryptographic Master Key (Must be 32 bytes / 64 hex characters)
# Generate using: openssl rand -hex 32
MASTER_ENCRYPTION_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef

# Transactional Email Engine Configuration
EMAIL_DRIVER=smtp
SMTP_HOST=smtp-railbridge.alwaysdata.net
SMTP_PORT=587
SMTP_USER=noreply@swe-labs.com
SMTP_PASS=your_smtp_secret_password

# Transactional SMS Engine Configuration
SMS_PRIMARY_DRIVER=android_bridge
SMS_FALLBACK_DRIVER=greenweb
ANDROID_BRIDGE_SECRET=generate_strong_alphanumeric_token
GREENWEB_API_TOKEN=your_greenweb_api_token
```

---

### 1. Local Development

Railbridge includes a zero-dependency sandbox mode utilizing an embedded SQLite database in WAL mode:

```bash
# Clone the repository
git clone https://github.com/swe-labs/railbridge.git
cd railbridge

# Launch the local development server with SQLite and Sandbox UI
go run ./cmd/railbridge serve --dev
```

Expected startup logs:

```text
INFO [Database] Initializing SQLite schema mode=WAL path=./data/railbridge.db
INFO [Outbox] Background worker queue initialized (concurrency=4)
INFO [Server] Local sandbox active at http://localhost:8080/_sandbox
INFO [Server] Railbridge API listening on http://127.0.0.1:8080
```

Inspect local mail and SMS deliveries inside your browser at `http://localhost:8080/_sandbox`.

---

### 2. Deploying to Alwaysdata

#### Step A: Compile Static Linux Executable

Compile the Go application without CGO dependencies so it can execute across any x86-64 Linux environment:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o railbridge ./cmd/railbridge
```

#### Step B: Transfer Artifacts via SFTP / SCP

Transfer the compiled binary and the production `.env` file to your Alwaysdata home directory:

```bash
scp railbridge .env railbridge@ssh-railbridge.alwaysdata.net:/home/railbridge/app/
```

#### Step C: Alwaysdata Admin Console Configuration

1. Log into the Alwaysdata Administration Dashboard.
2. Navigate to **Web > Sites > Add a site**.
3. **Addresses:** Set to `railbridge.alwaysdata.net` (and later add `api.railbridge.swe-labs.com`).
4. **Type:** Select **User program**.
5. **Command:** Set to `/home/railbridge/app/railbridge serve`.
6. **Working directory:** Set to `/home/railbridge/app`.
7. **Process environment:** Ensure default environment variables are exposed (Railbridge reads Alwaysdata's injected `$IP` and `$PORT`).
8. **TLS/SSL:** Leave default settings enabled. Alwaysdata provisions and attaches a Let's Encrypt certificate automatically.

---

### 3. Containerized Deployment with Docker

To deploy Railbridge version 1 inside an OCI container:

```bash
docker run -d \
  --name railbridge \
  --restart unless-stopped \
  -p 8080:8080 \
  --env-file .env \
  ghcr.io/swe-labs/railbridge:v1.0.0
```

---

## Horizontal Scaling & Outbox Processing

The modular monolith design scales across distinct stages without requiring application rewrites:

| Scale Tier                                     | Architecture                                                             | Operational Footprint                                                                                   | Code Modifications                                                      |
| ---------------------------------------------- | ------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------- |
| **Stage 1: MVP / Staging**               | Single binary process hosting HTTP API and Worker pool on Alwaysdata.    | Single Alwaysdata node + Alwaysdata Managed PostgreSQL + Android SIM Bridge.                            | None.                                                                   |
| **Stage 2: Custom Production Domain**    | Single binary process with high-availability DNS routing.                | Custom CNAME (`api.railbridge.swe-labs.com`) via Cloudflare DNS + commercial SMS aggregator failover. | Update`APP_BASE_URL` in `.env`.                                     |
| **Stage 3: Multi-Server Cluster**        | 2 to 10 Railbridge instances running behind a round-robin load balancer. | Replicated application servers (VPS or containers) connected to a central PostgreSQL database.          | None. Replicas coordinate through PostgreSQL`FOR UPDATE SKIP LOCKED`. |
| **Stage 4: Dedicated Node Partitioning** | Dedicated API nodes and separate worker background nodes.                | Node Group A:`railbridge serve --role=api`Node Group B: `railbridge serve --role=worker`            | None. Configured via CLI startup flag.                                  |

---

## Verification & Diagnostic Commands

Use these commands to verify operational health, index state, and queue mechanics:

### 1. Health Probe Verification

```bash
curl -i https://railbridge.alwaysdata.net/healthz
# Expected: HTTP/1.1 200 OK
# Body: {"status":"pass","version":"v1.0.0"}

curl -i https://railbridge.alwaysdata.net/readyz
# Expected: HTTP/1.1 200 OK
# Body: {"status":"ready","database":"connected"}
```

### 2. Verify Database Outbox Index Efficiency

Execute `EXPLAIN` inside the PostgreSQL console to confirm the queue index eliminates table scans:

```sql
EXPLAIN (ANALYZE, BUFFERS)
SELECT id, payload
FROM outbox_jobs
WHERE status = 'pending' AND next_run_at <= NOW()
ORDER BY next_run_at ASC
LIMIT 10
FOR UPDATE SKIP LOCKED;
```

**Target Execution Plan:**

```text
Limit  (cost=0.15..1.20 rows=10 width=88) (actual time=0.035..0.048 rows=0 loops=1)
  Buffers: shared hit=1
  ->  LockRows  (cost=0.15..10.55 rows=100 width=88) (actual time=0.034..0.046 rows=0 loops=1)
        ->  Index Scan using idx_outbox_poll on outbox_jobs  (cost=0.15..9.55 rows=100 width=88) (actual time=0.032..0.033 rows=0 loops=1)
Planning Time: 0.082 ms
Execution Time: 0.071 ms
```

---

## License

Railbridge is distributed under the **MIT License**. Engineered with care by [swe-labs](https://github.com/swe-labs).
