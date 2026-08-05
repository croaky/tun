package main

import (
	"testing"

	"github.com/croaky/is"
)

func TestParseRules(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
		wantLen int
	}{
		{"valid single", []string{"POST", "/slack/events"}, false, 1},
		{"valid multiple", []string{"POST", "/slack/events", "GET", "/health"}, false, 2},
		{"lowercase normalized", []string{"post", "/slack/events"}, false, 1},
		{"empty", []string{}, true, 0},
		{"nil", nil, true, 0},
		{"odd count", []string{"POST"}, true, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			is := is.New(t)

			rules, err := parseRules(tt.args)
			if tt.wantErr {
				is.HasErr(err)
				return
			}
			is.NoErr(err)
			is.Eq(len(rules), tt.wantLen)
		})
	}
}

func TestAllowed(t *testing.T) {
	rules, err := parseRules([]string{"POST", "/slack/events", "GET", "/health"})
	is.New(t).NoErr(err)

	tests := []struct {
		method string
		path   string
		want   bool
	}{
		{"POST", "/slack/events", true},
		{"GET", "/health", true},
		{"GET", "/slack/events", false},
		{"POST", "/health", false},
		{"POST", "/other", false},
		{"DELETE", "/slack/events", false},
		{"POST", "/slack/events/", false},
		{"POST", "/Slack/Events", false},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			is := is.New(t)

			is.Eq(allowed(rules, tt.method, tt.path), tt.want)
		})
	}
}
