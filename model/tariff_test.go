package model

import (
	"encoding/json"
	"testing"

	"gopkg.in/guregu/null.v4"
)

func zvtTariff() Tariff {
	return Tariff{
		Type:                  VZP,
		UseTimeTariff:         true,
		TimeTariff1Active:     true,
		TimeTariff1Name:       null.StringFrom("Tag"),
		TimeTariff1From:       null.StringFrom("06:00"),
		TimeTariff1To:         null.StringFrom("08:00"),
		TimeTariff1CentPerKWh: null.FloatFrom(22.5),
		TimeTariff2Active:     true,
		TimeTariff2Name:       null.StringFrom("Nacht"),
		TimeTariff2From:       null.StringFrom("20:00"),
		TimeTariff2To:         null.StringFrom("06:00"),
		TimeTariff2CentPerKWh: null.FloatFrom(5.5),
	}
}

func TestValidateTimeTariff(t *testing.T) {
	t.Run("valid incl. midnight crossing", func(t *testing.T) {
		tariff := zvtTariff()
		if err := tariff.ValidateTimeTariff(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("simple tariff skips validation", func(t *testing.T) {
		tariff := Tariff{Type: VZP, UseTimeTariff: false}
		if err := tariff.ValidateTimeTariff(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("EEG tariff rejected", func(t *testing.T) {
		tariff := zvtTariff()
		tariff.Type = EEG
		if err := tariff.ValidateTimeTariff(); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("freeKWh rejected in time mode", func(t *testing.T) {
		tariff := zvtTariff()
		tariff.FreeKWh = null.IntFrom(100)
		if err := tariff.ValidateTimeTariff(); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("from == to rejected", func(t *testing.T) {
		tariff := zvtTariff()
		tariff.TimeTariff1To = null.StringFrom("06:00")
		if err := tariff.ValidateTimeTariff(); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("off-raster time rejected", func(t *testing.T) {
		tariff := zvtTariff()
		tariff.TimeTariff1From = null.StringFrom("06:10")
		if err := tariff.ValidateTimeTariff(); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("missing price rejected", func(t *testing.T) {
		tariff := zvtTariff()
		tariff.TimeTariff1CentPerKWh = null.Float{}
		if err := tariff.ValidateTimeTariff(); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("overlap rejected", func(t *testing.T) {
		tariff := zvtTariff()
		// 05:00-07:00 ueberlappt das Mitternachtsfenster 20:00-06:00
		tariff.TimeTariff1From = null.StringFrom("05:00")
		tariff.TimeTariff1To = null.StringFrom("07:00")
		if err := tariff.ValidateTimeTariff(); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("overlap contained window rejected", func(t *testing.T) {
		tariff := zvtTariff()
		tariff.TimeTariff1From = null.StringFrom("21:00")
		tariff.TimeTariff1To = null.StringFrom("23:00")
		if err := tariff.ValidateTimeTariff(); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("adjacent windows allowed", func(t *testing.T) {
		tariff := zvtTariff()
		// 06:00-08:00 grenzt exakt an 20:00-06:00 (Bis exklusiv) - erlaubt
		if err := tariff.ValidateTimeTariff(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("single active window valid", func(t *testing.T) {
		tariff := zvtTariff()
		tariff.TimeTariff2Active = false
		tariff.TimeTariff2From = null.String{}
		tariff.TimeTariff2To = null.String{}
		tariff.TimeTariff2CentPerKWh = null.Float{}
		if err := tariff.ValidateTimeTariff(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

// konzept-beitrittsgebuehr.md, Tech Design B: amount >= 0 with at most 2 decimals, VAT 0..100, other types 0/false.
func TestValidateJoiningFee(t *testing.T) {
	tests := []struct {
		name    string
		tariff  Tariff
		wantErr bool
	}{
		{"EEG without fee", Tariff{Type: EEG}, false},
		{"EEG with cents", Tariff{Type: EEG, JoiningFee: 49.90, JoiningFeeUseVat: true, JoiningFeeVatInPercent: 20}, false},
		{"EEG one decimal", Tariff{Type: EEG, JoiningFee: 0.1}, false},
		{"EEG large amount with cents", Tariff{Type: EEG, JoiningFee: 12345678.91}, false},
		{"EEG VAT 100", Tariff{Type: EEG, JoiningFee: 50, JoiningFeeVatInPercent: 100}, false},
		{"EEG VAT without use flag", Tariff{Type: EEG, JoiningFee: 50, JoiningFeeVatInPercent: 13}, false},
		{"EEG negative", Tariff{Type: EEG, JoiningFee: -0.01}, true},
		{"EEG three decimals", Tariff{Type: EEG, JoiningFee: 49.999}, true},
		{"EEG largest amount of numeric(10,2)", Tariff{Type: EEG, JoiningFee: 99999999.99}, false},
		{"EEG above numeric(10,2) (review 28.10.2026: a database error instead of a message)", Tariff{Type: EEG, JoiningFee: 100000000}, true},
		{"EEG VAT above 100", Tariff{Type: EEG, JoiningFee: 50, JoiningFeeVatInPercent: 101}, true},
		{"EEG VAT negative", Tariff{Type: EEG, JoiningFee: 50, JoiningFeeVatInPercent: -1}, true},
		{"VZP zero", Tariff{Type: VZP}, false},
		{"VZP with fee", Tariff{Type: VZP, JoiningFee: 50}, true},
		{"EZP with VAT flag", Tariff{Type: EZP, JoiningFeeUseVat: true}, true},
		{"AKONTO with VAT rate", Tariff{Type: AKONTO, JoiningFeeVatInPercent: 20}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.tariff.ValidateJoiningFee()
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateJoiningFee() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Tariff JSON contract (caller eegfaktura-web): the three fields default to 0/false when absent and keep 2 decimals.
func TestTariffJoiningFeeJSON(t *testing.T) {
	var old Tariff
	if err := json.Unmarshal([]byte(`{"type":"EEG","name":"Beitrag","participantFee":2}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.JoiningFee != 0 || old.JoiningFeeUseVat || old.JoiningFeeVatInPercent != 0 {
		t.Fatalf("defaults: got %v/%v/%v", old.JoiningFee, old.JoiningFeeUseVat, old.JoiningFeeVatInPercent)
	}

	var fee Tariff
	if err := json.Unmarshal([]byte(`{"type":"EEG","name":"Beitrag","joiningFee":49.90,"joiningFeeUseVat":true,"joiningFeeVatInPercent":20}`), &fee); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(fee)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["joiningFee"] != 49.9 || got["joiningFeeUseVat"] != true || got["joiningFeeVatInPercent"] != float64(20) {
		t.Fatalf("round trip: %s", b)
	}
}

// What the web sends and receives for baseFee (261026-mm1): number or string, a whole amount looks as before.
func TestTariffBaseFeeJSON(t *testing.T) {
	cases := []struct {
		body string
		want FloatOrString
	}{
		{`{"baseFee":3}`, 3},
		{`{"baseFee":"3"}`, 3},
		{`{"baseFee":2.25}`, 2.25},
		{`{"baseFee":"2.25"}`, 2.25},
	}
	for _, c := range cases {
		var tariff Tariff
		if err := json.Unmarshal([]byte(c.body), &tariff); err != nil {
			t.Fatalf("%s: %v", c.body, err)
		}
		if tariff.BaseFee != c.want {
			t.Fatalf("%s: got %v", c.body, tariff.BaseFee)
		}
	}

	for _, body := range []string{`{"baseFee":""}`, `{"baseFee":"abc"}`, `{"baseFee":"NaN"}`, `{"baseFee":"Inf"}`, `{"baseFee":true}`} {
		var tariff Tariff
		if err := json.Unmarshal([]byte(body), &tariff); err == nil {
			t.Fatalf("%s: accepted as %v", body, tariff.BaseFee)
		}
	}

	var whole map[string]interface{}
	b, err := json.Marshal(Tariff{BaseFee: 3})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &whole); err != nil {
		t.Fatal(err)
	}
	if whole["baseFee"] != float64(3) {
		t.Fatalf("whole amount: %s", b)
	}
}
