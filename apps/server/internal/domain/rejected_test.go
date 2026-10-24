package domain

import (
	"errors"
	"net/http"
	"testing"
)

func TestRejectedKeepsValidationCodeAndNamesTheCause(t *testing.T) {
	err := Rejected("field_too_long:collection.subtitle.de-DE")
	if err.Code != CodeValidation || err.HTTPStatus != http.StatusBadRequest {
		t.Fatalf("got code %s status %d", err.Code, err.HTTPStatus)
	}
	if err.Msg != "rejected: field_too_long:collection.subtitle.de-DE" {
		t.Fatalf("got msg %q", err.Msg)
	}
	var target *AppError
	if !errors.As(error(err), &target) || target.Code != CodeValidation {
		t.Fatal("Rejected must stay an *AppError with the validation code")
	}
}
