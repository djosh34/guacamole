// Command guac drives an Apache Guacamole SSH session that is open in Chrome.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/djosh34/guacamole/internal/cdp"
	"github.com/djosh34/guacamole/internal/guac"
)

const usage = `usage: guac [--endpoint URL] [--page N] COMMAND

Drives a Guacamole SSH session that is open in Chrome.

Commands:
  sessions  list the tabs that show a Guacamole session
  screen    print the text on the terminal screen
  help      show this help

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
	"help": usage,
}

// usageError is bad usage: exit code 2.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func main() {
	err := run(os.Args[1:], os.Stdout)
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
}

func flags(o *options) *flag.FlagSet {
	f := flag.NewFlagSet("guac", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&o.endpoint, "endpoint", o.endpoint, "")
	f.IntVar(&o.page, "page", o.page, "")
	f.BoolVar(&o.help, "help", false, "")
	f.BoolVar(&o.help, "h", false, "")
	return f
}

func parse(args []string) (options, string, error) {
	o := options{endpoint: os.Getenv("BROWSER_CDP_URL")}
	global := flags(&o)
	if err := global.Parse(args); err != nil {
		return o, "", usageError{err.Error()}
	}
	rest := global.Args()
	if o.help || len(rest) == 0 {
		return o, "help", nil
	}
	cmd := rest[0]
	if _, ok := commandHelp[cmd]; !ok {
		return o, "", usageError{fmt.Sprintf("unknown command %q", cmd)}
	}
	local := flags(&o)
	if err := local.Parse(rest[1:]); err != nil {
		return o, "", usageError{err.Error()}
	}
	if cmd == "help" && local.NArg() == 1 {
		if _, ok := commandHelp[local.Arg(0)]; ok {
			o.help = true
			return o, local.Arg(0), nil
		}
	}
	if local.NArg() > 0 {
		return o, "", usageError{fmt.Sprintf("%s takes no arguments", cmd)}
	}
	if o.page < 0 {
		return o, "", usageError{"--page must be a tab number from 1"}
	}
	return o, cmd, nil
}

func run(args []string, stdout io.Writer) (err error) {
	o, cmd, err := parse(args)
	if err != nil {
		return err
	}
	if cmd == "help" || o.help {
		_, err := io.WriteString(stdout, commandHelp[cmd])
		return err
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

	switch cmd {
	case "sessions":
		all, err := guac.Sessions(ctx, c)
		if err != nil {
			return err
		}
		_, err = io.WriteString(stdout, guac.List(all))
		return err
	case "screen":
		s, err := guac.Pick(ctx, c, o.page)
		if err != nil {
			return err
		}
		text, err := guac.Screen(ctx, c, s)
		if err != nil {
			return err
		}
		// The copy joins the rows with newlines and has none at the end.
		_, err = io.WriteString(stdout, text+"\n")
		return err
	}
	return nil
}
