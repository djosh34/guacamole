// Package guac drives a Guacamole SSH session in a Chrome tab.
package guac

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/djosh34/guacamole/internal/cdp"
)

//go:embed page.js
var pageJS string

// Session is a tab that shows a Guacamole client.
type Session struct {
	cdp.Tab
	State   string // connected, disconnected, ...
	Name    string // connection name
	session string // CDP session of the tab
}

// Sessions returns the tabs that show a Guacamole client, connected or not.
func Sessions(ctx context.Context, c *cdp.Conn) ([]Session, error) {
	tabs, err := c.Tabs(ctx)
	if err != nil {
		return nil, err
	}
	var found []Session
	for _, tab := range tabs {
		// Every Guacamole client page has #/client/ in its URL. Other tabs
		// are not asked, so a hung or frozen tab cannot block guac.
		if !strings.Contains(tab.URL, "#/client/") {
			continue
		}
		s, ok, err := session(ctx, c, tab)
		if err != nil {
			return nil, fmt.Errorf("page %d (%s): %w", tab.Number, tab.URL, err)
		}
		if ok {
			found = append(found, s)
		}
	}
	return found, nil
}

func session(ctx context.Context, c *cdp.Conn, tab cdp.Tab) (Session, bool, error) {
	s := Session{Tab: tab}
	var err error
	if s.session, err = c.Attach(ctx, tab.ID); err != nil {
		return s, false, err
	}
	var v *struct{ State, Name string }
	if err := s.run(ctx, c, "session", nil, &v); err != nil {
		return s, false, err
	}
	if v == nil {
		return s, false, nil
	}
	s.State, s.Name = v.State, v.Name
	return s, true, nil
}

// Pick returns the session to work on: tab number page, or the only tab with
// a Guacamole client when page is 0.
func Pick(ctx context.Context, c *cdp.Conn, page int) (Session, error) {
	if page > 0 {
		tabs, err := c.Tabs(ctx)
		if err != nil {
			return Session{}, err
		}
		if page > len(tabs) {
			return Session{}, fmt.Errorf("there is no page %d; guac sessions lists the pages", page)
		}
		s, ok, err := session(ctx, c, tabs[page-1])
		if err == nil && !ok {
			err = fmt.Errorf("page %d shows no Guacamole session; guac sessions lists the pages", page)
		}
		return s, err
	}
	all, err := Sessions(ctx, c)
	if err != nil {
		return Session{}, err
	}
	switch len(all) {
	case 1:
		return all[0], nil
	case 0:
		return Session{}, errors.New("no tab shows a Guacamole session; open one in the browser")
	}
	var b strings.Builder
	b.WriteString("several tabs show a Guacamole session; pick one with --page N:\n")
	b.WriteString(strings.TrimRight(List(all), "\n"))
	return Session{}, errors.New(b.String())
}

// List formats sessions as aligned lines: page number, state, connection
// name and URL.
func List(sessions []Session) string {
	var stateWidth, nameWidth int
	for _, s := range sessions {
		stateWidth = max(stateWidth, len(s.State))
		nameWidth = max(nameWidth, len(s.Name))
	}
	var b strings.Builder
	for _, s := range sessions {
		fmt.Fprintf(&b, "%-3d %-*s  %-*s  %s\n", s.Number, stateWidth, s.State, nameWidth, s.Name, s.URL)
	}
	return b.String()
}

// Screen returns the text on the terminal screen of the session, as a mouse
// drag over the whole terminal and copy give it.
func Screen(ctx context.Context, c *cdp.Conn, s Session) (string, error) {
	// guacd takes two clicks less than 300 ms apart as a double click, which
	// copies a word. Keep our clicks further apart than that.
	const clickGap = 350 * time.Millisecond

	var idle float64
	if err := s.run(ctx, c, "idle", nil, &idle); err != nil {
		return "", err
	}
	if err := sleep(ctx, clickGap-time.Duration(idle)*time.Millisecond); err != nil {
		return "", err
	}
	var copied struct {
		Text  *string
		Error string
	}
	err := s.run(ctx, c, "copy", nil, &copied)
	if err == nil && copied.Error != "" {
		err = errors.New(copied.Error)
	}
	if err == nil && copied.Text == nil {
		err = errors.New("the page returned no text")
	}
	if err == nil {
		// Click once to clear the highlight.
		err = sleep(ctx, clickGap)
		if err == nil {
			var cleared struct{ Error string }
			err = s.run(ctx, c, "clear", nil, &cleared)
			if err == nil && cleared.Error != "" {
				err = errors.New(cleared.Error)
			}
		}
		// A copy that the click causes still lands in our handler.
		if err == nil {
			err = sleep(ctx, 100*time.Millisecond)
		}
	}
	// Give the user's clipboard handler back, also after a failure.
	if rerr := s.run(ctx, c, "restore", nil, nil); err == nil {
		err = rerr
	}
	if err != nil {
		return "", err
	}
	return *copied.Text, nil
}

