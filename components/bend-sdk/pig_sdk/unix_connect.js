// Unix.connect on the JS lane: socket(AF_UNIX), connect, O_NONBLOCK, dup.
// io_sys() is libc through bun:ffi; its fcntl and connect are the ones the
// TCP effects use.
function unix_connect(path) {
  const sys = io_sys();
  const p = io_bytes(path);
  const max = sys.mac ? 103 : 107;
  if (p.includes(0) || p.length === 0 || p.length > max) {
    return io_fail(22);
  }
  const fd = sys.socket(1, 1, 0);
  if (fd < 0) {
    return io_fail(sys.errno());
  }
  // Linux sockaddr_un: u16 family, then the path. macOS: u8 length, u8 family.
  const at = new Uint8Array(sys.mac ? 106 : 110);
  if (sys.mac) {
    at[0] = at.length;
    at[1] = 1;
  } else {
    at[0] = 1;
  }
  at.set(p, 2);
  const fail = () => {
    const code = sys.errno();
    sys.close(fd);
    return io_fail(code);
  };
  if (sys.connect(fd, sys.ptr(at), at.length) < 0) {
    return fail();
  }
  const flags = sys.fcntl(fd, 3, 0);
  if (flags < 0 || sys.fcntl(fd, 4, flags | (sys.mac ? 4 : 0x800)) < 0) {
    return fail();
  }
  // F_DUPFD is 0 on both platforms; the lowest free descriptor from 3.
  const dup = sys.fcntl(fd, 0, 3);
  if (dup < 0) {
    return fail();
  }
  return io_done(io_tup(fd, dup));
}

io_eff(CID(Unix.connect), unix_connect);
