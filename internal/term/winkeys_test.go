package term

import (
	"bytes"
	"testing"
)

// Expected bytes are written from libuv v1.48 src/win/tty.c (get_vt100_fn_key
// and uv_process_tty_read_raw_req), not produced by the code under test.
func TestKeyTranslator(t *testing.T) {
	down := func(vk, ch uint16, state uint32) KeyEvent {
		return KeyEvent{KeyDown: true, RepeatCount: 1, VirtualKeyCode: vk, UnicodeChar: ch, ControlState: state}
	}
	for _, c := range []struct {
		name string
		ev   []KeyEvent
		want string
	}{
		{"letter", []KeyEvent{down(0x59, 'y', 0)}, "y"},
		{"enter is CR", []KeyEvent{down(0x0D, '\r', 0)}, "\r"},
		{"backspace is BS", []KeyEvent{down(0x08, 0x08, 0)}, "\x08"},
		{"escape key", []KeyEvent{down(0x1B, 0x1B, 0)}, "\x1b"},
		{"ctrl+c is byte 3", []KeyEvent{down(0x43, 0x03, leftCtrlPressed)}, "\x03"},
		{"ctrl+d is byte 4", []KeyEvent{down(0x44, 0x04, leftCtrlPressed)}, "\x04"},
		{"alt+b is ESC b", []KeyEvent{down(0x42, 'b', leftAltPressed)}, "\x1bb"},
		{"ctrl+alt has no ESC prefix", []KeyEvent{down(0x42, 'b', leftAltPressed|leftCtrlPressed)}, "b"},
		{"key up is ignored", []KeyEvent{{KeyDown: false, VirtualKeyCode: 0x59, UnicodeChar: 'y'}}, ""},
		{"alt key up with a char", []KeyEvent{{KeyDown: false, VirtualKeyCode: vkMenu, UnicodeChar: 'x', RepeatCount: 1}}, "x"},
		{"left arrow", []KeyEvent{down(vkLeft, 0, enhancedKey)}, "\x1b[D"},
		{"right arrow", []KeyEvent{down(vkRight, 0, enhancedKey)}, "\x1b[C"},
		{"up arrow", []KeyEvent{down(vkUp, 0, enhancedKey)}, "\x1b[A"},
		{"down arrow", []KeyEvent{down(vkDown, 0, enhancedKey)}, "\x1b[B"},
		{"home", []KeyEvent{down(vkHome, 0, enhancedKey)}, "\x1b[1~"},
		{"end", []KeyEvent{down(vkEnd, 0, enhancedKey)}, "\x1b[4~"},
		{"delete", []KeyEvent{down(vkDelete, 0, enhancedKey)}, "\x1b[3~"},
		{"ctrl+left", []KeyEvent{down(vkLeft, 0, leftCtrlPressed|enhancedKey)}, "\x1b[1;5D"},
		{"shift+ctrl+delete", []KeyEvent{down(vkDelete, 0, shiftPressed|leftCtrlPressed|enhancedKey)}, "\x1b[3;6~"},
		{"alt+left", []KeyEvent{down(vkLeft, 0, leftAltPressed|enhancedKey)}, "\x1b\x1b[D"},
		{"alt+numpad4 composing is dropped", []KeyEvent{down(0x64, 0, leftAltPressed)}, ""},
		{"numpad4 aliases left", []KeyEvent{down(0x64, 0, 0)}, "\x1b[D"},
		{"f1", []KeyEvent{down(vkF1, 0, 0)}, "\x1b[[A"},
		{"shift+f1", []KeyEvent{down(vkF1, 0, shiftPressed)}, "\x1b[23~"},
		{"ctrl+f12", []KeyEvent{down(vkF12, 0, leftCtrlPressed)}, "\x1b[24^"},
		{"unknown virtual key", []KeyEvent{down(0x07, 0, 0)}, ""},
		{"surrogate pair", []KeyEvent{down(0, 0xD83D, 0), down(0, 0xDE00, 0)}, "\U0001F600"},
		{"BMP character", []KeyEvent{down(0, 0x4E2D, 0)}, "中"},
		{"repeat count", []KeyEvent{{KeyDown: true, RepeatCount: 3, VirtualKeyCode: 0x59, UnicodeChar: 'y'}}, "yyy"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var k KeyTranslator
			var got []byte
			for _, ev := range c.ev {
				got = append(got, k.Translate(ev)...)
			}
			if !bytes.Equal(got, []byte(c.want)) {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
