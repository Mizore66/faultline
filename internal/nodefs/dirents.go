package nodefs

import (
	"io/fs"
	"sort"
)

// Dirent is one entry of readdirSync(path, { withFileTypes: true }).
type Dirent struct {
	Name                     string // decoded as UTF-8 with replacement
	IsFile, IsDir, IsSymlink bool
}

// d_type values libuv's uv__fs_get_dirent_type names (with FIFO, CHR, BLK
// and SOCK, which are none of the three).
const (
	dtDir = 4
	dtReg = 8
	dtLnk = 10
)

// ReadDirents is readdirSync(path, { withFileTypes: true }): byte-sorted
// names (libuv scandir), types from d_type, an lstat in order for an
// unknown type (the first failure thrown), as ReadDirNames. Where scandir
// reports no d_type (the non-Linux scandir reads names only), every entry
// is typed by that lstat.
func ReadDirents(path string) ([]Dirent, error) {
	names, types, err := scandir(sysPath(path))
	if err != nil {
		return nil, wrap(err, "scandir", path)
	}
	order := make([]int, len(names))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool { return names[order[i]] < names[order[j]] })
	out := make([]Dirent, len(names))
	for i, k := range order {
		out[i].Name = DecodeUTF8([]byte(names[k]))
	}
	// lib/internal/fs/utils.js getDirents: the known types first, then
	// one lstat per unknown entry, in order.
	for i, k := range order {
		if types == nil || !direntTypeKnown(types[k]) {
			info, err := Lstat(Join(path, out[i].Name))
			if err != nil {
				return nil, err
			}
			mode := info.Mode()
			out[i].IsFile, out[i].IsDir, out[i].IsSymlink = mode.IsRegular(), mode.IsDir(), mode&fs.ModeSymlink != 0
			continue
		}
		switch types[k] {
		case dtReg:
			out[i].IsFile = true
		case dtDir:
			out[i].IsDir = true
		case dtLnk:
			out[i].IsSymlink = true
		}
	}
	return out, nil
}

// MtimeMs is fs.Stats.mtimeMs of an lstat result: milliseconds with the
// sub-millisecond fraction Node keeps (sec*1e3 + nsec/1e6 as a float64,
// lib/internal/fs/utils.js msFromTimeSpec). On Windows the time is libuv's
// uv__filetime_to_timespec of the 100 ns file time: the seconds and the
// remainder both truncate toward zero.
func MtimeMs(info fs.FileInfo) float64 {
	t := info.ModTime()
	if isWindows {
		ticks := t.UnixNano() / 100
		sec, rem := ticks/10_000_000, ticks%10_000_000
		return float64(sec)*1e3 + float64(rem*100)/1e6
	}
	return float64(t.Unix())*1e3 + float64(t.Nanosecond())/1e6
}
