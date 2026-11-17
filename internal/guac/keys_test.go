package guac

import (
	"reflect"
	"testing"
)

func TestKeys(t *testing.T) {
	down := func(k int) KeyEvent { return KeyEvent{true, k} }
	up := func(k int) KeyEvent { return KeyEvent{false, k} }
	tests := []struct {
		names []string
		want  []KeyEvent
	}{
		{[]string{"enter"}, []KeyEvent{down(0xff0d), up(0xff0d)}},
		{[]string{"ENTER", "Tab"}, []KeyEvent{down(0xff0d), up(0xff0d), down(0xff09), up(0xff09)}},
		{[]string{"ctrl+c"}, []KeyEvent{down(0xffe3), down('c'), up('c'), up(0xffe3)}},
		{[]string{"Ctrl+Shift+V"}, []KeyEvent{down(0xffe3), down(0xffe1), down('V'), up('V'), up(0xffe1), up(0xffe3)}},
		{[]string{"alt+f"}, []KeyEvent{down(0xffe9), down('f'), up('f'), up(0xffe9)}},
		{[]string{"G"}, []KeyEvent{down('G'), up('G')}},
		{[]string{"g"}, []KeyEvent{down('g'), up('g')}},
		{[]string{"+"}, []KeyEvent{down('+'), up('+')}},
		{[]string{"ctrl++"}, []KeyEvent{down(0xffe3), down('+'), up('+'), up(0xffe3)}},
		{[]string{"é"}, []KeyEvent{down(0xe9), up(0xe9)}},
		{[]string{"€"}, []KeyEvent{down(0x10020ac), up(0x10020ac)}},
		{[]string{"f1", "F12"}, []KeyEvent{down(0xffbe), up(0xffbe), down(0xffc9), up(0xffc9)}},
		{[]string{"shift+pgup"}, []KeyEvent{down(0xffe1), down(0xff55), up(0xff55), up(0xffe1)}},
		{[]string{"space", "esc", "delete"}, []KeyEvent{down(0x20), up(0x20), down(0xff1b), up(0xff1b), down(0xffff), up(0xffff)}},
	}
	for _, tt := range tests {
		got, err := Keys(tt.names...)
		if err != nil {
			t.Errorf("Keys(%q): %v", tt.names, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Keys(%q) =\n%v\nwant\n%v", tt.names, got, tt.want)
		}
	}
	for _, bad := range []string{"", "ctrl+", "enterr", "ab", "ctrl+xy", "\xff"} {
		if _, err := Keys("enter", bad); err == nil {
			t.Errorf("Keys(%q): no error", bad)
		}
	}
	// A word that is no key name points to the names and to send; a modifier
	// with it does not.
	hint := "guac key --help lists the names, and guac send --no-enter types text"
	if _, err := Keys("/ERROR"); err == nil || err.Error() != `unknown key "/ERROR"; `+hint {
		t.Errorf("Keys(/ERROR): %v", err)
	}
	if _, err := Keys("ctrl+xy"); err == nil || err.Error() != `unknown key "ctrl+xy"` {
		t.Errorf("Keys(ctrl+xy): %v", err)
	}
}
