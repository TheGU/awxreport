package awx

import "testing"

func TestExtractAnsibleHost(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"just yaml header", "---\n", ""},
		{"yaml with key", "---\nansible_host: 10.0.0.1\n", "10.0.0.1"},
		{"yaml without key", "---\nfoo: bar\n", ""},
		{"json with key", `{"ansible_host": "10.0.0.2"}`, "10.0.0.2"},
		{"non-string value gets stringified", "---\nansible_host: 12345\n", "12345"},
		{"malformed yaml returns empty (does not panic)", "---\n  : : : invalid", ""},
		{"key with extra vars", "---\nansible_user: admin\nansible_host: 10.0.0.3\nansible_port: 22\n", "10.0.0.3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractAnsibleHost(tc.in)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
