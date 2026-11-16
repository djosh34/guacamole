//go:build e2e

// Package e2e tests guac against the test stack in teststack/: a real
// Guacamole, an SSH target and a headless Chromium.
//
//	go test -tags e2e ./e2e
//
// GUAC_VERSION picks the Guacamole version (default 1.6.0). KEEP_STACK=1
// leaves the stack running afterwards.
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/djosh34/guacamole/internal/teststack"
)

var (
	stack   *teststack.Stack
	guacBin string
	version = os.Getenv("GUAC_VERSION")
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	root, err := teststack.Root(wd)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	dir, err := os.MkdirTemp("", "guac-e2e")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	guacBin = filepath.Join(dir, "guac")
	build := exec.Command("go", "build", "-o", guacBin, "./cmd/guac")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build guac: %v\n%s", err, out)
		return 1
	}
	if version == "" {
		version = "1.6.0"
	}
	stack, err = teststack.Up(ctx, root, version)
	if os.Getenv("KEEP_STACK") == "" {
		defer func() {
			if err := teststack.Down(context.Background(), root); err != nil {
				fmt.Fprintln(os.Stderr, "stop the test stack:", err)
			}
		}()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "start the test stack:", err)
		return 1
	}
	if err := stack.CloseTabs(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "close the open tabs:", err)
		return 1
	}
	return m.Run()
}

// guac runs the guac binary against the stack's Chromium.
func guac(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(guacBin, args...)
	cmd.Env = append(os.Environ(), "BROWSER_CDP_URL="+stack.CDPURL)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatalf("run guac: %v", err)
		}
	}
	return out.String(), errOut.String(), cmd.ProcessState.ExitCode()
}

// screen runs guac screen and returns the copied text, without the newline
// guac adds at the end.
func screen(t *testing.T, args ...string) string {
	t.Helper()
	out, errOut, code := guac(t, append([]string{"screen"}, args...)...)
	if code != 0 {
		t.Fatalf("guac screen: exit %d: %s", code, errOut)
	}
	text, ok := strings.CutSuffix(out, "\n")
	if !ok {
		t.Fatalf("guac screen output does not end with a newline: %q", out)
	}
	return text
}

