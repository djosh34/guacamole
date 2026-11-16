package cdp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

// fakeBrowser serves /json/version and a WebSocket that answers CDP calls
// with handle.
func fakeBrowser(t *testing.T, handle func(method string, params json.RawMessage) (any, string)) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/version" {
			ws := "ws" + strings.TrimPrefix(srv.URL, "http") + "/devtools/browser/x"
			if err := json.NewEncoder(w).Encode(map[string]string{"webSocketDebuggerUrl": ws}); err != nil {
				t.Error(err)
			}
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer c.CloseNow()
		for {
			_, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			var msg struct {
				ID     int64           `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(data, &msg); err != nil {
				t.Error(err)
				return
			}
			result, errMsg := handle(msg.Method, msg.Params)
			reply := map[string]any{"id": msg.ID, "result": result}
			if errMsg != "" {
				reply = map[string]any{"id": msg.ID, "error": map[string]any{"code": -32000, "message": errMsg}}
			}
			out, err := json.Marshal(reply)
			if err != nil {
				t.Error(err)
				return
			}
			// An event first, which the client must skip.
			event := []byte(`{"method":"Target.targetCreated","params":{}}`)
			if err := c.Write(r.Context(), websocket.MessageText, event); err != nil {
				return
			}
			if err := c.Write(r.Context(), websocket.MessageText, out); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTabsAndEval(t *testing.T) {
	srv := fakeBrowser(t, func(method string, params json.RawMessage) (any, string) {
		switch method {
		case "Target.getTargets":
			return map[string]any{"targetInfos": []map[string]string{
				{"targetId": "a", "type": "page", "title": "One", "url": "http://one/"},
				{"targetId": "b", "type": "service_worker", "url": "http://sw/"},
				{"targetId": "c", "type": "page", "url": "chrome://newtab/"},
				{"targetId": "d", "type": "page", "title": "Two", "url": "http://two/#/client/x"},
			}}, ""
		case "Target.attachToTarget":
			return map[string]string{"sessionId": "s1"}, ""
		case "Runtime.evaluate":
			var p struct{ Expression string }
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, err.Error()
			}
			if p.Expression == "boom" {
				return map[string]any{"exceptionDetails": map[string]any{"text": "Uncaught",
					"exception": map[string]string{"description": "Error: boom\n    at <anonymous>:1:1"}}}, ""
			}
			return map[string]any{"result": map[string]any{"value": map[string]int{"n": 42}}}, ""
		}
		return nil, "unknown method " + method
	})
	ctx := context.Background()
	c, err := Dial(ctx, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	tabs, err := c.Tabs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []Tab{{1, "a", "http://one/"}, {2, "d", "http://two/#/client/x"}}
	if len(tabs) != 2 || tabs[0] != want[0] || tabs[1] != want[1] {
		t.Fatalf("Tabs = %+v, want %+v", tabs, want)
	}
	session, err := c.Attach(ctx, "d")
	if err != nil || session != "s1" {
		t.Fatalf("Attach = %q, %v", session, err)
	}
	var v struct{ N int }
	if err := c.Eval(ctx, session, "1", &v); err != nil || v.N != 42 {
		t.Fatalf("Eval = %+v, %v", v, err)
	}
	if err := c.Eval(ctx, session, "boom", nil); err == nil || err.Error() != "Error: boom" {
		t.Fatalf("Eval(boom) error = %v", err)
	}
	if err := c.Call(ctx, "", "Nope.nope", nil, nil); err == nil || !strings.Contains(err.Error(), "unknown method") {
		t.Fatalf("Call(Nope.nope) error = %v", err)
	}
}

func TestDialErrors(t *testing.T) {
	ctx := context.Background()
	for _, endpoint := range []string{"", "localhost:9222", "ftp://x/"} {
		if _, err := Dial(ctx, endpoint); err == nil || !strings.Contains(err.Error(), "bad endpoint") {
			t.Errorf("Dial(%q) error = %v", endpoint, err)
		}
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if _, err := Dial(ctx, srv.URL); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("Dial(404 server) error = %v", err)
	}
}
