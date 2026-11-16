// Package cdp is a small Chrome DevTools Protocol client. It connects to the
// browser endpoint, lists tabs and runs JavaScript in them.
package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

// Conn is a connection to the browser endpoint.
type Conn struct {
	ws      *websocket.Conn
	mu      sync.Mutex
	next    int64
	pending map[int64]chan reply
	err     error
	done    chan struct{}
}

type reply struct {
	Result json.RawMessage
	Error  *struct {
		Message string `json:"message"`
	}
}

// Tab is a browser tab. Number is its position among the tabs, counted from 1,
// in the same order browser-use-cli uses for --page.
type Tab struct {
	Number int
	ID     string
	Title  string
	URL    string
}

// Dial connects to a browser endpoint: an http(s) URL of the DevTools server,
// or the ws(s) URL of the browser itself.
func Dial(ctx context.Context, endpoint string) (*Conn, error) {
	wsURL, err := browserURL(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	ws, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", endpoint, err)
	}
	ws.SetReadLimit(256 << 20)
	c := &Conn{ws: ws, pending: map[int64]chan reply{}, done: make(chan struct{})}
	go c.read()
	return c, nil
}

func browserURL(ctx context.Context, endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("bad endpoint %q", endpoint)
	}
	switch u.Scheme {
	case "ws", "wss":
		return endpoint, nil
	case "http", "https":
	default:
		return "", fmt.Errorf("bad endpoint %q: want http, https, ws or wss", endpoint)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/json/version"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("connect to %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("connect to %s: %s", endpoint, resp.Status)
	}
	var v struct {
		WS string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil || v.WS == "" {
		return "", fmt.Errorf("%s is not a Chrome DevTools endpoint", endpoint)
	}
	return v.WS, nil
}

func (c *Conn) read() {
	defer close(c.done)
	for {
		_, data, err := c.ws.Read(context.Background())
		if err != nil {
			c.mu.Lock()
			c.err = fmt.Errorf("browser connection lost: %w", err)
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			c.mu.Unlock()
			return
		}
		var msg struct {
			ID int64 `json:"id"`
			reply
		}
		if json.Unmarshal(data, &msg) != nil || msg.ID == 0 {
			continue // an event; nothing here listens to events
		}
		c.mu.Lock()
		ch := c.pending[msg.ID]
		delete(c.pending, msg.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- msg.reply
		}
	}
}

// Close closes the connection. Tabs stay open.
func (c *Conn) Close() error {
	err := c.ws.Close(websocket.StatusNormalClosure, "")
	<-c.done
	return err
}

// Call sends one CDP command and decodes its result into result, unless result
// is nil. session is empty for browser commands.
func (c *Conn) Call(ctx context.Context, session, method string, params, result any) error {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return c.err
	}
	c.next++
	id := c.next
	ch := make(chan reply, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	msg := map[string]any{"id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if session != "" {
		msg["sessionId"] = session
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if err := c.ws.Write(ctx, websocket.MessageText, data); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	select {
	case r, ok := <-ch:
		if !ok {
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.err
		}
		if r.Error != nil {
			return fmt.Errorf("%s: %s", method, r.Error.Message)
		}
		if result == nil {
			return nil
		}
		return json.Unmarshal(r.Result, result)
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("%s: %w", method, ctx.Err())
	}
}

// Tabs lists the open tabs. Pages of the browser itself (chrome:, devtools:
// and the like) are left out, as browser-use-cli does.
func (c *Conn) Tabs(ctx context.Context) ([]Tab, error) {
	var res struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
			Title    string `json:"title"`
			URL      string `json:"url"`
		} `json:"targetInfos"`
	}
	if err := c.Call(ctx, "", "Target.getTargets", nil, &res); err != nil {
		return nil, err
	}
	var tabs []Tab
	for _, t := range res.TargetInfos {
		if t.Type != "page" || internalURL(t.URL) {
			continue
		}
		tabs = append(tabs, Tab{Number: len(tabs) + 1, ID: t.TargetID, Title: t.Title, URL: t.URL})
	}
	return tabs, nil
}

func internalURL(u string) bool {
	u = strings.ToLower(u)
	for _, p := range []string{"chrome:", "chrome-untrusted:", "devtools:", "chrome-extension:", "edge:"} {
		if strings.HasPrefix(u, p) {
			return true
		}
	}
	return false
}

// Attach opens a session on a tab. Commands for the tab take its id.
func (c *Conn) Attach(ctx context.Context, tabID string) (string, error) {
	var res struct {
		SessionID string `json:"sessionId"`
	}
	err := c.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": tabID, "flatten": true}, &res)
	return res.SessionID, err
}

// Eval runs a JavaScript expression in a tab session, waits for a returned
// promise and decodes the value into result.
func (c *Conn) Eval(ctx context.Context, session, expr string, result any) error {
	var res struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	params := map[string]any{"expression": expr, "returnByValue": true, "awaitPromise": true}
	if err := c.Call(ctx, session, "Runtime.evaluate", params, &res); err != nil {
		return err
	}
	if e := res.Exception; e != nil {
		if e.Exception != nil && e.Exception.Description != "" {
			return errors.New(firstLine(e.Exception.Description))
		}
		return errors.New(e.Text)
	}
	if result == nil || len(res.Result.Value) == 0 {
		return nil
	}
	return json.Unmarshal(res.Result.Value, result)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
