package api

import (
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"net/http"
)

var checkoutHTMLTemplate = template.Must(template.New("checkout").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Railbridge Secure Checkout</title>
  <style>
    :root {
      --bg: #0f172a;
      --card: #1e293b;
      --border: #334155;
      --text: #f8fafc;
      --muted: #94a3b8;
      --primary: #0284c7;
      --success: #10b981;
      --accent: #e11d48;
    }
    * { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
    body { background: var(--bg); color: var(--text); display: flex; align-items: center; justify-content: center; min-height: 100vh; padding: 1rem; }
    .card { background: var(--card); border: 1px solid var(--border); border-radius: 12px; width: 100%; max-width: 480px; padding: 2rem; box-shadow: 0 20px 25px -5px rgba(0,0,0,0.5); }
    .header { text-align: center; margin-bottom: 1.5rem; }
    .amount { font-size: 2.25rem; font-weight: 700; color: var(--text); margin: 0.5rem 0; }
    .ref { font-size: 0.875rem; color: var(--muted); }
    .channels { display: grid; grid-template-columns: 1fr 1fr 1fr; gap: 0.5rem; margin: 1.5rem 0; }
    .channel-btn { background: #0f172a; border: 1px solid var(--border); color: var(--text); padding: 0.75rem; border-radius: 8px; cursor: pointer; font-weight: 600; text-align: center; }
    .channel-btn.active { border-color: var(--primary); background: rgba(2, 132, 199, 0.15); color: #38bdf8; }
    .instruction { background: #0f172a; border: 1px solid var(--border); border-radius: 8px; padding: 1rem; margin-bottom: 1.5rem; font-size: 0.875rem; line-height: 1.5; }
    .copy-box { display: flex; justify-content: space-between; align-items: center; background: #1e293b; padding: 0.5rem 0.75rem; border-radius: 6px; margin: 0.5rem 0; font-family: monospace; font-size: 1rem; }
    .copy-btn { background: var(--primary); border: none; color: white; padding: 0.25rem 0.75rem; border-radius: 4px; cursor: pointer; font-size: 0.75rem; }
    .form-group { margin-bottom: 1rem; }
    label { display: block; font-size: 0.875rem; margin-bottom: 0.5rem; color: var(--muted); }
    input { width: 100%; padding: 0.75rem; background: #0f172a; border: 1px solid var(--border); border-radius: 6px; color: var(--text); font-size: 1rem; }
    input:focus { outline: none; border-color: var(--primary); }
    .submit-btn { width: 100%; background: var(--primary); color: white; border: none; padding: 0.875rem; border-radius: 8px; font-size: 1rem; font-weight: 600; cursor: pointer; transition: background 0.2s; }
    .submit-btn:hover { background: #0369a1; }
    .badge { display: inline-block; padding: 0.25rem 0.5rem; border-radius: 9999px; font-size: 0.75rem; font-weight: 600; }
    .badge-pending { background: rgba(234, 179, 8, 0.2); color: #facc15; }
    .badge-verified { background: rgba(16, 185, 129, 0.2); color: #34d399; }
    #status-msg { margin-top: 1rem; text-align: center; font-size: 0.875rem; }
  </style>
</head>
<body>
  <div class="card">
    <div class="header">
      <div class="badge badge-pending" id="status-badge">{{ .Status }}</div>
      <div class="amount">BDT {{ .FormattedAmount }}</div>
      <div class="ref">Order Reference: <strong>{{ .ExternalReferenceID }}</strong></div>
    </div>

    <div class="channels">
      <button class="channel-btn active" type="button" onclick="selectChannel('bkash')">bKash</button>
      <button class="channel-btn" type="button" onclick="selectChannel('nagad')">Nagad</button>
      <button class="channel-btn" type="button" onclick="selectChannel('rocket')">Rocket</button>
    </div>

    <div class="instruction" id="instruction-box">
      <p>1. Go to your <strong id="channel-name">bKash</strong> App or dial *247#</p>
      <p>2. Choose <strong>Send Money</strong> to the number below:</p>
      <div class="copy-box">
        <span id="merchant-num">+8801812345678</span>
        <button class="copy-btn" onclick="copyNumber()">Copy</button>
      </div>
      <p>3. Enter exact amount: <strong>BDT {{ .FormattedAmount }}</strong></p>
      <p>4. Enter the TrxID below to verify payment.</p>
    </div>

    <form id="verify-form" onsubmit="submitVerification(event)">
      <div class="form-group">
        <label for="customer_phone">Your Sender Phone Number</label>
        <input type="tel" id="customer_phone" required placeholder="01712345678" value="{{ .CustomerPhone }}">
      </div>
      <div class="form-group">
        <label for="transaction_id">Transaction ID (TrxID)</label>
        <input type="text" id="transaction_id" required placeholder="e.g. 9K72B8X9M1" style="text-transform: uppercase;">
      </div>
      <button type="submit" class="submit-btn" id="btn-submit">Confirm & Verify</button>
      <div id="status-msg"></div>
    </form>
  </div>

  <script>
    const sessionID = "{{ .ID }}";
    const returnURL = "{{ .ReturnURL }}";
    let currentChannel = "bkash";

    function selectChannel(name) {
      currentChannel = name;
      document.querySelectorAll('.channel-btn').forEach(btn => btn.classList.remove('active'));
      event.target.classList.add('active');
      document.getElementById('channel-name').innerText = name.toUpperCase();
    }

    function copyNumber() {
      const num = document.getElementById('merchant-num').innerText;
      navigator.clipboard.writeText(num);
      alert('Number copied: ' + num);
    }

    async function submitVerification(e) {
      e.preventDefault();
      const trx = document.getElementById('transaction_id').value.trim();
      const phone = document.getElementById('customer_phone').value.trim();
      const btn = document.getElementById('btn-submit');
      const msg = document.getElementById('status-msg');

      btn.disabled = true;
      btn.innerText = "Verifying...";
      msg.innerText = "";

      try {
        const res = await fetch('/v1/payments/sessions/' + sessionID + '/verify', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ channel: currentChannel, transaction_id: trx, customer_phone: phone })
        });
        const data = await res.json();
        if (data.status === 'verified') {
          msg.innerHTML = '<span style="color:#10b981;">Payment verified successfully! Redirecting...</span>';
          setTimeout(() => { window.location.href = returnURL; }, 1500);
        } else {
          msg.innerHTML = '<span style="color:#facc15;">Submitted. Awaiting automated SMS matching...</span>';
          pollStatus();
        }
      } catch (err) {
        msg.innerHTML = '<span style="color:#ef4444;">Verification error: ' + err.message + '</span>';
        btn.disabled = false;
        btn.innerText = "Confirm & Verify";
      }
    }

    function pollStatus() {
      const interval = setInterval(async () => {
        try {
          const res = await fetch('/v1/payments/sessions/' + sessionID);
          const data = await res.json();
          if (data.status === 'verified') {
            clearInterval(interval);
            document.getElementById('status-msg').innerHTML = '<span style="color:#10b981;">Payment Verified! Redirecting...</span>';
            setTimeout(() => { window.location.href = returnURL; }, 1200);
          }
        } catch (_) {}
      }, 3000);
    }
  </script>
</body>
</html>`))

type CheckoutViewData struct {
	ID                  string
	ExternalReferenceID string
	FormattedAmount     string
	Currency            string
	Status              string
	CustomerPhone       string
	ReturnURL           string
}

func (s *Server) handleHostedCheckout(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if sessionID == "" {
		http.Error(w, "Session ID required", http.StatusBadRequest)
		return
	}

	// Retrieve session across any tenant for hosted checkout view
	var sessionIDVal, extRef, status, phone, returnURL string
	var amountMinor int64
	query := `SELECT id, external_reference_id, amount_minor, status, customer_phone, return_url FROM payment_sessions WHERE id = ?`
	if s.Store.Dialect == "postgres" {
		query = `SELECT id, external_reference_id, amount_minor, status, customer_phone, return_url FROM payment_sessions WHERE id = $1`
	}

	var phoneNull sql.NullString
	err := s.Store.QueryRowContext(r.Context(), query, sessionID).Scan(&sessionIDVal, &extRef, &amountMinor, &status, &phoneNull, &returnURL)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Checkout session not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Failed to load checkout", http.StatusInternalServerError)
		return
	}

	if phoneNull.Valid {
		phone = phoneNull.String
	}

	data := CheckoutViewData{
		ID:                  sessionIDVal,
		ExternalReferenceID: extRef,
		FormattedAmount:     fmt.Sprintf("%.2f", float64(amountMinor)/100.0),
		Currency:            "BDT",
		Status:              status,
		CustomerPhone:       phone,
		ReturnURL:           returnURL,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = checkoutHTMLTemplate.Execute(w, data)
}