// eventually runs guac screen until check passes or 15 seconds pass. It
// returns the last screen and whether check passed.
func eventually(t *testing.T, check func(string) bool) (string, bool) {
	t.Helper()
	end := time.Now().Add(15 * time.Second)
	for {
		s := screen(t)
		if check(s) || time.Now().After(end) {
			return s, check(s)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func compose(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	out, err := teststack.Compose(context.Background(), stack.Root, []byte(stdin), args...)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// row is one terminal row. wrapped is true when its line goes on in the next
// row.
type row struct {
	text    string
	wrapped bool
}

// rowsOf splits lines into terminal rows of cols cells. Every character in
// the tests takes one cell.
func rowsOf(lines []string, cols int) []row {
	var rows []row
	for _, line := range lines {
		r := []rune(line)
		for len(r) > cols {
			rows = append(rows, row{string(r[:cols]), true})
			r = r[cols:]
		}
		rows = append(rows, row{text: string(r)})
	}
	return rows
}

// render gives the text guacd copies for rows. Since 1.6 a wrapped line
// comes back as one line; before, every row ends with a newline.
func render(rows []row) string {
	joinWrapped := !strings.HasPrefix(version, "1.4") && !strings.HasPrefix(version, "1.5")
	var b strings.Builder
	for i, r := range rows {
		b.WriteString(r.text)
		if i < len(rows)-1 && !(joinWrapped && r.wrapped) {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// window finds the screen in rows: the index of its first row, or -1.
func window(rows []row, height int, screen string) int {
	for k := 0; k+height <= len(rows); k++ {
		if render(rows[k:k+height]) == screen {
			return k
		}
	}
	return -1
}

const prompt = "tester@ssh-target:~$ "

var views = []struct {
	connection string
	view       teststack.View
}{
	{"default", teststack.View{}},
	{"dejavu-10", teststack.View{}},
	// Leaves part of a row and a column over at the edges.
	{"liberation-14", teststack.View{Width: 1111, Height: 677}},
	{"noto-18", teststack.View{Width: 1024, Height: 640, Zoom: 1.25}},
	{"default", teststack.View{Width: 900, Height: 500, Zoom: 1.25}},
}

func TestScreen(t *testing.T) {
	for _, v := range views {
		name := fmt.Sprintf("%s/%dx%d@%g", v.connection, v.view.Width, v.view.Height, v.view.Zoom)
		t.Run(name, func(t *testing.T) { testScreen(t, v.connection, v.view) })
	}
}

func open(t *testing.T, connection string, view teststack.View) *teststack.Tab {
	t.Helper()
	ctx := context.Background()
	tab, err := stack.Open(ctx, connection, view)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := tab.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return tab
}

func typeText(t *testing.T, tab *teststack.Tab, text string) {
	t.Helper()
	if err := tab.Type(context.Background(), text); err != nil {
		t.Fatal(err)
	}
}

func wheel(t *testing.T, tab *teststack.Tab, steps int) {
	t.Helper()
	if err := tab.Wheel(context.Background(), steps); err != nil {
		t.Fatal(err)
	}
}

func testScreen(t *testing.T, connection string, view teststack.View) {
	tab := open(t, connection, view)
	id := strconv.FormatInt(time.Now().UnixNano(), 36)

	// The terminal size, as the shell sees it.
	typeText(t, tab, "stty size > /tmp/size-"+id+"\n")
	var height, width int
	for end := time.Now().Add(10 * time.Second); ; {
		out, err := teststack.Compose(context.Background(), stack.Root, nil, "exec", "-T", "ssh-target", "cat", "/tmp/size-"+id)
		if err == nil {
			if _, err := fmt.Sscan(string(out), &height, &width); err == nil {
				break
			}
		}
		if time.Now().After(end) {
			t.Fatalf("no terminal size: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("terminal %d rows, %d columns", height, width)

	out, _, code := guac(t, "sessions")
	if code != 0 || !strings.Contains(out, " connected ") || !strings.Contains(out, connection) {
		t.Fatalf("guac sessions: exit %d:\n%s", code, out)
	}

	// 1300 numbered lines, more than the 1000 rows of scrollback. Every
	// seventh line is long and wraps, and the last one wraps too.
	var lines []string
	for i := 1; i <= 1300; i++ {
		line := fmt.Sprintf("L%04d café über straße → %d", i, i*i)
		if i%7 == 0 || i == 1300 {
			for len([]rune(line)) < 2*width+5 {
				line += fmt.Sprintf(" word%d", len(line))
			}
		}
		lines = append(lines, line)
	}
	compose(t, strings.Join(lines, "\n")+"\n", "exec", "-T", "ssh-target", "sh", "-c", "cat > /tmp/lines-"+id)
	typeText(t, tab, "clear; cat /tmp/lines-"+id+"\n")
	rows := rowsOf(append(lines, prompt), width)
	bottom := len(rows) - height

	expect := func(what string, k int) {
		t.Helper()
		want := render(rows[k : k+height])
		got, ok := eventually(t, func(s string) bool { return s == want })
		if !ok {
			t.Fatalf("%s: screen is rows %d of %d, want row %d:\ngot:\n%s\nwant:\n%s", what, window(rows, height, got), len(rows), k, got, want)
		}
	}
	expect("bottom", bottom)
	for i := 0; i < 3; i++ {
		if s := screen(t); s != render(rows[bottom:]) {
			t.Fatalf("screen call %d in a row differs:\n%s", i+2, s)
		}
	}
	wheel(t, tab, 1)
	expect("1 wheel step up", bottom-3)
	wheel(t, tab, 6)
	expect("7 wheel steps up", bottom-21)

	// The top of the scrollback: guac screen shows some rows of the output,
	// and scrolling further up does not change them.
	wheel(t, tab, 1000)
	time.Sleep(time.Second)
	top := screen(t)
	k := window(rows, height, top)
	if k < 0 || k >= bottom-21 {
		t.Fatalf("top of the scrollback: screen is not rows of the output (index %d):\n%s", k, top)
	}
	t.Logf("top of the scrollback is row %d of %d", k, len(rows))
	wheel(t, tab, 1)
	time.Sleep(500 * time.Millisecond)
	if s := screen(t); s != top {
		t.Fatalf("screen changed after scrolling past the top:\n%s", s)
	}
	wheel(t, tab, -1000)
	expect("back at the bottom", bottom)

	// After clear only the prompt is left. guacd leaves out some of the empty
	// rows at the end of the copy after a clear, so those are not compared.
	sameText := func(want string) func(string) bool {
		return func(s string) bool {
			return strings.TrimRight(s, "\n") == want && strings.Count(s, "\n") < height
		}
	}
	typeText(t, tab, "clear\n")
	if got, ok := eventually(t, sameText(prompt)); !ok {
		t.Fatalf("after clear:\n%q", got)
	}

	// grep and tail over a long log file.
	cmd := "grep ERROR /var/log/app/app.log | tail -n 5"
	typeText(t, tab, cmd+"\n")
	output := compose(t, "", "exec", "-T", "--user", "tester", "ssh-target", "sh", "-c", cmd)
	all := append([]string{prompt + cmd}, strings.Split(strings.TrimSuffix(output, "\n"), "\n")...)
	wantGrep := render(rowsOf(append(all, prompt), width))
	if got, ok := eventually(t, sameText(wantGrep)); !ok {
		t.Fatalf("grep | tail:\ngot:\n%s\nwant:\n%s", got, wantGrep)
	}

	// A log stream that never ends. Each screen shows whole log lines in
	// order, then Ctrl+C stops the stream.
	typeText(t, tab, "clear; logstream 0.05\n")
	// Wait until the stream has scrolled the command line off the screen.
	if got, ok := eventually(t, func(s string) bool { return strings.Count(s, " tick seq=") >= height }); !ok {
		t.Fatalf("the log stream did not fill the screen:\n%s", got)
	}
	logLine := regexp.MustCompile(`^\d\d:\d\d:\d\d (INFO|WARN) worker\[\d\]: tick seq=(\d{6}) queue=\d+$`)
	for i := 0; i < 3; i++ {
		// While lines scroll in, the row where the drag started can move up
		// one row before the drag ends, so the copy may have one row more.
		s := screen(t)
		last := -1
		seen := 0
		all := strings.Split(s, "\n")
		if len(all) > height {
			t.Logf("log stream screen %d has %d rows", i+1, len(all))
		}
		if len(all) > height+1 {
			t.Fatalf("log stream screen %d has %d rows, want %d or %d:\n%s", i+1, len(all), height, height+1, s)
		}
		for j, line := range all {
			if line == "" && j == len(all)-1 || j == 0 && strings.HasSuffix(line, "logstream 0.05") {
				continue // the cursor row, or the command line at the top
			}
			m := logLine.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("log stream screen %d, row %d is not a log line: %q\n%s", i+1, j, line, s)
			}
			seq, err := strconv.Atoi(m[2])
			if err != nil {
				t.Fatal(err)
			}
			if last >= 0 && seq != last+1 {
				t.Fatalf("log stream screen %d: seq %d after %d\n%s", i+1, seq, last, s)
			}
			last = seq
			seen++
		}
		if seen < height-2 {
			t.Fatalf("log stream screen %d has %d log lines, want %d or more:\n%s", i+1, seen, height-2, s)
		}
		time.Sleep(300 * time.Millisecond)
	}
	if err := tab.CtrlC(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, ok := eventually(t, func(s string) bool {
		return strings.HasSuffix(s, "^C\n"+prompt) || strings.HasSuffix(s, "\n"+prompt)
	}); !ok {
		t.Fatalf("no prompt after Ctrl+C:\n%s", got)
	}
}

func TestSeveralTabs(t *testing.T) {
	open(t, "default", teststack.View{})
	open(t, "dejavu-10", teststack.View{})
	out, _, code := guac(t, "sessions")
	if code != 0 || strings.Count(out, " connected ") != 2 {
		t.Fatalf("guac sessions: exit %d:\n%s", code, out)
	}
	_, errOut, code := guac(t, "screen")
	if code != 1 || !strings.Contains(errOut, "--page") || strings.Count(errOut, " connected ") != 2 {
		t.Fatalf("guac screen with two sessions: exit %d: %s", code, errOut)
	}
	page := strings.Fields(strings.Split(out, "\n")[1])[0]
	if s := screen(t, "--page", page); !strings.Contains(s, prompt) {
		t.Fatalf("guac screen --page %s:\n%s", page, s)
	}
}

func TestNoSession(t *testing.T) {
	_, errOut, code := guac(t, "screen")
	if code != 1 || !strings.Contains(errOut, "no tab shows a Guacamole session") {
		t.Fatalf("guac screen without a session: exit %d: %s", code, errOut)
	}
}

func TestDisconnected(t *testing.T) {
	open(t, "default", teststack.View{})
	// Ending the SSH session makes Guacamole show its disconnect dialog.
	compose(t, "", "exec", "-T", "ssh-target", "pkill", "-u", "tester")
	var errOut string
	var code int
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(300 * time.Millisecond) {
		if _, errOut, code = guac(t, "screen"); code != 0 {
			break
		}
	}
	if code != 1 || !strings.Contains(strings.ToLower(errOut), "disconnected") {
		t.Fatalf("guac screen on a disconnected session: exit %d: %s", code, errOut)
	}
	t.Logf("guac screen: %s", strings.TrimSpace(errOut))
	out, _, _ := guac(t, "sessions")
	if !strings.Contains(out, "disconnected") {
		t.Fatalf("guac sessions does not list the disconnected session:\n%s", out)
	}
}
