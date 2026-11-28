package database

import (
	"errors"
	"strings"
	"testing"
)

func TestRedactRemovesThePasswordFromAnErrorThatQuotesTheURL(t *testing.T) {
	raw := "postgres://app:s3cret-pw@db.internal:5432/nitpick"
	err := redact(errors.New(`cannot parse "`+raw+`": bad option`), raw)
	if strings.Contains(err.Error(), "s3cret-pw") {
		t.Errorf("the password survived: %v", err)
	}
	if !strings.Contains(err.Error(), "REDACTED") || !strings.Contains(err.Error(), "db.internal") {
		t.Errorf("redaction should replace the password and keep the rest of the message: %v", err)
	}
	// A URL with no password changes nothing.
	plain := errors.New("connection refused")
	if got := redact(plain, "postgres://app@db/x"); got.Error() != "connection refused" {
		t.Errorf("an error with nothing to redact was changed: %v", got)
	}
}
