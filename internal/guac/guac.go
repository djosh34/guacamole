// Package guac drives a Guacamole SSH session in a Chrome tab.
package guac

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
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
	if err := s.run(ctx, c, "session", &v); err != nil {
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
			return Session{}, fmt.Errorf("there is no page %d; the browser has %d tabs", page, len(tabs))
		}
		s, _, err := session(ctx, c, tabs[page-1])
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
	if err := s.run(ctx, c, "idle", &idle); err != nil {
		return "", err
	}
	if err := sleep(ctx, clickGap-time.Duration(idle)*time.Millisecond); err != nil {
		return "", err
	}
	var copied struct {
		Text  *string
		Error string
	}
	err := s.run(ctx, c, "copy", &copied)
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
			err = s.run(ctx, c, "clear", nil)
		}
		// A copy that the click causes still lands in our handler.
		if err == nil {
			err = sleep(ctx, 100*time.Millisecond)
		}
	}
	// Give the user's clipboard handler back, also after a failure.
	if rerr := s.run(ctx, c, "restore", nil); err == nil {
		err = rerr
	}
	if err != nil {
		return "", err
	}
	return *copied.Text, nil
}

// run calls an operation of page.js in the tab.
func (s Session) run(ctx context.Context, c *cdp.Conn, op string, result any) error {
	return c.Eval(ctx, s.session, "("+pageJS+")('"+op+"')", result)
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
