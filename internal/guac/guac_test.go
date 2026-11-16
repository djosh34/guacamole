package guac

import (
	"testing"

	"github.com/djosh34/guacamole/internal/cdp"
)

func TestList(t *testing.T) {
	got := List([]Session{
		{Tab: cdp.Tab{Number: 2, URL: "http://a/#/client/x"}, State: "connected", Name: "web"},
		{Tab: cdp.Tab{Number: 11, URL: "http://b/#/client/y"}, State: "disconnected", Name: "database"},
	})
	want := "2   connected     web       http://a/#/client/x\n" +
		"11  disconnected  database  http://b/#/client/y\n"
	if got != want {
		t.Errorf("List =\n%s\nwant\n%s", got, want)
	}
}
