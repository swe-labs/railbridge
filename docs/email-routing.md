# Custom Domain Email Routing Specification

> **Virtual Inbound Email Aliasing, Cloudflare Routing & Outbound SMTP Setup**

## Reader Contract

- **Audience:** DevOps engineers and domain administrators configuring branded email routing without maintaining dedicated mail transfer agents (MTAs).
- **Assumed Knowledge:** DNS management (MX, TXT, SPF, DKIM) and cloud serverless workers.
- **Outcome:** You will be able to route any address on your custom domain (e.g. `business@swe-labs.com`) to a personal inbox (Gmail, Outlook, Proton, Yahoo) and send replies directly from that branded address.

---

## 1. High-Level Flow

```mermaid
flowchart TD
    Inbound["Incoming Email to business@swe-labs.com"] --> MX["Cloudflare Email Routing (MX Edge)"]
    MX -->|Direct Forward: Preserves SRS & SPF| Inboxes["Target Mailboxes: founder@gmail.com, team@outlook.com, finance@proton.me"]
    MX -->|Programmable Worker Hook| Worker["Cloudflare Email Worker (Transforms MIME to JSON)"]
    Worker -->|POST /v1/emails/inbound| Railbridge["Railbridge Inbound Ingestion Endpoint"]
    Railbridge --> Alias["Resolve Tenant Alias (email_aliases)"]
    Railbridge --> Audit["Audit Trail Record (message_logs)"]
    Railbridge --> Outbox["Enqueue Webhook Dispatch (outbox_jobs)"]

    style Inbound fill:#0f172a,stroke:#64748b,stroke-width:1.5px,color:#f8fafc
    style MX fill:#1e293b,stroke:#0284c7,stroke-width:2px,color:#f8fafc
    style Inboxes fill:#064e3b,stroke:#10b981,stroke-width:2px,color:#f8fafc
    style Worker fill:#312e81,stroke:#818cf8,stroke-width:2px,color:#f8fafc
    style Railbridge fill:#1e293b,stroke:#38bdf8,stroke-width:2px,color:#f8fafc
    style Alias fill:#0f172a,stroke:#38bdf8,stroke-width:1.5px,color:#f8fafc
    style Audit fill:#0f172a,stroke:#38bdf8,stroke-width:1.5px,color:#f8fafc
    style Outbox fill:#064e3b,stroke:#10b981,stroke-width:1.5px,color:#f8fafc
```

---

## 2. DNS Configuration (Cloudflare DNS)

Configure your custom domain (`swe-labs.com`) with the following DNS records:

### 2.1 Mail Exchange (MX) Records
| Type | Host | Points To | Priority |
|---|---|---|---|
| MX | `@` | `mx1.cloudflare.net` | 10 |
| MX | `@` | `mx2.cloudflare.net` | 20 |
| MX | `@` | `mx3.cloudflare.net` | 30 |

### 2.2 Sender Policy Framework (SPF) TXT Record
Authorize both Cloudflare Email Routing and your outbound SMTP provider (Alwaysdata, Resend, or Brevo):
```text
v=spf1 include:_spf.mx.cloudflare.net include:alwaysdata.com include:resend.com ~all
```

---

## 3. Cloudflare Email Worker Script

Deploy this worker in your Cloudflare dashboard to intercept incoming emails and relay them to Railbridge:

```typescript
export default {
  async email(message, env, ctx) {
    const rawEmail = await new Response(message.raw).text();
    
    const payload = {
      from: message.from,
      to: message.to,
      subject: message.headers.get("subject") || "(No Subject)",
      text: rawEmail,
    };

    const railbridgeEndpoint = "https://railbridge.alwaysdata.net/v1/emails/inbound";

    const response = await fetch(railbridgeEndpoint, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "User-Agent": "Cloudflare-Email-Worker/v1.0.0",
      },
      body: JSON.stringify(payload),
    });

    if (!response.ok) {
      console.error(`Railbridge webhook returned status: ${response.status}`);
      // Fallback: forward directly to safety inbox if webhook fails
      await message.forward("fallback@swe-labs.com");
      return;
    }

    // Forward original message preserving headers and SPF
    await message.forward("founder@gmail.com");
  }
};
```

---

## 4. Sending Outbound Replies from Personal Inboxes (Gmail / Outlook)

Recipients should see replies originating directly from `business@swe-labs.com` rather than your personal address:

### Setup in Gmail
1. Open Gmail **Settings > Accounts and Import > Send mail as**.
2. Click **Add another email address**.
3. **Name:** `SWE Labs Business`
4. **Email address:** `business@swe-labs.com`
5. Uncheck **Treat as an alias**. Click **Next Step**.
6. **SMTP Server:** `smtp-railbridge.alwaysdata.net` (or `smtp.resend.com`).
7. **Port:** `587` (Secured connection using TLS).
8. **Username:** `noreply@swe-labs.com` (your Alwaysdata/Resend SMTP username).
9. **Password:** `<your_smtp_password>`.
10. Click **Add Account** and enter the confirmation verification code sent to `business@swe-labs.com`.
