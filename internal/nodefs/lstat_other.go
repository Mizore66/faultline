//go:build !windows

package nodefs

import "io/fs"

func lstatReparse(_ string, info fs.FileInfo) (fs.FileInfo, error) { return info, nil }
