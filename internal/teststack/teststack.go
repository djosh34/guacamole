// Package teststack runs the Guacamole test stack in teststack/ and a
// Chromium for it. Each checkout of the repo gets its own compose project,
// ports and browser profile, so several stacks run on one host.
package teststack

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/djosh34/guacamole/internal/cdp"
)

// Stack is a running test stack.
type Stack struct {
	Root    string // repo root
	GuacURL string // http://127.0.0.1:PORT/guacamole/
	CDPURL  string // http://127.0.0.1:PORT
}

// Root returns the root of the repo that holds dir.
func Root(dir string) (string, error) {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "teststack", "compose.yaml")); err == nil {
			return d, nil
		}
		if filepath.Dir(d) == d {
			return "", fmt.Errorf("no teststack/compose.yaml in %s or above", dir)
		}
	}
}

func project(root string) string {
	return fmt.Sprintf("guac-%x", sha1.Sum([]byte(root)))[:13]
}

func stateDir(root string) string { return filepath.Join(root, ".teststack") }

// Compose runs docker compose on the stack's project and returns its output.
func Compose(ctx context.Context, root string, stdin []byte, args ...string) ([]byte, error) {
	args = append([]string{"compose", "-p", project(root), "-f", filepath.Join(root, "teststack", "compose.yaml")}, args...)
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("docker %s: %w: %s", strings.Join(args[3:], " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// Up starts the stack and a headless Chromium, and waits until both answer.
// $GUAC_VERSION picks the Guacamole version (default 1.6.0).
func Up(ctx context.Context, root string) (*Stack, error) {
	// No --build: every build gets a new image ID, and compose would then
	// recreate the SSH target and end the open sessions. Down removes the
	// image, so the next Up builds it again.
	if _, err := Compose(ctx, root, nil, "up", "-d"); err != nil {
		return nil, err
	}
	addr, err := Compose(ctx, root, nil, "port", "guacamole", "8080")
	if err != nil {
		return nil, err
	}
	s := &Stack{Root: root, GuacURL: "http://" + strings.TrimSpace(string(addr)) + "/guacamole/"}
	if err := waitHTTP(ctx, s.GuacURL, 3*time.Minute); err != nil {
		return nil, err
	}
	if s.CDPURL, err = startChromium(ctx, root); err != nil {
		return nil, err
	}
	return s, nil
}

// Down stops the Chromium and removes the stack with its volumes and the
// SSH target image.
func Down(ctx context.Context, root string) error {
	stopErr := stopChromium(root)
	_, err := Compose(ctx, root, nil, "down", "-v", "--rmi", "local", "--remove-orphans")
	return errors.Join(stopErr, err)
}

func waitHTTP(ctx context.Context, url string, limit time.Duration) error {
	end := time.Now().Add(limit)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			err = errors.New(resp.Status)
		}
		if time.Now().After(end) {
			return fmt.Errorf("%s did not answer: %w", url, err)
		}
		if err := sleep(ctx, time.Second); err != nil {
			return err
		}
	}
}

// Chrome returns the Chromium to run: $CHROME_BIN, a Chrome or Chromium on
// PATH, or Playwright's Chromium.
func Chrome() (string, error) {
	if c := os.Getenv("CHROME_BIN"); c != "" {
		return c, nil
	}
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	found, err := filepath.Glob(filepath.Join(home, ".cache/ms-playwright/chromium-*/chrome-linux*/chrome"))
	if err != nil || len(found) == 0 {
		return "", errors.New("no Chromium found; set CHROME_BIN")
	}
	return found[len(found)-1], nil
}

// startChromium starts a headless Chromium that outlives this process, with
// its profile in .teststack/, and returns its CDP URL. A Chromium that runs
// already is reused.
func startChromium(ctx context.Context, root string) (string, error) {
	dir := stateDir(root)
	profile := filepath.Join(dir, "chromium")
	portFile := filepath.Join(profile, "DevToolsActivePort")
	if pidAlive(root) {
		return cdpURL(portFile)
	}
	if err := os.MkdirAll(profile, 0o755); err != nil {
		return "", err
	}
	if err := os.Remove(portFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	chrome, err := Chrome()
	if err != nil {
		return "", err
	}
	logFile, err := os.Create(filepath.Join(dir, "chromium.log"))
	if err != nil {
		return "", err
	}
	defer logFile.Close()
	cmd := exec.Command(chrome,
		"--headless=new", "--no-sandbox", "--disable-gpu",
		"--remote-debugging-port=0", "--user-data-dir="+profile,
		"--no-first-run", "--no-default-browser-check",
		"--window-size=1280,800", "about:blank")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	pid := strconv.Itoa(cmd.Process.Pid)
	if err := os.WriteFile(filepath.Join(dir, "chromium.pid"), []byte(pid), 0o644); err != nil {
		return "", err
	}
	if err := cmd.Process.Release(); err != nil {
		return "", err
	}
	for end := time.Now().Add(30 * time.Second); ; {
		if url, err := cdpURL(portFile); err == nil {
			return url, nil
		}
		if time.Now().After(end) {
			return "", fmt.Errorf("Chromium did not start; see %s", logFile.Name())
		}
		if err := sleep(ctx, 100*time.Millisecond); err != nil {
			return "", err
		}
	}
}

func cdpURL(portFile string) (string, error) {
	data, err := os.ReadFile(portFile)
	if err != nil {
		return "", err
	}
	port, _, _ := strings.Cut(string(data), "\n")
	if _, err := strconv.Atoi(port); err != nil {
		return "", fmt.Errorf("bad %s: %q", portFile, data)
	}
	return "http://127.0.0.1:" + port, nil
}

func readPid(root string) (int, error) {
	data, err := os.ReadFile(filepath.Join(stateDir(root), "chromium.pid"))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func pidAlive(root string) bool {
	pid, err := readPid(root)
	return err == nil && syscall.Kill(pid, 0) == nil
}

func stopChromium(root string) error {
	pid, err := readPid(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return os.Remove(filepath.Join(stateDir(root), "chromium.pid"))
}

// View is the window size and page zoom of the browser tab. Zero values keep
// Chromium's default.
type View struct {
	Width, Height int     // window size in screen pixels
	Zoom          float64 // 1.25 is a page zoom of 125%
}

// Tab is an open browser tab with a CDP session that stays attached, so the
// tab keeps its View.
type Tab struct {
	Conn    *cdp.Conn
	ID      string
	Session string
}

// Close closes the tab and the connection. It returns once the tab is gone.
func (t *Tab) Close(ctx context.Context) error {
	err := t.Conn.Call(ctx, "", "Target.closeTarget", map[string]any{"targetId": t.ID}, nil)
	for end := time.Now().Add(10 * time.Second); err == nil; {
		tabs, terr := t.Conn.Tabs(ctx)
		if terr != nil {
			err = terr
			break
		}
		if !slices.ContainsFunc(tabs, func(tab cdp.Tab) bool { return tab.ID == t.ID }) {
			break
		}
		if time.Now().After(end) {
			err = errors.New("the tab did not close")
			break
		}
		err = sleep(ctx, 100*time.Millisecond)
	}
	return errors.Join(err, t.Conn.Close())
}

// Eval runs JavaScript in the tab.
func (t *Tab) Eval(ctx context.Context, expr string, result any) error {
	return t.Conn.Eval(ctx, t.Session, expr, result)
}

// WaitJS waits until the JavaScript expression is truthy.
func (t *Tab) WaitJS(ctx context.Context, what, expr string, limit time.Duration) error {
	end := time.Now().Add(limit)
	for {
		var ok bool
		err := t.Eval(ctx, "!!("+expr+")", &ok)
		if err == nil && ok {
			return nil
		}
		if time.Now().After(end) {
			return fmt.Errorf("timed out waiting for %s (last error: %v)", what, err)
		}
		if err := sleep(ctx, 200*time.Millisecond); err != nil {
			return err
		}
	}
}

// Open opens a new tab, logs in as guacuser and opens the SSH connection
// with the given name. It returns when the session is connected.
func (s *Stack) Open(ctx context.Context, connection string, view View) (*Tab, error) {
	c, err := cdp.Dial(ctx, s.CDPURL)
	if err != nil {
		return nil, err
	}
	t := &Tab{Conn: c}
	var target struct {
		TargetID string `json:"targetId"`
	}
	if err := c.Call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"}, &target); err != nil {
		return nil, errors.Join(err, c.Close())
	}
	t.ID = target.TargetID
	if err := t.open(ctx, s.GuacURL, connection, view); err != nil {
		return nil, errors.Join(err, t.Close(ctx))
	}
	return t, nil
}

func (t *Tab) open(ctx context.Context, guacURL, connection string, view View) error {
	var err error
	if t.Session, err = t.Conn.Attach(ctx, t.ID); err != nil {
		return err
	}
	if view.Width > 0 {
		zoom := view.Zoom
		if zoom == 0 {
			zoom = 1
		}
		// Page zoom is what this emulates: the page sees a window that is
		// zoom times smaller in CSS pixels, with zoom device pixels for each.
		err := t.Conn.Call(ctx, t.Session, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width":             int(float64(view.Width) / zoom),
			"height":            int(float64(view.Height) / zoom),
			"deviceScaleFactor": zoom,
			"mobile":            false,
		}, nil)
		if err != nil {
			return err
		}
	}
	if err := t.navigate(ctx, guacURL); err != nil {
		return err
	}
	// Guacamole keeps its login in localStorage, so a second tab is logged in
	// already.
	err = t.WaitJS(ctx, "login form or home page",
		`document.querySelector('.login-ui input[type=password]')?.offsetParent || document.querySelector('.home, .connection-list, guac-group-list')`,
		60*time.Second)
	if err != nil {
		return err
	}
	var needLogin bool
	if err := t.Eval(ctx, `!!document.querySelector('.login-ui input[type=password]')?.offsetParent`, &needLogin); err != nil {
		return err
	}
	if needLogin {
		err := t.Eval(ctx, `(() => {
			const set = (el, v) => { el.value = v; el.dispatchEvent(new Event('input', {bubbles: true})); };
			set(document.querySelector('.login-ui input[type=text]'), 'guacuser');
			set(document.querySelector('.login-ui input[type=password]'), 'guacpass');
			document.querySelector('.login-ui input[type=submit], .login-ui button[type=submit]').click();
		})()`, nil)
		if err != nil {
			return err
		}
		err = t.WaitJS(ctx, "login", `!document.querySelector('.login-ui input[type=password]')?.offsetParent`, 30*time.Second)
		if err != nil {
			return err
		}
	}
	// The client URL is #/client/ and base64 of: name NUL "c" NUL data source.
	id := base64.RawStdEncoding.EncodeToString([]byte(connection + "\x00c\x00default"))
	if err := t.navigate(ctx, guacURL+"#/client/"+id); err != nil {
		return err
	}
	err = t.WaitJS(ctx, "connected session", `(() => {
		const m = `+managedClientJS+`;
		return m && m.clientState.connectionState === 'CONNECTED' && m.client.getDisplay().getWidth() > 0;
	})()`, 60*time.Second)
	if err != nil {
		// Say what the page shows instead.
		var shows string
		if jsErr := t.Eval(ctx, `document.querySelector('.client-status-modal')?.innerText || document.body.innerText.slice(0, 300)`, &shows); jsErr != nil {
			shows = jsErr.Error()
		}
		return fmt.Errorf("%w; the page shows: %q", err, shows)
	}
	return nil
}

func (t *Tab) navigate(ctx context.Context, url string) error {
	return t.Conn.Call(ctx, t.Session, "Page.navigate", map[string]any{"url": url}, nil)
}

// managedClientJS is JavaScript for the ManagedClient that the tab shows.
const managedClientJS = `Object.values(angular.element(document.documentElement).injector().get('guacClientManager').getManagedClients())
	.find(m => location.hash.includes(m.id))`

// Type types text into the session as key presses. A newline is Enter.
func (t *Tab) Type(ctx context.Context, text string) error {
	var keysyms []int
	for _, r := range text {
		switch {
		case r == '\n':
			keysyms = append(keysyms, 0xff0d)
		case r <= 0xff:
			keysyms = append(keysyms, int(r))
		default:
			keysyms = append(keysyms, 0x01000000|int(r))
		}
	}
	return t.Eval(ctx, fmt.Sprintf(`(() => {
		const c = %s.client;
		for (const k of %s) { c.sendKeyEvent(1, k); c.sendKeyEvent(0, k); }
	})()`, managedClientJS, intsJS(keysyms)), nil)
}

// CtrlC sends Ctrl+C.
func (t *Tab) CtrlC(ctx context.Context) error {
	return t.Eval(ctx, `(() => {
		const c = `+managedClientJS+`.client;
		c.sendKeyEvent(1, 0xffe3); c.sendKeyEvent(1, 0x63); c.sendKeyEvent(0, 0x63); c.sendKeyEvent(0, 0xffe3);
	})()`, nil)
}

// Wheel scrolls the terminal by steps mouse wheel steps: up when steps is
// positive, down when negative. guacd scrolls 3 rows for each step.
func (t *Tab) Wheel(ctx context.Context, steps int) error {
	button := 8
	if steps < 0 {
		button, steps = 16, -steps
	}
	return t.Eval(ctx, fmt.Sprintf(`(() => {
		const m = %s;
		for (let i = 0; i < %d; i++) { m.tunnel.sendMessage('mouse', 100, 100, %d); m.tunnel.sendMessage('mouse', 100, 100, 0); }
	})()`, managedClientJS, steps, button), nil)
}

func intsJS(v []int) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = strconv.Itoa(n)
	}
	return "[" + strings.Join(s, ",") + "]"
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// CloseTabs closes every open tab.
func (s *Stack) CloseTabs(ctx context.Context) error {
	c, err := cdp.Dial(ctx, s.CDPURL)
	if err != nil {
		return err
	}
	tabs, err := c.Tabs(ctx)
	for _, t := range tabs {
		err = errors.Join(err, c.Call(ctx, "", "Target.closeTarget", map[string]any{"targetId": t.ID}, nil))
	}
	return errors.Join(err, c.Close())
}
