package assets

import "testing"

func TestDecidePaymentAsset(t *testing.T) {
	tests := []struct {
		name string
		risk int
		want Decision
	}{
		{"ordinary receipt is processed", 18, Decision{"process", "payment_asset_processing_queued"}},
		{"threshold enters review", 70, Decision{"manual_review", "risk_team_asset_ready"}},
		{"high risk enters review", 96, Decision{"manual_review", "risk_team_asset_ready"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Decide(PaymentAsset{RiskScore: test.risk})
			if got != test.want {
				t.Fatalf("Decide(%d) = %#v, want %#v", test.risk, got, test.want)
			}
		})
	}
}
