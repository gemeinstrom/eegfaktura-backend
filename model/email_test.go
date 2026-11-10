package model

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// 261031-mm7: logs use Error(), which names no address; a person gets the address through UserText.
func TestValidateEmailList_AddressStaysOutOfTheLog(t *testing.T) {
	_, err := ValidateEmailList("anna@example.org;kaputt@adresse")
	if err == nil {
		t.Fatal("want an error")
	}
	if !errors.Is(err, ErrInvalidEmail) {
		t.Error("not ErrInvalidEmail")
	}
	for name, e := range map[string]error{"plain": err, "wrapped": fmt.Errorf("save: %w", err), "VfeegError": ErrParseJson(err)} {
		if strings.Contains(e.Error(), "kaputt@adresse") {
			t.Errorf("%s: Error() names the address: %s", name, e.Error())
		}
		if got := UserText(e); got != "Die E-Mail-Adresse „kaputt@adresse“ ist ungültig." {
			t.Errorf("%s: UserText = %q", name, got)
		}
	}
	if got := UserText(errors.New("other")); got != "other" {
		t.Errorf("UserText of another error = %q", got)
	}
}

func TestNormalizeEmailList(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain", "a@x.at", "a@x.at"},
		{"leading space", " a@x.at", "a@x.at"},
		{"trailing space", "a@x.at ", "a@x.at"},
		{"nbsp", " a@x.at ", "a@x.at"},
		{"multi with spaces", "a@x.at; b@y.at", "a@x.at;b@y.at"},
		{"empty part dropped", "a@x.at;;b@y.at", "a@x.at;b@y.at"},
		{"only whitespace", "  ", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeEmailList(tt.in); got != tt.want {
				t.Errorf("NormalizeEmailList(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestValidateEmailList(t *testing.T) {
	tests := []struct {
		name, in, want string
		wantErr        bool
	}{
		{"plain valid", "a@x.at", "a@x.at", false},
		{"heals outer whitespace", " a@x.at ", "a@x.at", false},
		{"heals nbsp", " a@x.at", "a@x.at", false},
		{"multi healed", " a@x.at ; b@y.at ", "a@x.at;b@y.at", false},
		{"modern gtld", "a@eeg.energy", "a@eeg.energy", false},
		{"uppercase ok", "A@X.AT", "A@X.AT", false},
		{"empty is valid (no address)", "", "", false},
		{"whitespace-only is valid (no address)", " ", "", false},
		{"garbage", "x", "", true},
		{"umlaut local part", "hedwig.schön@x.at", "", true},
		{"inner whitespace", "a b@x.at", "", true},
		{"one bad part rejects all", "a@x.at;x", "", true},
		{"missing tld", "a@x", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateEmailList(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateEmailList(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ValidateEmailList(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
