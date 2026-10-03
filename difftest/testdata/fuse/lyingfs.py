# lyingfs: a read-only passthrough FUSE filesystem (fusepy, libfuse 2) that
# misreports what the round-5 reviewers needed to test the glibc and libuv
# ports in internal/nodefs. Used by TestLiveFuseMatchesNode. Knobs, from the
# environment:
#  OPEN_FAIL=a,b   getattr of these names fails with ERRNO while the file has an open handle (i.e. fstat)
#  ERRNO=5         errno used for OPEN_FAIL / GETATTR_FAIL / READ_FAIL
#  GETATTR_FAIL=a  getattr of these names always fails with ERRNO
#  READ_FAIL=a     read of these names fails with ERRNO
#  BIGSIZE=a:N     getattr reports st_size=N for name a (content unchanged)
#  DTYPE=a:M       readdir reports st_mode M (octal) for name a -> d_type=(M>>12)&15
#  UNTYPED=1       readdir reports no type (DT_UNKNOWN) for every entry
#  ZERO=a          readdir reports d_ino 0 for these names (with use_ino)
#  ATTR0=1         attr_timeout=0, entry_timeout=0
#  DIRECT=1        direct_io on open
#  ONCE=op:name:errno[:count]  fail the first count (1) calls of op on name
#  ACCESS_FAIL=a:errno         access(2) of a fails with errno
import os, sys, errno, threading
try:
    from fusepy import FUSE, FuseOSError, Operations
except ImportError:  # Debian and Ubuntu's python3-fusepy installs it as "fuse"
    from fuse import FUSE, FuseOSError, Operations
SRC, MNT = sys.argv[1], sys.argv[2]
env = os.environ.get
def names(k): return set(x for x in (env(k) or '').split(',') if x)
def kv(k): return dict(x.split(':',1) for x in (env(k) or '').split(',') if x)
OPEN_FAIL, GETATTR_FAIL, READ_FAIL, ZERO = names('OPEN_FAIL'), names('GETATTR_FAIL'), names('READ_FAIL'), names('ZERO')
ERR = int(env('ERRNO') or '5')
BIG = {k:int(v) for k,v in kv('BIGSIZE').items()}
DTYPE = {k:int(v,8) for k,v in kv('DTYPE').items()}
openc = {}; dopen = {}; lock = threading.Lock()
# ONCE=op:name:errno[:count],...  fail the first <count> (default 1) calls of op (getattr/read/readdir/opendir/open/access) on name
ONCE = {}
for x in (env('ONCE') or '').split(','):
    if not x: continue
    parts = x.split(':'); ONCE[(parts[0], parts[1])] = [int(parts[2]), int(parts[3]) if len(parts) > 3 else 1]
ACCESS_FAIL = kv('ACCESS_FAIL')
def once(op, path):
    b = os.path.basename(path) or '/'
    with lock:
        e = ONCE.get((op, b))
        if e and e[1] > 0:
            e[1] -= 1
            raise FuseOSError(e[0])
class FS(Operations):
    def _p(self, p):
        if env('LATIN1'): p = os.fsdecode(p.encode('latin-1'))
        return os.path.join(SRC, p.lstrip('/'))
    def getattr(self, path, fh=None):
        once('getattr', path)
        b = os.path.basename(path)
        if b in GETATTR_FAIL: raise FuseOSError(ERR)
        with lock:
            if b in OPEN_FAIL and openc.get(path, 0) > 0: raise FuseOSError(ERR)
            if b in names('OPENDIR_FAIL') and dopen.get(path, 0) > 0: raise FuseOSError(ERR)
        try: st = os.lstat(self._p(path))
        except OSError as e: raise FuseOSError(e.errno)
        d = {k: getattr(st, k) for k in ('st_mode','st_ino','st_nlink','st_uid','st_gid','st_size','st_atime','st_mtime','st_ctime')}
        if b in BIG: d['st_size'] = BIG[b]
        return d
    def access(self, path, amode):
        once('access', path)
        b = os.path.basename(path)
        if b in ACCESS_FAIL: raise FuseOSError(int(ACCESS_FAIL[b]))
        return 0
    def opendir(self, path):
        once('opendir', path)
        with lock: dopen[path] = dopen.get(path, 0) + 1
        return 0
    def releasedir(self, path, fh):
        with lock: dopen[path] = dopen.get(path, 1) - 1
        return 0
    def readdir(self, path, fh):
        once('readdir', path)
        out = ['.', '..']
        for n0 in sorted(os.listdir(self._p(path)), key=os.fsencode):
            st = os.lstat(os.path.join(self._p(path), n0))
            n = os.fsencode(n0).decode('latin-1') if env('LATIN1') else n0
            if env('UNTYPED'): out.append((n, None, 0)); continue
            a = {'st_ino': 0 if n in ZERO else st.st_ino, 'st_mode': DTYPE.get(n, st.st_mode)}
            out.append((n, a, 0))
            if n in names('DUP'): out.append((n, a, 0))
        return out
    def readlink(self, path): return os.readlink(self._p(path))
    def open(self, path, flags):
        once('open', path)
        fd = os.open(self._p(path), os.O_RDONLY)
        with lock: openc[path] = openc.get(path, 0) + 1
        return fd
    def read(self, path, size, offset, fh):
        once('read', path)
        if os.path.basename(path) in READ_FAIL: raise FuseOSError(ERR)
        return os.pread(fh, size, offset)
    def release(self, path, fh):
        os.close(fh)
        with lock: openc[path] = openc.get(path, 1) - 1
kw = dict(foreground=True, ro=True, allow_other=True, use_ino=True)
if env('LATIN1'): kw.update(encoding='latin-1')
if env('ATTR0'): kw.update(attr_timeout=0, entry_timeout=0)
if env('DIRECT'): kw.update(direct_io=True)
FUSE(FS(), MNT, **kw)
