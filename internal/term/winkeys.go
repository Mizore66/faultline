package term

import "unicode/utf16"

// This file is the platform-independent core of the Windows console raw input
// path: the translation of console KEY_EVENT_RECORDs to the bytes Node's libuv
// (src/win/tty.c, uv_process_tty_read_raw_req and get_vt100_fn_key, v1.48)
// delivers to a raw-mode TTY stream. It has no Windows dependency so it is
// unit-tested on every platform; the Win32 calls are in raw_windows.go.

// Win32 control-key state bits (dwControlKeyState).
const (
	rightAltPressed  = 0x0001
	leftAltPressed   = 0x0002
	rightCtrlPressed = 0x0004
	leftCtrlPressed  = 0x0008
	shiftPressed     = 0x0010
	enhancedKey      = 0x0100
)

// Virtual key codes used by the table.
const (
	vkMenu    = 0x12
	vkPrior   = 0x21
	vkNext    = 0x22
	vkEnd     = 0x23
	vkHome    = 0x24
	vkLeft    = 0x25
	vkUp      = 0x26
	vkRight   = 0x27
	vkDown    = 0x28
	vkInsert  = 0x2D
	vkDelete  = 0x2E
	vkClear   = 0x0C
	vkNumpad0 = 0x60
	vkNumpad9 = 0x69
	vkDecimal = 0x6E
	vkF1      = 0x70
	vkF12     = 0x7B
)

// KeyEvent is the fields of a console KEY_EVENT_RECORD libuv reads.
type KeyEvent struct {
	KeyDown        bool
	RepeatCount    uint16
	VirtualKeyCode uint16
	UnicodeChar    uint16
	ControlState   uint32
}

type fnKey struct{ normal, shift, ctrl, shiftCtrl string }

var fnKeys = map[uint16]fnKey{
	vkInsert:  {"[2~", "[2;2~", "[2;5~", "[2;6~"},
	vkEnd:     {"[4~", "[4;2~", "[4;5~", "[4;6~"},
	vkDown:    {"[B", "[1;2B", "[1;5B", "[1;6B"},
	vkNext:    {"[6~", "[6;2~", "[6;5~", "[6;6~"},
	vkLeft:    {"[D", "[1;2D", "[1;5D", "[1;6D"},
	vkClear:   {"[G", "[1;2G", "[1;5G", "[1;6G"},
	vkRight:   {"[C", "[1;2C", "[1;5C", "[1;6C"},
	vkUp:      {"[A", "[1;2A", "[1;5A", "[1;6A"},
	vkHome:    {"[1~", "[1;2~", "[1;5~", "[1;6~"},
	vkPrior:   {"[5~", "[5;2~", "[5;5~", "[5;6~"},
	vkDelete:  {"[3~", "[3;2~", "[3;5~", "[3;6~"},
	0x60:      {"[2~", "[2;2~", "[2;5~", "[2;6~"}, // VK_NUMPAD0
	0x61:      {"[4~", "[4;2~", "[4;5~", "[4;6~"}, // VK_NUMPAD1
	0x62:      {"[B", "[1;2B", "[1;5B", "[1;6B"},  // VK_NUMPAD2
	0x63:      {"[6~", "[6;2~", "[6;5~", "[6;6~"}, // VK_NUMPAD3
	0x64:      {"[D", "[1;2D", "[1;5D", "[1;6D"},  // VK_NUMPAD4
	0x65:      {"[G", "[1;2G", "[1;5G", "[1;6G"},  // VK_NUMPAD5
	0x66:      {"[C", "[1;2C", "[1;5C", "[1;6C"},  // VK_NUMPAD6
	0x67:      {"[A", "[1;2A", "[1;5A", "[1;6A"},  // VK_NUMPAD7
	0x68:      {"[1~", "[1;2~", "[1;5~", "[1;6~"}, // VK_NUMPAD8
	0x69:      {"[5~", "[5;2~", "[5;5~", "[5;6~"}, // VK_NUMPAD9
	vkDecimal: {"[3~", "[3;2~", "[3;5~", "[3;6~"},
	0x70:      {"[[A", "[23~", "[11^", "[23^"},  // F1
	0x71:      {"[[B", "[24~", "[12^", "[24^"},  // F2
	0x72:      {"[[C", "[25~", "[13^", "[25^"},  // F3
	0x73:      {"[[D", "[26~", "[14^", "[26^"},  // F4
	0x74:      {"[[E", "[28~", "[15^", "[28^"},  // F5
	0x75:      {"[17~", "[29~", "[17^", "[29^"}, // F6
	0x76:      {"[18~", "[31~", "[18^", "[31^"}, // F7
	0x77:      {"[19~", "[32~", "[19^", "[32^"}, // F8
	0x78:      {"[20~", "[33~", "[20^", "[33^"}, // F9
	0x79:      {"[21~", "[34~", "[21^", "[34^"}, // F10
	0x7A:      {"[23~", "[23$", "[23^", "[23@"}, // F11
	0x7B:      {"[24~", "[24$", "[24^", "[24@"}, // F12
}

