//go:build windows

package nodefs

import "os"

// renameRaw is MoveFileEx with MOVEFILE_REPLACE_EXISTING, as libuv's
// uv_fs_rename calls it.
func renameRaw(from, to string) error { return os.Rename(from, to) }
