package mfs

import (
	"testing"
)

func TestParseSMS_Bkash(t *testing.T) {
	tests := []struct {
		name        string
		sender      string
		body        string
		wantTrxID   string
		wantAmount  int64
		wantSender  string
		wantChannel string
	}{
		{
			name:        "Standard bKash Receive Money",
			sender:      "16247",
			body:        "You have received Tk 150.00 from 01812345678. Fee Tk 0.00. Balance Tk 5,230.12. TrxID 9K72B8X9M1 at 21/11/2026 14:30",
			wantTrxID:   "9K72B8X9M1",
			wantAmount:  15000,
			wantSender:  "+8801812345678",
			wantChannel: "bkash",
		},
		{
			name:        "bKash Receive Money With Commas",
			sender:      "bKash",
			body:        "You have received Tk 12,500.50 from +8801711223344. Fee Tk 0.00. Balance Tk 25,000.00. TrxID BL7A2X9K11 at 21/11/2026 15:00",
			wantTrxID:   "BL7A2X9K11",
			wantAmount:  1250050,
			wantSender:  "+8801711223344",
			wantChannel: "bkash",
		},
		{
			name:        "bKash Deposit Format",
			sender:      "16247",
			body:        "You have received deposit of Tk 500.00 from 01912345678. Fee Tk 0.00. Balance Tk 1,500.00. TrxID DP99812A1 at 20/11/2026 11:20",
			wantTrxID:   "DP99812A1",
			wantAmount:  50000,
			wantSender:  "+8801912345678",
			wantChannel: "bkash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := ParseSMS(tt.sender, tt.body)
			if err != nil {
				t.Fatalf("unexpected error parsing sms: %v", err)
			}
			if res.TransactionID != tt.wantTrxID {
				t.Errorf("TrxID: got %q, want %q", res.TransactionID, tt.wantTrxID)
			}
			if res.AmountMinor != tt.wantAmount {
				t.Errorf("AmountMinor: got %d, want %d", res.AmountMinor, tt.wantAmount)
			}
			if res.SenderPhone != tt.wantSender {
				t.Errorf("SenderPhone: got %q, want %q", res.SenderPhone, tt.wantSender)
			}
			if res.Channel != tt.wantChannel {
				t.Errorf("Channel: got %q, want %q", res.Channel, tt.wantChannel)
			}
		})
	}
}

func TestParseSMS_Nagad(t *testing.T) {
	sender := "16167"
	body := "Money Received. Amount: Tk 250.00. Sender: 01712345678. TxnID: 7K9B12XA. Balance: Tk 1,200.00. Date: 21/11/2026 14:32"

	res, err := ParseSMS(sender, body)
	if err != nil {
		t.Fatalf("failed to parse nagad SMS: %v", err)
	}

	if res.Channel != "nagad" {
		t.Errorf("expected channel nagad, got %s", res.Channel)
	}
	if res.TransactionID != "7K9B12XA" {
		t.Errorf("expected TxnID 7K9B12XA, got %s", res.TransactionID)
	}
	if res.AmountMinor != 25000 {
		t.Errorf("expected amount 25000, got %d", res.AmountMinor)
	}
	if res.SenderPhone != "+8801712345678" {
		t.Errorf("expected sender +8801712345678, got %s", res.SenderPhone)
	}
}

func TestParseSMS_Rocket(t *testing.T) {
	sender := "16216"
	body := "Cash In Tk 100.00 from 01812345678 successful. TxnId: RK8812A. Balance Tk 500.00"

	res, err := ParseSMS(sender, body)
	if err != nil {
		t.Fatalf("failed to parse rocket SMS: %v", err)
	}

	if res.Channel != "rocket" {
		t.Errorf("expected channel rocket, got %s", res.Channel)
	}
	if res.TransactionID != "RK8812A" {
		t.Errorf("expected TxnID RK8812A, got %s", res.TransactionID)
	}
	if res.AmountMinor != 10000 {
		t.Errorf("expected amount 10000, got %d", res.AmountMinor)
	}
}

func TestParseSMS_Invalid(t *testing.T) {
	_, err := ParseSMS("RandomPromo", "Get 50% discount on recharge today!")
	if err == nil {
		t.Fatal("expected error on promotional SMS, got nil")
	}
}
