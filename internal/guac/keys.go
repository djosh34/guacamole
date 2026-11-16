package guac

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// KeyEvent is one key press or release, as X11 keysym.
type KeyEvent struct {
	Pressed bool
	Keysym  int
}

// X11 keysyms of the named keys.
var keyNames = map[string]int{
	"enter":     0xff0d,
	"tab":       0xff09,
	"esc":       0xff1b,
	"backspace": 0xff08,
	"delete":    0xffff,
	"up":        0xff52,
	"down":      0xff54,
	"left":      0xff51,
	"right":     0xff53,
	"home":      0xff50,
	"end":       0xff57,
	"pgup":      0xff55,
	"pgdn":      0xff56,
	"space":     0x20,
}

func init() {
	for i := 1; i <= 12; i++ {
		keyNames[fmt.Sprintf("f%d", i)] = 0xffbe + i - 1
	}
}

var modifiers = []struct {
	prefix string
	keysym int
}{
	{"ctrl+", ctrl},
	{"alt+", 0xffe9},
	{"shift+", shift},
}

// Keys turns key names such as "enter", "ctrl+c" or "y" into key events, in
// order. Names and modifiers ignore case; a single character is sent as
// written.
func Keys(names ...string) ([]KeyEvent, error) {
	var events []KeyEvent
	for _, name := range names {
		e, err := key(name)
		if err != nil {
			return nil, err
		}
		events = append(events, e...)
	}
	return events, nil
}

func key(name string) ([]KeyEvent, error) {
	var mods []int
	rest := name
	for {
		found := false
		for _, m := range modifiers {
			// "ctrl++" is Ctrl and the + key.
			if len(rest) > len(m.prefix) && strings.EqualFold(rest[:len(m.prefix)], m.prefix) {
				mods = append(mods, m.keysym)
				rest = rest[len(m.prefix):]
				found = true
			}
		}
		if !found {
			break
		}
	}
	k, ok := keyNames[strings.ToLower(rest)]
	if !ok {
		r, size := utf8.DecodeRuneInString(rest)
		if rest == "" || size != len(rest) || r == utf8.RuneError {
			return nil, fmt.Errorf("unknown key %q", name)
		}
		k = charKeysym(r)
	}
	return press(k, mods...), nil
}

// press presses the modifiers, presses and releases k, then releases the
// modifiers.
func press(k int, mods ...int) []KeyEvent {
	var events []KeyEvent
	for _, m := range mods {
		events = append(events, KeyEvent{true, m})
	}
	events = append(events, KeyEvent{true, k}, KeyEvent{false, k})
	for i := len(mods) - 1; i >= 0; i-- {
		events = append(events, KeyEvent{false, mods[i]})
	}
	return events
}

// charKeysym is the keysym of a character: Latin-1 as is, anything else as
// Unicode keysym.
func charKeysym(r rune) int {
	if r >= 0x20 && r <= 0xff {
		return int(r)
	}
	return 0x01000000 | int(r)
}

const (
	ctrl  = 0xffe3
	shift = 0xffe1
)

// Key strokes that send and scroll use.
var (
	ctrlC     = press('c', ctrl)
	pasteKeys = press('V', ctrl, shift) // guacd pastes its clipboard on Ctrl+Shift+V
	enterKey  = press(0xff0d)
	pageUp    = press(0xff55, shift) // guacd scrolls one screen on Shift+PgUp
	pageDown  = press(0xff56, shift)
)
