package fraud

import (
	"time"
)

// Request mirrors the POST /fraud-score body.
type Request struct {
	ID          string      `json:"id"`
	Transaction Transaction `json:"transaction"`
	Customer    Customer    `json:"customer"`
	Merchant    Merchant    `json:"merchant"`
	Terminal    Terminal    `json:"terminal"`
	LastTx      *LastTx     `json:"last_transaction"`
}

type Transaction struct {
	Amount       float64 `json:"amount"`
	Installments int     `json:"installments"`
	RequestedAt  string  `json:"requested_at"`
}

type Customer struct {
	AvgAmount      float64  `json:"avg_amount"`
	TxCount24h     int      `json:"tx_count_24h"`
	KnownMerchants []string `json:"known_merchants"`
}

type Merchant struct {
	ID        string  `json:"id"`
	MCC       string  `json:"mcc"`
	AvgAmount float64 `json:"avg_amount"`
}

type Terminal struct {
	IsOnline    bool    `json:"is_online"`
	CardPresent bool    `json:"card_present"`
	KmFromHome  float64 `json:"km_from_home"`
}

type LastTx struct {
	Timestamp     string  `json:"timestamp"`
	KmFromCurrent float64 `json:"km_from_current"`
}

func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func boolF64(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// buildVector transforms a transaction request into a 14-dimensional uint16 vector.
// Dimensions:
//
//	0:  amount / max_amount (clamped [0,1])
//	1:  installments / max_installments (clamped)
//	2:  (amount / avg_amount) / amount_vs_avg_ratio (clamped)
//	3:  hour_UTC / 23
//	4:  day_of_week / 6 (Mon=0, Sun=6)
//	5:  minutes_since_last / max_minutes (clamped), -1 if null
//	6:  km_from_last / max_km (clamped), -1 if null
//	7:  km_from_home / max_km (clamped)
//	8:  tx_count_24h / max_tx_count_24h (clamped)
//	9:  is_online (1 or 0)
//	10: card_present (1 or 0)
//	11: unknown_merchant (1 or 0)
//	12: mcc_risk (lookup, default 0.5)
//	13: merchant_avg_amount / max_merchant_avg_amount (clamped)
func (e *Engine) buildVector(req *Request) [dims]uint16 {
	n := &e.norm

	var t time.Time
	if req.Transaction.RequestedAt != "" {
		t, _ = time.Parse(time.RFC3339, req.Transaction.RequestedAt)
	}
	hourUTC := float64(t.UTC().Hour()) / 23.0
	dow := float64((int(t.UTC().Weekday())+6)%7) / 6.0

	dim5, dim6 := -1.0, -1.0
	if req.LastTx != nil {
		lastT, err := time.Parse(time.RFC3339, req.LastTx.Timestamp)
		if err == nil {
			minutes := t.Sub(lastT).Minutes()
			dim5 = clamp(minutes / n.MaxMinutes)
		}
		dim6 = clamp(req.LastTx.KmFromCurrent / n.MaxKm)
	}

	unknownMerchant := 1.0
	for _, m := range req.Customer.KnownMerchants {
		if m == req.Merchant.ID {
			unknownMerchant = 0
			break
		}
	}

	mccRisk, ok := e.mccRisk[req.Merchant.MCC]
	if !ok {
		mccRisk = 0.5
	}

	raw := [dims]float64{
		clamp(req.Transaction.Amount / n.MaxAmount),
		clamp(float64(req.Transaction.Installments) / n.MaxInstallments),
		clamp((req.Transaction.Amount / req.Customer.AvgAmount) / n.AmountVsAvgRatio),
		hourUTC,
		dow,
		dim5,
		dim6,
		clamp(req.Terminal.KmFromHome / n.MaxKm),
		clamp(float64(req.Customer.TxCount24h) / n.MaxTxCount24h),
		boolF64(req.Terminal.IsOnline),
		boolF64(req.Terminal.CardPresent),
		unknownMerchant,
		mccRisk,
		clamp(req.Merchant.AvgAmount / n.MaxMerchantAvgAmt),
	}

	var vec [dims]uint16
	for i, v := range raw {
		vec[i] = encodeVal(v)
	}
	return vec
}
