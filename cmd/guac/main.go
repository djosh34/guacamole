// Command guac drives an Apache Guacamole SSH session that is open in Chrome.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/djosh34/guacamole/internal/cdp"
	"github.com/djosh34/guacamole/internal/guac"
)

const usage = `usage: guac [--endpoint URL] [--page N] COMMAND [ARGS]

Drives a Guacamole SSH session that is open in Chrome.

Commands:
  sessions   list the tabs that show a Guacamole session
  screen     print the text on the terminal screen
  send       paste a command and press Enter
  key        press keys, such as ctrl+c or enter
  scroll     scroll the terminal up or down by screens
  reconnect  reconnect a disconnected session
  help       show this help

Flags:
  --endpoint URL  Chrome DevTools endpoint (default $BROWSER_CDP_URL)
  --page N        use tab N, as numbered by sessions

Run 'guac COMMAND --help' for more about a command.
`

var commandHelp = map[string]string{
	"sessions": `usage: guac sessions [--endpoint URL]

Lists the tabs that show a Guacamole session, connected or not, one per line:
page number, state, connection name and URL.
`,
	"screen": `usage: guac screen [--endpoint URL] [--page N]

Prints the text on the terminal screen: the text that a mouse drag over the
whole terminal and copy give. When the terminal is scrolled up, that is the
part of the scrollback on the screen.
`,
	"send": `usage: guac send [--no-clear] [--no-enter] [TEXT...]

Pastes TEXT into the terminal: Ctrl+C first, to clear the line or stop what
runs, then the text, then Enter. The words of TEXT are joined with spaces.
Without TEXT, or with -, the text comes from stdin, without its last newline.
Text with newlines runs as several lines. A tab goes as Ctrl+V Tab, so the
shell inserts it instead of completing a word.

send prints nothing and does not wait for output; run guac screen for that.
Flags go before TEXT. At a prompt, Ctrl+C cancels it: answer with
guac key y enter, or send --no-clear. Ctrl+C also sets $? to 130: read the
status of the last command with guac send --no-clear 'echo $?'.

  --no-clear  do not press Ctrl+C first
  --no-enter  do not press Enter after

Examples:
  guac send kubectl get pods
  guac send --no-clear y
  guac send < script.sh
`,
	"key": `usage: guac key KEY...

Presses keys in order. A key is a single character, sent as written, or one
of these names: enter tab esc backspace delete up down left right home end
pgup pgdn space f1 to f12. Put ctrl+, alt+ or shift+ in front for a
modifier. Names ignore case. To type text, such as a search in less, use
guac send --no-clear --no-enter.

Examples:
  guac key ctrl+c
  guac key q
  guac key up enter
`,
	"scroll": `usage: guac scroll up|down [N]

Scrolls the terminal N screens (default 1) up into the scrollback or back
down. guac screen then prints that part. Any key, such as the Ctrl+C or
Enter of guac send, scrolls back to the bottom.
`,
	"reconnect": `usage: guac reconnect [--endpoint URL] [--page N]

Clicks Reconnect in the disconnect dialog of the session and waits until it
is connected again. Guacamole shows the dialog about a second after the
session ends, so right after guac send exit, reconnect waits for it. It does
not log in.
`,
	"help": usage,
}

// usageError is bad usage: exit code 2.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func main() {
	err := run(os.Args[1:], os.Stdin, os.Stdout)
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "guac: %v\n", err)
	var u usageError
	if errors.As(err, &u) {
		fmt.Fprintln(os.Stderr, "Run 'guac help' for usage.")
		os.Exit(2)
	}
	os.Exit(1)
}

type options struct {
	endpoint string
	page     int
	help     bool
	noClear  bool // send
	noEnter  bool // send
	args     []string
	keys     []guac.KeyEvent // key
	up       bool            // scroll
	screens  int             // scroll
}

func flags(o *options, cmd string) *flag.FlagSet {
	f := flag.NewFlagSet("guac", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&o.endpoint, "endpoint", o.endpoint, "")
	f.IntVar(&o.page, "page", o.page, "")
	f.BoolVar(&o.help, "help", false, "")
	f.BoolVar(&o.help, "h", false, "")
	if cmd == "send" {
		f.BoolVar(&o.noClear, "no-clear", false, "")
		f.BoolVar(&o.noEnter, "no-enter", false, "")
	}
	return f
}

