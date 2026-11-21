package mfs

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	ErrUnknownProvider = errors.New("mfs: unknown or unsupported sender shortcode")
	ErrUnparseableSMS  = errors.New("mfs: unable to parse transaction details from SMS body")
)

// ParsedTransaction contains extracted details from an MFS operator confirmation SMS.
type ParsedTransaction struct {
	Channel       string // bkash | nagad | rocket
	TransactionID string
	AmountMinor   int64 // e.g. 150.00 BDT -> 15000
	SenderPhone   string
	RawBody       string
}

// bKash Regexes (Shortcode 16247 or "bKash")
// Standard personal / merchant notification formats:
// "You have received Tk 150.00 from 01812345678. Fee Tk 0.00. Balance Tk 5,230.12. TrxID 9K72B8X9M1 at 21/11/2026 14:30"
// "You have received deposit of Tk 500.00 from 01711223344. Fee Tk 0.00. Balance Tk 1,500.00. TrxID BL7A2X9K11 at ..."
var (
	bkashReceivedRegex = regexp.MustCompile(`(?i)You have received (?:deposit of )?Tk\s*([0-9,]+\.[0-9]{2}) from ([0-9+]+)\..*?TrxID\s+([A-Z0-9]+)`)
	bkashPaymentRegex  = regexp.MustCompile(`(?i)Payment of Tk\s*([0-9,]+\.[0-9]{2}) from ([0-9+]+) successful.*?TrxID\s+([A-Z0-9]+)`)
)

// Nagad Regexes (Shortcode 16167 or "Nagad")
// "Money Received. Amount: Tk 150.00. Sender: 01812345678. TxnID: 7K9B12XA. Balance: Tk 1,200.00. Date: 21/11/2026 14:32"
// "Payment Received. Amount: Tk 250.00. Sender: 01911223344. TxnID: 8L2A11ZX. ..."
var (
	nagadReceivedRegex = regexp.MustCompile(`(?i)(?:Money|Payment) Received\.\s*Amount:\s*Tk\s*([0-9,]+\.[0-9]{2})\.\s*Sender:\s*([0-9+]+)\.\s*TxnID:\s*([A-Z0-9]+)`)
)

// Rocket Regexes (Shortcode 16216 or "Rocket")
// "Cash In Tk 150.00 from 01812345678 successful. TxnId: 9812471201. Balance Tk 800.00"
var (
	rocketReceivedRegex = regexp.MustCompile(`(?i)(?:Cash In|Received) Tk\s*([0-9,]+\.[0-9]{2}) from ([0-9+]+) successful\.\s*TxnId:\s*([A-Z0-9]+)`)
)

// ParseSMS inspects the incoming sender shortcode and body text to extract financial metadata.
func ParseSMS(sender, body string) (*ParsedTransaction, error) {
	cleanSender := strings.ToUpper(strings.TrimSpace(sender))
	cleanBody := strings.TrimSpace(body)

	switch {
	case cleanSender == "16247" || strings.Contains(cleanSender, "BKASH"):
		return parseBkash(cleanBody)
	case cleanSender == "16167" || strings.Contains(cleanSender, "NAGAD"):
		return parseNagad(cleanBody)
	case cleanSender == "16216" || strings.Contains(cleanSender, "ROCKET"):
		return parseRocket(cleanBody)
	default:
		// Attempt fallback detection based on body content
		if strings.Contains(strings.ToUpper(cleanBody), "TRXID") && strings.Contains(strings.ToLower(cleanBody), "tk") {
			if tx, err := parseBkash(cleanBody); err == nil {
				return tx, nil
			}
		}
		if strings.Contains(strings.ToUpper(cleanBody), "TXNID") {
			if tx, err := parseNagad(cleanBody); err == nil {
				return tx, nil
			}
			if tx, err := parseRocket(cleanBody); err == nil {
				return tx, nil
			}
		}
		return nil, fmt.Errorf("%w: sender=%q", ErrUnknownProvider, sender)
	}
}

func parseBkash(body string) (*ParsedTransaction, error) {
	if matches := bkashReceivedRegex.FindStringSubmatch(body); len(matches) == 4 {
		minor, err := parseAmountMinor(matches[1])
		if err != nil {
			return nil, err
		}
		return &ParsedTransaction{
			Channel:       "bkash",
			AmountMinor:   minor,
			SenderPhone:   normalizePhone(matches[2]),
			TransactionID: strings.ToUpper(strings.TrimSpace(matches[3])),
			RawBody:       body,
		}, nil
	}

	if matches := bkashPaymentRegex.FindStringSubmatch(body); len(matches) == 4 {
		minor, err := parseAmountMinor(matches[1])
		if err != nil {
			return nil, err
		}
		return &ParsedTransaction{
			Channel:       "bkash",
			AmountMinor:   minor,
			SenderPhone:   normalizePhone(matches[2]),
			TransactionID: strings.ToUpper(strings.TrimSpace(matches[3])),
			RawBody:       body,
		}, nil
	}

	return nil, ErrUnparseableSMS
}

func parseNagad(body string) (*ParsedTransaction, error) {
	if matches := nagadReceivedRegex.FindStringSubmatch(body); len(matches) == 4 {
		minor, err := parseAmountMinor(matches[1])
		if err != nil {
			return nil, err
		}
		return &ParsedTransaction{
			Channel:       "nagad",
			AmountMinor:   minor,
			SenderPhone:   normalizePhone(matches[2]),
			TransactionID: strings.ToUpper(strings.TrimSpace(matches[3])),
			RawBody:       body,
		}, nil
	}

	return nil, ErrUnparseableSMS
}

func parseRocket(body string) (*ParsedTransaction, error) {
	if matches := rocketReceivedRegex.FindStringSubmatch(body); len(matches) == 4 {
		minor, err := parseAmountMinor(matches[1])
		if err != nil {
			return nil, err
		}
		return &ParsedTransaction{
			Channel:       "rocket",
			AmountMinor:   minor,
			SenderPhone:   normalizePhone(matches[2]),
			TransactionID: strings.ToUpper(strings.TrimSpace(matches[3])),
			RawBody:       body,
		}, nil
	}

	return nil, ErrUnparseableSMS
}

// parseAmountMinor converts decimal currency string "1,250.50" to integer minor units 125050.
func parseAmountMinor(raw string) (int64, error) {
	cleaned := strings.ReplaceAll(raw, ",", "")
	parts := strings.Split(cleaned, ".")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid currency format %q", raw)
	}

	major, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse major currency: %w", err)
	}

	centsStr := parts[1]
	if len(centsStr) == 1 {
		centsStr += "0"
	} else if len(centsStr) > 2 {
		centsStr = centsStr[:2]
	}

	cents, err := strconv.ParseInt(centsStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse minor currency: %w", err)
	}

	return (major * 100) + cents, nil
}

// normalizePhone normalizes a Bangladesh phone number to E.164 (+8801...) or standard 11 digits.
func normalizePhone(raw string) string {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "+")
	if strings.HasPrefix(cleaned, "880") {
		return "+" + cleaned
	}
	if strings.HasPrefix(cleaned, "01") && len(cleaned) == 11 {
		return "+88" + cleaned
	}
	return cleaned
}
