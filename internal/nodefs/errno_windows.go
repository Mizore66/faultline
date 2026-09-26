package nodefs

// win32Codes is libuv's uv_translate_sys_error for the Win32 errors fs calls
// return. Codes missing here are UNKNOWN, as in libuv. ERROR_PATH_NOT_FOUND
// is ENOENT (Go names it syscall.ENOTDIR); wrap still reports ENOTDIR when an
// ancestor is a file, matching the Linux goldens (KNOWN_DIFFERENCES.md).
var win32Codes = map[uintptr]string{
	1:    "EISDIR",       // ERROR_INVALID_FUNCTION
	2:    "ENOENT",       // ERROR_FILE_NOT_FOUND
	3:    "ENOENT",       // ERROR_PATH_NOT_FOUND
	4:    "EMFILE",       // ERROR_TOO_MANY_OPEN_FILES
	5:    "EPERM",        // ERROR_ACCESS_DENIED
	6:    "EBADF",        // ERROR_INVALID_HANDLE
	8:    "ENOMEM",       // ERROR_NOT_ENOUGH_MEMORY
	14:   "ENOMEM",       // ERROR_OUTOFMEMORY
	15:   "ENOENT",       // ERROR_INVALID_DRIVE
	17:   "EXDEV",        // ERROR_NOT_SAME_DEVICE
	19:   "EROFS",        // ERROR_WRITE_PROTECT
	23:   "EIO",          // ERROR_CRC
	32:   "EBUSY",        // ERROR_SHARING_VIOLATION
	33:   "EBUSY",        // ERROR_LOCK_VIOLATION
	38:   "EOF",          // ERROR_HANDLE_EOF
	39:   "ENOSPC",       // ERROR_HANDLE_DISK_FULL
	50:   "ENOTSUP",      // ERROR_NOT_SUPPORTED
	80:   "EEXIST",       // ERROR_FILE_EXISTS
	87:   "EINVAL",       // ERROR_INVALID_PARAMETER
	112:  "ENOSPC",       // ERROR_DISK_FULL
	123:  "ENOENT",       // ERROR_INVALID_NAME
	126:  "ENOENT",       // ERROR_MOD_NOT_FOUND
	142:  "EBUSY",        // ERROR_BUSY_DRIVE
	145:  "ENOTEMPTY",    // ERROR_DIR_NOT_EMPTY
	148:  "EBUSY",        // ERROR_PATH_BUSY
	161:  "ENOENT",       // ERROR_BAD_PATHNAME
	170:  "EBUSY",        // ERROR_BUSY
	183:  "EEXIST",       // ERROR_ALREADY_EXISTS
	206:  "ENAMETOOLONG", // ERROR_FILENAME_EXCED_RANGE
	267:  "ENOENT",       // ERROR_DIRECTORY
	740:  "EACCES",       // ERROR_ELEVATION_REQUIRED
	998:  "EACCES",       // ERROR_NOACCESS
	1117: "EIO",          // ERROR_IO_DEVICE
	1314: "EPERM",        // ERROR_PRIVILEGE_NOT_HELD
	1464: "EINVAL",       // ERROR_SYMLINK_NOT_SUPPORTED
	1920: "EACCES",       // ERROR_CANT_ACCESS_FILE
	1921: "ELOOP",        // ERROR_CANT_RESOLVE_FILENAME
	4390: "EINVAL",       // ERROR_NOT_A_REPARSE_POINT
	4392: "ENOENT",       // ERROR_INVALID_REPARSE_DATA
}
