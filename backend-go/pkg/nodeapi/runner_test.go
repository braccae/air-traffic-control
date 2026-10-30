package nodeapi

import "testing"

func TestIsTailnetRoute(t *testing.T) {
	cases := map[string]bool{
		"100.115.75.78":                true,
		"100.64.0.1":                   true,
		"100.127.255.254":              true,
		"the-freezer.leo-banjo.ts.net": true,
		"100.63.0.1":                   false,
		"100.128.0.1":                  false,
		"192.168.8.3":                  false,
		"10.0.0.5":                     false,
		"127.0.0.1":                    false,
		"":                             false,
	}
	for host, want := range cases {
		if got := isTailnetRoute(host); got != want {
			t.Errorf("isTailnetRoute(%q) = %v, want %v", host, got, want)
		}
	}
}
