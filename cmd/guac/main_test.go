package main

import (
	"errors"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	t.Setenv("BROWSER_CDP_URL", "http://env:9222")
	tests := []struct {
		args     []string
		cmd      string
		endpoint string
		page     int
		help     bool
		usage    string // part of the usage error, if any
	}{
		{args: nil, cmd: "help"},
		{args: []string{"--help"}, cmd: "help", help: true},
		{args: []string{"help"}, cmd: "help"},
		{args: []string{"help", "screen"}, cmd: "screen", help: true},
		{args: []string{"screen", "-h"}, cmd: "screen", help: true},
		{args: []string{"screen"}, cmd: "screen", endpoint: "http://env:9222"},
		{args: []string{"--endpoint", "ws://x/devtools/browser/1", "screen", "--page", "3"}, cmd: "screen", endpoint: "ws://x/devtools/browser/1", page: 3},
		{args: []string{"--page", "2", "sessions"}, cmd: "sessions", endpoint: "http://env:9222", page: 2},
		{args: []string{"bogus"}, usage: `unknown command "bogus"`},
		{args: []string{"screen", "extra"}, usage: "screen takes no arguments"},
		{args: []string{"screen", "--page", "x"}, usage: "-page"},
		{args: []string{"screen", "--page", "-1"}, usage: "--page must be"},
		{args: []string{"--nope"}, usage: "nope"},
	}
	for _, tt := range tests {
		o, cmd, err := parse(tt.args)
		if tt.usage != "" {
			var u usageError
			if !errors.As(err, &u) || !strings.Contains(err.Error(), tt.usage) {
				t.Errorf("parse(%q): error %v, want usage error with %q", tt.args, err, tt.usage)
			}
			continue
		}
		if err != nil {
			t.Errorf("parse(%q): %v", tt.args, err)
			continue
		}
		if cmd != tt.cmd || o.page != tt.page || o.help != tt.help || (tt.endpoint != "" && o.endpoint != tt.endpoint) {
			t.Errorf("parse(%q) = %+v, %q; want cmd %q, page %d, help %v, endpoint %q", tt.args, o, cmd, tt.cmd, tt.page, tt.help, tt.endpoint)
		}
	}
}

func TestRunHelp(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"screen", "--help"}, {"sessions", "-h"}} {
		var out strings.Builder
		if err := run(args, &out); err != nil {
			t.Errorf("run(%q): %v", args, err)
		}
		if !strings.HasPrefix(out.String(), "usage: guac") {
			t.Errorf("run(%q) printed %q", args, out.String())
		}
	}
}

func TestRunNoEndpoint(t *testing.T) {
	t.Setenv("BROWSER_CDP_URL", "")
	var u usageError
	if err := run([]string{"screen"}, &strings.Builder{}); !errors.As(err, &u) {
		t.Errorf("run(screen) without endpoint: %v, want a usage error", err)
	}
}
