package main

import (
	"errors"
	"slices"
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
		rest     []string
		noClear  bool
		noEnter  bool
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
		{args: []string{"send", "--no-clear", "--no-enter", "ls", "-l"}, cmd: "send", rest: []string{"ls", "-l"}, noClear: true, noEnter: true},
		{args: []string{"send"}, cmd: "send"},
		{args: []string{"send", "ls", "--no-enter"}, cmd: "send", rest: []string{"ls", "--no-enter"}},
		{args: []string{"screen", "--no-enter"}, usage: "no-enter"},
		{args: []string{"key", "ctrl+c", "Enter", "y"}, cmd: "key", rest: []string{"ctrl+c", "Enter", "y"}},
		{args: []string{"key"}, usage: "key needs a key"},
		{args: []string{"key", "ctrl+cc"}, usage: `unknown key "ctrl+cc"`},
		{args: []string{"scroll", "up"}, cmd: "scroll", rest: []string{"up"}},
		{args: []string{"scroll", "down", "3"}, cmd: "scroll", rest: []string{"down", "3"}},
		{args: []string{"scroll"}, usage: "up or down"},
		{args: []string{"scroll", "left"}, usage: "up or down"},
		{args: []string{"scroll", "up", "0"}, usage: "not a number"},
		{args: []string{"scroll", "up", "1", "2"}, usage: "up or down"},
		{args: []string{"reconnect"}, cmd: "reconnect"},
		{args: []string{"reconnect", "now"}, usage: "reconnect takes no arguments"},
		{args: []string{"help", "send"}, cmd: "send", help: true},
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
		if cmd != tt.cmd || o.page != tt.page || o.help != tt.help || (tt.endpoint != "" && o.endpoint != tt.endpoint) ||
			!slices.Equal(o.args, tt.rest) || o.noClear != tt.noClear || o.noEnter != tt.noEnter {
			t.Errorf("parse(%q) = %+v, %q; want %+v", tt.args, o, cmd, tt)
		}
	}
}

func TestRunHelp(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"screen", "--help"}, {"sessions", "-h"}} {
		var out strings.Builder
		if err := run(args, strings.NewReader(""), &out); err != nil {
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
	if err := run([]string{"screen"}, strings.NewReader(""), &strings.Builder{}); !errors.As(err, &u) {
		t.Errorf("run(screen) without endpoint: %v, want a usage error", err)
	}
}

func TestSendText(t *testing.T) {
	tests := []struct {
		args  []string
		stdin string
		want  string
	}{
		{[]string{"echo", "a  b"}, "ignored", "echo a  b"},
		{nil, "ls\n", "ls"},
		{[]string{"-"}, "a\nb\n\n", "a\nb\n"},
		{nil, "dir\r\n", "dir\r"},
		{nil, "no newline", "no newline"},
		{[]string{"-", "-"}, "ignored", "- -"},
		{[]string{""}, "ignored", ""},
	}
	for _, tt := range tests {
		got, err := sendText(tt.args, strings.NewReader(tt.stdin))
		if err != nil || got != tt.want {
			t.Errorf("sendText(%q, %q) = %q, %v; want %q", tt.args, tt.stdin, got, err, tt.want)
		}
	}
}
