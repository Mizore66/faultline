package nodefs

import (
	"io/fs"
	"syscall"
)

const oDirectory = 0

// fileAttributeDevice is FILE_ATTRIBUTE_DEVICE.
const fileAttributeDevice = 0x40

// direntTypeOf is the type libuv's fs__scandir gives an entry from the
// FileAttributes of its FILE_DIRECTORY_INFORMATION record: a device is
// UV__DT_CHAR, then any reparse point is a link, then a directory, else a
// file. os.File.ReadDir reads the same attributes from its
// FILE_ID_BOTH_DIR_INFO or FILE_FULL_DIR_INFO record, which Info returns
// without another call.
func direntTypeOf(e fs.DirEntry) uint8 {
	info, err := e.Info()
	if err != nil {
		return 0
	}
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return 0
	}
	switch attrs := data.FileAttributes; {
	case attrs&fileAttributeDevice != 0:
		return dtCHR
	case attrs&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0:
		return dtLNK
	case attrs&syscall.FILE_ATTRIBUTE_DIRECTORY != 0:
		return dtDIR
	}
	return dtREG
}
