package nodefs

import (
	"io/fs"
	"os"
)

// fstatFile is uv_fs_fstat.
func fstatFile(f *os.File) (fs.FileInfo, error) { return f.Stat() }
