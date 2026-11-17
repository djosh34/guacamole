//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/djosh34/guacamole/internal/teststack"
)

// must runs guac and fails the test unless it exits 0 and prints nothing.
func must(t *testing.T, stdin string, args ...string) {
	t.Helper()
	out, errOut, code := guacIn(t, stdin, args...)
	if code != 0 || out != "" || errOut != "" {
		t.Fatalf("guac %q: exit %d, stdout %q, stderr %q", args, code, out, errOut)
	}
}

// waitScreen runs guac screen until check passes, or fails the test.
func waitScreen(t *testing.T, what string, check func(string) bool) string {
	t.Helper()
	s, ok := eventually(t, check)
	if !ok {
		t.Fatalf("%s; the screen shows:\n%s", what, s)
	}
	return s
}

// lastLine is the last row of the screen with text, without spaces at its
// end.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n "), "\n")
	return strings.TrimRight(lines[len(lines)-1], " ")
}

// endsWith reports whether the screen ends with lines, then the prompt.
func endsWith(lines ...string) func(string) bool {
	want := "\n" + strings.Join(lines, "\n") + "\n" + strings.TrimSpace(prompt)
	return func(s string) bool { return strings.HasSuffix(strings.TrimRight(s, "\n "), want) }
}

func readFile(path string) (string, error) {
	out, err := teststack.Compose(context.Background(), stack.Root, nil, "exec", "-T", "ssh-target", "cat", path)
	return string(out), err
}

// waitFile waits until the file on the SSH target holds want.
func waitFile(t *testing.T, path, want string) {
	t.Helper()
	var got string
	var err error
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
		if got, err = readFile(path); err == nil && got == want {
			return
		}
	}
	t.Fatalf("%s: got %q (%v), want %q", path, got, err, want)
}

// noFile checks that the file on the SSH target does not show up.
func noFile(t *testing.T, path string) {
	t.Helper()
	time.Sleep(time.Second)
	if got, err := readFile(path); err == nil {
		t.Fatalf("%s exists and holds %q", path, got)
	}
}

func newID() string { return strconv.FormatInt(time.Now().UnixNano(), 36) }

var warnLine = regexp.MustCompile(`^\d\d:\d\d:\d\d WARN worker\[\d\]: tick seq=\d{6} queue=\d+$`)

// TestLogStream is the real use: follow a log stream through grep, read it,
// stop it, then look through a long log with tail, grep and scroll.
func TestLogStream(t *testing.T) {
	tab := open(t, "default", teststack.View{})
	height, width := termSize(t, tab)

	must(t, "", "send", "clear; logstream 0.02 | grep --line-buffered WARN")
	s := waitScreen(t, "no WARN lines", func(s string) bool { return strings.Count(s, " WARN ") >= 5 })
	for _, line := range strings.Split(s, "\n")[1:] {
		if line != "" && !warnLine.MatchString(line) {
			t.Fatalf("not a WARN line: %q\n%s", line, s)
		}
	}
	must(t, "", "key", "ctrl+c")
	waitScreen(t, "no prompt after guac key ctrl+c", func(s string) bool { return lastLine(s) == strings.TrimSpace(prompt) })

	// The Ctrl+C that send presses first stops a running stream too.
	must(t, "", "send", "logstream 0.02")
	waitScreen(t, "the stream did not start", func(s string) bool { return strings.Count(s, " tick seq=") >= 3 })
	must(t, "", "send", "echo stopped-$((40+2))")
	waitScreen(t, "the stream did not stop", endsWith("stopped-42"))
	time.Sleep(500 * time.Millisecond)
	if s := screen(t); !endsWith("stopped-42")(s) {
		t.Fatalf("the stream goes on after send:\n%s", s)
	}

	// grep and tail over a long log, then scroll back through the output.
	cmd := "grep ERROR /var/log/app/app.log | tail -n 150"
	must(t, "", "send", "clear; "+cmd)
	output := compose(t, "", "exec", "-T", "--user", "tester", "ssh-target", "sh", "-c", cmd)
	// clear leaves the output at the top of the screen.
	rows := rowsOf(append(strings.Split(strings.TrimSuffix(output, "\n"), "\n"), prompt), width)
	bottom := len(rows) - height
	expect := func(what string, k int) {
		t.Helper()
		want := render(rows[k : k+height])
		got, ok := eventually(t, func(s string) bool { return s == want })
		if !ok {
			t.Fatalf("%s: screen is rows %d of %d, want row %d:\n%s", what, window(rows, height, got), len(rows), k, got)
		}
	}
	expect("bottom", bottom)
	must(t, "", "scroll", "up")
	expect("scroll up", bottom-height)
	must(t, "", "scroll", "up", "2")
	expect("scroll up 2", bottom-3*height)
	must(t, "", "scroll", "down")
	expect("scroll down", bottom-2*height)
	must(t, "", "scroll", "down", "10")
	expect("scroll down 10", bottom)
	// A key scrolls back to the bottom.
	must(t, "", "scroll", "up", "2")
	expect("scroll up 2 again", bottom-2*height)
	must(t, "", "key", "x")
	waitScreen(t, "a key did not scroll to the bottom", func(s string) bool { return lastLine(s) == prompt+"x" })
}

