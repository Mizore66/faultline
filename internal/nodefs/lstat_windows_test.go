package nodefs

import (
	"encoding/binary"
	"testing"
	"unicode/utf16"
)

func reparseBuf(tag uint32, data []byte) []byte {
	b := make([]byte, 8, 8+len(data))
	binary.LittleEndian.PutUint32(b, tag)
	binary.LittleEndian.PutUint16(b[4:], uint16(len(data)))
	return append(b, data...)
}

func mountPoint(target string) []byte {
	name := utf16.Encode([]rune(target))
	d := make([]byte, 8)
	binary.LittleEndian.PutUint16(d[2:], uint16(2*len(name)))
	for _, u := range name {
		d = binary.LittleEndian.AppendUint16(d, u)
	}
	return reparseBuf(tagMountPoint, d)
}

func appExecLink(count uint32, strs ...string) []byte {
	d := binary.LittleEndian.AppendUint32(nil, count)
	for _, s := range strs {
		for _, u := range utf16.Encode([]rune(s)) {
			d = binary.LittleEndian.AppendUint16(d, u)
		}
		d = binary.LittleEndian.AppendUint16(d, 0)
	}
	return reparseBuf(tagAppExecLink, append(d, 0, 0))
}

// Cases follow libuv 1.52 fs__readlink_handle.
func TestClassifyReparse(t *testing.T) {
	for _, tc := range []struct {
		name string
		buf  []byte
		link bool
	}{
		{"symlink", reparseBuf(tagSymlink, make([]byte, 12)), true},
		{"wsl symlink", reparseBuf(tagLxSymlink, []byte{2, 0, 0, 0, 'x'}), true},
		{"junction to drive", mountPoint(`\??\C:\target`), true},
		{"junction to drive root", mountPoint(`\??\d:`), true},
		{"volume mount point", mountPoint(`\??\Volume{0000}\`), false},
		{"appexeclink", appExecLink(3, "pkg", "app", `C:\Program Files\app.exe`), true},
		{"appexeclink relative", appExecLink(3, "pkg", "app", `app.exe`), false},
		{"appexeclink two strings", appExecLink(2, "pkg", "app"), false},
		{"onedrive placeholder", reparseBuf(0x9000601A, nil), false},
		{"wof", reparseBuf(0x80000017, nil), false},
	} {
		if got := classifyReparse(tc.buf) == nil; got != tc.link {
			t.Errorf("%s: link=%v, want %v", tc.name, got, tc.link)
		}
	}
}
