package facts

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"
)

type Record struct {
	ID       int64           `json:"id,omitempty"`
	Kind     string          `json:"kind"`
	Key      string          `json:"key"`
	Value    json.RawMessage `json:"value"`
	Evidence Proof           `json:"evidence"`
}

func strictValue(raw json.RawMessage, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		return ErrInvalid
	}
	return nil
}

// ValidateRecord is also the Hermes write gate. Every supported kind has a typed validator;
// merely possessing a source URL is not sufficient to publish an arbitrary document.
func ValidateRecord(r Record, now time.Time) error {
	if len(r.Key) == 0 || len(r.Key) > 160 || strings.TrimSpace(r.Key) != r.Key || strings.ContainsAny(r.Key, "\r\n\x00") || len(r.Value) > 65536 {
		return ErrInvalid
	}
	if err := r.Evidence.Validate(now); err != nil {
		return err
	}
	switch r.Kind {
	case "integration":
		var value Integration
		if strictValue(r.Value, &value) != nil {
			return ErrInvalid
		}
		return value.Validate(now)
	case "issue":
		var value Issue
		if strictValue(r.Value, &value) != nil {
			return ErrInvalid
		}
		return value.Validate()
	case "compatibility":
		var value Compatibility
		if strictValue(r.Value, &value) != nil {
			return ErrInvalid
		}
		return value.Validate()
	case "attribute":
		var value Attribute
		if strictValue(r.Value, &value) != nil {
			return ErrInvalid
		}
		return value.Validate()
	case "migration":
		var value Migration
		if strictValue(r.Value, &value) != nil {
			return ErrInvalid
		}
		return value.Validate(now)
	case "security":
		var value SecurityObservation
		if strictValue(r.Value, &value) != nil {
			return ErrInvalid
		}
		return value.Validate()
	case "price":
		var value PriceObservation
		if strictValue(r.Value, &value) != nil {
			return ErrInvalid
		}
		return value.Validate()
	default:
		return ErrInvalid
	}
}

type PriceObservation struct {
	Region       string  `json:"region"`
	Edition      string  `json:"edition"`
	Period       string  `json:"period"`
	Usage        string  `json:"usage"`
	DownloadFree bool    `json:"downloadFree"`
	Amount       *string `json:"amount,omitempty"`
	Currency     *string `json:"currency,omitempty"`
	PurchaseURL  string  `json:"purchaseUrl"`
	Channel      string  `json:"channel"`
}

type PriceFact struct {
	PriceObservation
	Evidence Proof `json:"evidence"`
}

var amountPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,9})(\.[0-9]{1,6})?$`)

func (p PriceObservation) Validate() error {
	if !regionPattern.MatchString(p.Region) || strings.TrimSpace(p.Edition) == "" || len(p.Edition) > 100 || !ValidSourceURL(p.PurchaseURL) || !slices.Contains([]string{"vendor", "app_store"}, p.Channel) {
		return ErrInvalid
	}
	if !slices.Contains([]string{"free", "one_time", "monthly", "yearly", "unknown"}, p.Period) || !slices.Contains(PriceTypes, p.Usage) {
		return ErrInvalid
	}
	if (p.Amount == nil) != (p.Currency == nil) {
		return ErrInvalid
	}
	if p.Amount != nil && (!amountPattern.MatchString(*p.Amount) || !currencyPattern.MatchString(*p.Currency)) {
		return ErrInvalid
	}
	if p.Usage == "free" && (p.Period != "free" || !p.DownloadFree || p.Amount != nil && strings.Trim(*p.Amount, "0.") != "") {
		return ErrInvalid
	}
	if p.Usage == "paid" && p.Period != "one_time" || p.Usage == "subscription" && p.Period != "monthly" && p.Period != "yearly" {
		return ErrInvalid
	}
	if p.Usage == "paid" && p.Amount != nil && strings.Trim(*p.Amount, "0.") == "" {
		return ErrInvalid
	}
	if p.Channel == "app_store" && !strings.HasPrefix(p.PurchaseURL, "https://apps.apple.com/") {
		return ErrInvalid
	}
	return nil
}

func (p PriceFact) OfferReady(now time.Time) bool {
	if p.Validate() != nil || p.Evidence.At(now).Status != "verified" || p.Amount == nil || p.Currency == nil || p.Region == "ZZ" || p.Edition == "unspecified" {
		return false
	}
	// Acquisition price alone does not establish the price of using the software.
	return p.Usage == "free" || p.Usage == "paid" || p.Usage == "subscription"
}