// Press sends key events to the session.
func Press(ctx context.Context, c *cdp.Conn, s Session, events []KeyEvent) error {
	pairs := make([][2]any, len(events))
	for i, e := range events {
		pairs[i] = [2]any{e.Pressed, e.Keysym}
	}
	return s.do(ctx, c, "keys", pairs)
}

// Send pastes text into the session: it puts the text on guacd's clipboard,
// then presses Ctrl+C when clear is set, Ctrl+Shift+V, and Enter when enter
// is set. When guacd refuses the text, Send presses no key.
func Send(ctx context.Context, c *cdp.Conn, s Session, text string, clear, enter bool) error {
	// A tab would make the shell complete a word. Ctrl+V before it makes
	// readline and the terminal take it as a plain tab.
	text = strings.ReplaceAll(text, "\t", "\x16\t")
	if err := s.do(ctx, c, "clipboard", text); err != nil {
		return err
	}
	if clear {
		if err := Press(ctx, c, s, ctrlC); err != nil {
			return err
		}
		// Text that comes right after Ctrl+C can lose its first character,
		// while the shell handles the interrupt.
		if err := s.settle(ctx, c); err != nil {
			return err
		}
	}
	keys := slices.Clone(pasteKeys)
	if enter {
		keys = append(keys, enterKey...)
	}
	return Press(ctx, c, s, keys)
}

// settle waits until the terminal has answered the last keys: until it has
// drawn something and then nothing for 150 ms. A program can ignore the keys
// and print nothing, so settle waits at most a second.
func (s Session) settle(ctx context.Context, c *cdp.Conn) error {
	start := time.Now()
	quiet := start
	last := 0
	for time.Since(start) < time.Second {
		var frames int
		if err := s.run(ctx, c, "frames", nil, &frames); err != nil {
			return err
		}
		if frames != last {
			last, quiet = frames, time.Now()
		} else if frames > 0 && time.Since(quiet) >= 150*time.Millisecond {
			return nil
		}
		if err := sleep(ctx, 20*time.Millisecond); err != nil {
			return err
		}
	}
	return nil
}

// Scroll scrolls the terminal by pages screens: up into the scrollback, or
// down. Any key but paste scrolls back to the bottom.
func Scroll(ctx context.Context, c *cdp.Conn, s Session, up bool, pages int) error {
	page := pageDown
	if up {
		page = pageUp
	}
	var events []KeyEvent
	for range pages {
		events = append(events, page...)
	}
	return Press(ctx, c, s, events)
}

// Reconnect clicks Reconnect in the disconnect dialog of the session and
// waits until it is connected again. Guacamole shows the dialog about a
// second after the connection ends, so Reconnect waits up to 5 seconds for
// the button.
func Reconnect(ctx context.Context, c *cdp.Conn, s Session) error {
	end := time.Now().Add(5 * time.Second)
	for {
		var res struct{ Error string }
		if err := s.run(ctx, c, "reconnect", nil, &res); err != nil {
			return err
		}
		if res.Error == "" {
			break
		}
		if time.Now().After(end) {
			return errors.New(res.Error)
		}
		if err := sleep(ctx, 100*time.Millisecond); err != nil {
			return err
		}
	}
	for {
		var v struct{ State, Dialog string }
		if err := s.run(ctx, c, "session", nil, &v); err != nil {
			return err
		}
		switch {
		case v.State == "connected":
			return nil
		case v.Dialog != "" && v.State != "connecting" && v.State != "waiting":
			return errors.New(v.Dialog)
		}
		if err := sleep(ctx, 200*time.Millisecond); err != nil {
			return fmt.Errorf("reconnect: %w (state %s)", err, v.State)
		}
	}
}

// do calls an operation of page.js that returns {error} on failure.
func (s Session) do(ctx context.Context, c *cdp.Conn, op string, arg any) error {
	var res struct{ Error string }
	if err := s.run(ctx, c, op, arg, &res); err != nil {
		return err
	}
	if res.Error != "" {
		return errors.New(res.Error)
	}
	return nil
}

// run calls an operation of page.js in the tab with a JSON argument.
func (s Session) run(ctx context.Context, c *cdp.Conn, op string, arg, result any) error {
	data, err := json.Marshal(arg)
	if err != nil {
		return err
	}
	return c.Eval(ctx, s.session, "("+pageJS+")('"+op+"', "+string(data)+")", result)
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