// fnKeyBytes is get_vt100_fn_key: ESC plus the sequence for the modifiers.
func fnKeyBytes(code uint16, shift, ctrl bool) (string, bool) {
	k, ok := fnKeys[code]
	if !ok {
		return "", false
	}
	switch {
	case shift && ctrl:
		return "\x1b" + k.shiftCtrl, true
	case shift:
		return "\x1b" + k.shift, true
	case ctrl:
		return "\x1b" + k.ctrl, true
	}
	return "\x1b" + k.normal, true
}

// KeyTranslator turns console key events into the byte stream libuv produces.
// It carries the one piece of state libuv keeps between records: a pending
// UTF-16 high surrogate.
type KeyTranslator struct {
	highSurrogate uint16
}

// Translate returns the bytes for one record (nil when libuv emits nothing).
func (k *KeyTranslator) Translate(ev KeyEvent) []byte {
	// Ignore keyup events, unless the left alt key was held and a valid
	// unicode character was emitted.
	if !ev.KeyDown && (ev.VirtualKeyCode != vkMenu || ev.UnicodeChar == 0) {
		return nil
	}
	// Ignore keypresses to numpad number keys if the left alt is held because
	// the user is composing a character, or windows simulating this.
	if ev.ControlState&leftAltPressed != 0 && ev.ControlState&enhancedKey == 0 && altComposeKey(ev.VirtualKeyCode) {
		return nil
	}
	if ev.UnicodeChar != 0 {
		// Character key pressed.
		if ev.UnicodeChar >= 0xD800 && ev.UnicodeChar < 0xDC00 {
			k.highSurrogate = ev.UnicodeChar
			return nil
		}
		var out []byte
		// Prefix with ESC if alt was held, but alt was not used as part of a
		// compose sequence.
		if ev.ControlState&(leftAltPressed|rightAltPressed) != 0 &&
			ev.ControlState&(leftCtrlPressed|rightCtrlPressed) == 0 && ev.KeyDown {
			out = append(out, 0x1b)
		}
		if k.highSurrogate != 0 {
			out = append(out, []byte(string(utf16.DecodeRune(rune(k.highSurrogate), rune(ev.UnicodeChar))))...)
			k.highSurrogate = 0
		} else {
			out = append(out, wtf8(ev.UnicodeChar)...)
		}
		return repeat(out, ev.RepeatCount)
	}
	// Function key pressed.
	seq, ok := fnKeyBytes(ev.VirtualKeyCode, ev.ControlState&shiftPressed != 0,
		ev.ControlState&(leftCtrlPressed|rightCtrlPressed) != 0)
	if !ok {
		return nil
	}
	out := []byte(seq)
	if ev.ControlState&(leftAltPressed|rightAltPressed) != 0 {
		out = append([]byte{0x1b}, out...)
	}
	return repeat(out, ev.RepeatCount)
}

func altComposeKey(vk uint16) bool {
	switch vk {
	case vkInsert, vkEnd, vkDown, vkNext, vkLeft, vkClear, vkRight, vkHome, vkUp, vkPrior:
		return true
	}
	return vk >= 0x60 && vk <= vkNumpad9
}

// repeat honours wRepeatCount: libuv emits the key's bytes that many times
// (at least once).
func repeat(b []byte, n uint16) []byte {
	if n <= 1 {
		return b
	}
	out := make([]byte, 0, len(b)*int(n))
	for i := 0; i < int(n); i++ {
		out = append(out, b...)
	}
	return out
}

// wtf8 encodes one UTF-16 unit as uv_utf16_to_wtf8 does: a lone surrogate is
// encoded as its three-byte WTF-8 form.
func wtf8(u uint16) []byte {
	r := rune(u)
	if r >= 0xD800 && r <= 0xDFFF {
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F}
	}
	return []byte(string(r))
}
