package nodefs

import "io/fs"

// StatIdentity is a Stats object's dev and ino as the JS numbers Node gives
// them, for an info from Stat or Lstat: libuv's st_dev is the low 32 bits of
// the volume serial number and st_ino the 64-bit file id, converted to
// double. An entry read from its parent directory (statDirectory) carries
// no file id here, so it reports 0 for both, where libuv reads the id from
// FileIdFullDirectoryInformation. ok is false for any other fs.FileInfo.
func StatIdentity(info fs.FileInfo) (dev, ino float64, ok bool) {
	if s, isWin := info.(winStat); isWin {
		return float64(s.dev), float64(s.ino), true
	}
	return 0, 0, false
}
