package model

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrInvalidEmail is what an invalid address list reads as in a log.
var ErrInvalidEmail = errors.New("invalid email")

// AddressError is an error whose details name e-mail addresses. Error() leaves them out, so whatever
// logs the error writes no address; UserText gives the details for the person who corrects them
// (API answer, admin notification).
type AddressError struct {
	Safe   error // the text for logs; errors.Is matches it
	Detail string
}

func (e *AddressError) Error() string { return e.Safe.Error() }
func (e *AddressError) Unwrap() error { return e.Safe }

// UserText is the text of err for a person: with the addresses of an AddressError, else err.Error().
func UserText(err error) string {
	var v *VfeegError
	if errors.As(err, &v) {
		err = v.Err
	}
	var a *AddressError
	if errors.As(err, &a) {
		return a.Detail
	}
	return err.Error()
}

// emailPartRe is the shared address rule used across the eegfaktura
// suite (backend, eda-xp, billing, web): after trimming, exactly one
// address per part with an ASCII local part and a TLD of at least two
// letters — no TLD allowlist. Applied per ';'-separated part.
// Explicit a-zA-Z instead of (?i): RE2's case folding would also match
// non-ASCII case pairs like ſ (U+017F) or the Kelvin sign, which the
// JS/Java/Scala implementations of the same rule reject.
var emailPartRe = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)

// NormalizeEmailList trims outer unicode whitespace (incl. NBSP) around
// each ';'-separated part, drops empty parts and re-joins with ';'
// (no spaces) — the canonical storage and wire format. Returns "" when
// nothing remains.
func NormalizeEmailList(email string) string {
	parts := strings.Split(email, ";")
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			kept = append(kept, t)
		}
	}
	return strings.Join(kept, ";")
}

// ValidateEmailList normalizes the given address list and checks every
// part against the shared rule. An empty result (no address at all) is
// valid — a member without an e-mail is not an error. On success the
// normalized list is returned; on failure the offending part is named
// so tenant admins can correct the member data (an AddressError: the
// name stays out of the logs).
func ValidateEmailList(email string) (string, error) {
	normalized := NormalizeEmailList(email)
	if normalized == "" {
		return "", nil
	}
	for _, p := range strings.Split(normalized, ";") {
		if !emailPartRe.MatchString(p) {
			return "", &AddressError{Safe: ErrInvalidEmail, Detail: fmt.Sprintf("Die E-Mail-Adresse „%s“ ist ungültig.", p)}
		}
	}
	return normalized, nil
}