func TestSend(t *testing.T) {
	open(t, "default", teststack.View{})
	id := newID()

	// Quotes, $ and backslashes in the arguments, joined with spaces.
	file := "/tmp/args-" + id
	must(t, "", "send", "printf '%s|'", `"x  y" 'q"q' "$((2+3))" 'back\slash' > `+file)
	waitFile(t, file, `x  y|q"q|5|back\slash|`)

	// Several lines from stdin: a heredoc with tabs, quotes, $, backslashes
	// and emoji. One newline at the end of stdin is dropped.
	file = "/tmp/heredoc-" + id
	body := "tab\there \"double\" 'single' $HOME `date` \\n back\\\\slash\n😀 é → 𝄞\n\tindented\n"
	must(t, "cat > "+file+" <<'EOF'\n"+body+"EOF\n", "send")
	waitFile(t, file, body)

	// stdin with -.
	file = "/tmp/dash-" + id
	must(t, "echo dash > "+file+"\n", "send", "-")
	waitFile(t, file, "dash\n")

	// A command longer than 4 KB.
	var words []string
	for i := 0; len(strings.Join(words, " ")) < 6000; i++ {
		words = append(words, fmt.Sprintf("w%04d", i))
	}
	long := strings.Join(words, " ")
	file = "/tmp/long-" + id
	must(t, "", "send", "echo "+long+" > "+file)
	waitFile(t, file, long+"\n")

	// --no-enter leaves the line for Enter.
	file = "/tmp/no-enter-" + id
	must(t, "", "send", "--no-enter", "echo later > "+file)
	noFile(t, file)
	must(t, "", "key", "enter")
	waitFile(t, file, "later\n")

	// --no-clear adds to the line; without it Ctrl+C clears the line first.
	file = "/tmp/no-clear-" + id
	must(t, "", "send", "--no-enter", "echo part1")
	must(t, "", "send", "--no-clear", " part2 > "+file)
	waitFile(t, file, "part1 part2\n")
	file = "/tmp/clear-" + id
	must(t, "", "send", "--no-enter", "garbage")
	must(t, "", "send", "echo clean > "+file)
	waitFile(t, file, "clean\n")

	// Answer a y/n prompt.
	file = "/tmp/yn-" + id
	must(t, "", "send", `read -p "Continue? [y/n] " a; echo "answer=$a" > `+file)
	waitScreen(t, "no y/n prompt", func(s string) bool { return lastLine(s) == "Continue? [y/n]" })
	must(t, "", "send", "--no-clear", "y")
	waitFile(t, file, "answer=y\n")
}

func TestKey(t *testing.T) {
	open(t, "default", teststack.View{})
	// Names ignore case, characters are sent as written.
	must(t, "", "key", "e", "c", "h", "o", "SPACE", "K", "e", "Y", "-", "1", "Enter")
	waitScreen(t, "guac key did not type the command", endsWith("KeY-1"))
	must(t, "", "send", "--no-enter", "echo abX")
	must(t, "", "key", "left", "backspace", "right", "Z", "enter")
	waitScreen(t, "left, backspace and right did not edit the line", endsWith("aXZ"))

	_, errOut, code := guac(t, "key", "ctrl+bogus")
	if code != 2 || !strings.Contains(errOut, `unknown key "ctrl+bogus"`) {
		t.Fatalf("guac key ctrl+bogus: exit %d: %s", code, errOut)
	}
}

// TestPasteRefused uses a connection with disable-paste: send fails and
// presses no key, so the old clipboard is not pasted.
func TestPasteRefused(t *testing.T) {
	open(t, "no-paste", teststack.View{})
	file := "/tmp/refused-" + newID()
	// guac screen copies the screen to guacd's clipboard, which a paste
	// would now insert.
	before := screen(t)
	_, errOut, code := guac(t, "send", "echo refused > "+file)
	if code != 1 || !strings.Contains(errOut, "refused") {
		t.Fatalf("guac send without paste: exit %d: %s", code, errOut)
	}
	t.Logf("guac send: %s", strings.TrimSpace(errOut))
	noFile(t, file)
	if s := screen(t); s != before {
		t.Fatalf("the screen changed after a refused send:\nbefore:\n%s\nafter:\n%s", before, s)
	}
}

func TestReconnect(t *testing.T) {
	open(t, "default", teststack.View{})
	_, errOut, code := guac(t, "reconnect")
	if code != 1 || !strings.Contains(errOut, "Reconnect") {
		t.Fatalf("guac reconnect on a connected session: exit %d: %s", code, errOut)
	}

	compose(t, "", "exec", "-T", "ssh-target", "pkill", "-u", "tester")
	waitDisconnected := func(args ...string) {
		t.Helper()
		var code int
		var errOut string
		for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(300 * time.Millisecond) {
			if _, errOut, code = guac(t, args...); code == 1 && strings.Contains(strings.ToLower(errOut), "disconnected") {
				break
			}
		}
		if code != 1 || !strings.Contains(strings.ToLower(errOut), "disconnected") || !strings.Contains(errOut, "Run guac reconnect.") {
			t.Fatalf("guac %s on a disconnected session: exit %d: %s", strings.Join(args, " "), code, errOut)
		}
		t.Logf("guac %s: %s", strings.Join(args, " "), strings.TrimSpace(errOut))
	}
	waitDisconnected("send", "echo lost")
	waitDisconnected("key", "enter")
	waitDisconnected("scroll", "up")

	must(t, "", "reconnect")
	out, _, _ := guac(t, "sessions")
	if !strings.Contains(out, " connected ") {
		t.Fatalf("guac sessions after reconnect:\n%s", out)
	}
	must(t, "", "send", "echo back-$((1+1))")
	waitScreen(t, "send after reconnect", endsWith("back-2"))
}
