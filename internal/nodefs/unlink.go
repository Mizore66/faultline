package nodefs

// Unlink is unlinkSync(path): one unlink(2) (libuv's uv_fs_unlink on
// Windows), its failure naming the path.
func Unlink(path string) error {
	if err := unlinkRaw(sysPath(path)); err != nil {
		return wrap(err, "unlink", path)
	}
	return nil
}
