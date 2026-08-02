package server

import "testing"

func TestCountryCodeToFlag(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
		want    string
	}{
		{"DE", false, "🇩🇪"},
		{"de", false, "🇩🇪"},
		{"DEU", false, "🇩🇪"},
		{"USA", false, "🇺🇸"},
		{"UK", false, "🇬🇧"},
		{"XX", true, ""},
		{"ZZ", true, ""},
		{"EU", true, ""},
		{"A", true, ""},
		{"USAX", true, ""},
		{"12", true, ""},
	}
	for _, tc := range cases {
		got, err := countryCodeToFlag(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("%q: want error, got %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: unexpected err: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("%q: got %q want %q", tc.in, got, tc.want)
		}
	}
}
