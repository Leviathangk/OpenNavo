package domain

import (
	"errors"
	"net/http"
	"strings"
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

func TestRejectedDetailEchoesOnlySafeDetails(t *testing.T) {
	if got := RejectedDetail("unknown_field", "seoTitle").Msg; got != "rejected: unknown_field:seoTitle" {
		t.Fatalf("got %q", got)
	}
	if got := RejectedDetail("field_invalid", "translations.0.locale", "enum", "bad value").Msg; got != "rejected: field_invalid:translations.0.locale:enum" {
		t.Fatalf("got %q", got)
	}
	for _, unsafe := range []string{"", "two words", "https://example.test/x", "a:b", "<script>", strings.Repeat("a", 65)} {
		if got := RejectedDetail("unknown_field", unsafe).Msg; got != "rejected: unknown_field" {
			t.Fatalf("%q echoed as %q", unsafe, got)
		}
	}
}

func TestForbiddenKeepsCodeAndStatus(t *testing.T) {
	err := Forbidden(http.StatusConflict, "permission_required:catalog:package:edit")
	if err.Code != CodeForbidden || err.HTTPStatus != http.StatusConflict || err.Msg != "rejected: permission_required:catalog:package:edit" {
		t.Fatalf("got %+v", err)
	}
}