func parse(args []string) (options, string, error) {
	o := options{endpoint: os.Getenv("BROWSER_CDP_URL")}
	global := flags(&o, "")
	if err := global.Parse(args); err != nil {
		return o, "", flagError(err)
	}
	rest := global.Args()
	if o.help || len(rest) == 0 {
		return o, "help", nil
	}
	cmd := rest[0]
	if _, ok := commandHelp[cmd]; !ok {
		return o, "", usageError{fmt.Sprintf("unknown command %q", cmd)}
	}
	local := flags(&o, cmd)
	if err := local.Parse(rest[1:]); err != nil {
		return o, "", flagError(err)
	}
	o.args = local.Args()
	if o.help {
		return o, cmd, nil
	}
	if cmd == "help" && len(o.args) == 1 {
		if topic := o.args[0]; commandHelp[topic] != "" {
			o.help, o.args = true, nil
			return o, topic, nil
		}
	}
	if o.page < 0 {
		return o, "", usageError{"--page must be a tab number from 1"}
	}
	switch cmd {
	case "send":
	case "key":
		if len(o.args) == 0 {
			return o, "", usageError{"key needs a key, such as enter or ctrl+c"}
		}
		var err error
		if o.keys, err = guac.Keys(o.args...); err != nil {
			return o, "", usageError{err.Error()}
		}
	case "scroll":
		var err error
		if o.up, o.screens, err = scrollArgs(o.args); err != nil {
			return o, "", err
		}
	default:
		if len(o.args) > 0 {
			return o, "", usageError{fmt.Sprintf("%s takes no arguments", cmd)}
		}
	}
	return o, cmd, nil
}

// flagError rewords the errors of the flag package.
func flagError(err error) usageError {
	msg := err.Error()
	if name, ok := strings.CutPrefix(msg, "flag provided but not defined: "); ok {
		return usageError{"unknown flag -" + name}
	}
	if name, ok := strings.CutPrefix(msg, "flag needs an argument: -"); ok {
		return usageError{fmt.Sprintf("--%s needs a value", name)}
	}
	if rest, ok := strings.CutPrefix(msg, "invalid value "); ok {
		value, name, _ := strings.Cut(rest, " for flag -")
		name, _, _ = strings.Cut(name, ":")
		return usageError{fmt.Sprintf("bad value %s for --%s", value, name)}
	}
	return usageError{msg}
}

// scrollArgs parses up|down [N].
func scrollArgs(args []string) (up bool, n int, err error) {
	if len(args) == 0 || len(args) > 2 || args[0] != "up" && args[0] != "down" {
		return false, 0, usageError{"scroll takes up or down and a number of screens"}
	}
	n = 1
	if len(args) == 2 {
		n, err = strconv.Atoi(args[1])
		if err != nil || n < 1 {
			return false, 0, usageError{fmt.Sprintf("scroll: %q is not a number of screens", args[1])}
		}
	}
	return args[0] == "up", n, nil
}

// sendText is the text for send: the arguments joined with spaces, or stdin
// without its last newline.
func sendText(args []string, stdin io.Reader) (string, error) {
	if len(args) > 0 && !(len(args) == 1 && args[0] == "-") {
		return strings.Join(args, " "), nil
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	return strings.TrimSuffix(string(data), "\n"), nil
}

// screenText is the copied screen as guac screen prints it: the rows without
// the blank ones at the bottom, each ending in a newline. The copy joins the
// rows with newlines and has none at the end.
func screenText(copied string) string {
	rows := strings.Split(copied, "\n")
	for len(rows) > 0 && strings.TrimSpace(rows[len(rows)-1]) == "" {
		rows = rows[:len(rows)-1]
	}
	if len(rows) == 0 {
		return ""
	}
	return strings.Join(rows, "\n") + "\n"
}

func run(args []string, stdin io.Reader, stdout io.Writer) (err error) {
	o, cmd, err := parse(args)
	if err != nil {
		return err
	}
	if cmd == "help" || o.help {
		_, err := io.WriteString(stdout, commandHelp[cmd])
		return err
	}
	// Read stdin before anything happens in the browser.
	var text string
	if cmd == "send" {
		if text, err = sendText(o.args, stdin); err != nil {
			return err
		}
	}
	if o.endpoint == "" {
		return usageError{"no browser endpoint; set BROWSER_CDP_URL or pass --endpoint"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := cdp.Dial(ctx, o.endpoint)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := c.Close(); err == nil {
			err = cerr
		}
	}()

	if cmd == "sessions" {
		all, err := guac.Sessions(ctx, c)
		if err != nil {
			return err
		}
		_, err = io.WriteString(stdout, guac.List(all))
		return err
	}
	s, err := guac.Pick(ctx, c, o.page)
	if err != nil {
		return err
	}
	switch cmd {
	case "screen":
		text, err := guac.Screen(ctx, c, s)
		if err != nil {
			return err
		}
		_, err = io.WriteString(stdout, screenText(text))
		return err
	case "send":
		return guac.Send(ctx, c, s, text, !o.noClear, !o.noEnter)
	case "key":
		return guac.Press(ctx, c, s, o.keys)
	case "scroll":
		return guac.Scroll(ctx, c, s, o.up, o.screens)
	case "reconnect":
		return guac.Reconnect(ctx, c, s)
	}
	return nil
}
