// Unix.connect on the C lane: socket(AF_UNIX), connect, O_NONBLOCK, dup.
#include <sys/un.h>

Term unix_connect_run(Env e, Term* f, IoWork* w) {
  uint64_t n = 0;
  char* p = io_cstr(e, f[0], &n);
  struct sockaddr_un at;
  memset(&at, 0, sizeof(at));
  int fd = -1;
  int dup = -1;
  int code = EINVAL;
  if (n > 0 && !io_nul(p, n) && n < sizeof(at.sun_path)) {
    at.sun_family = AF_UNIX;
    memcpy(at.sun_path, p, n);
    fd = socket(AF_UNIX, SOCK_STREAM, 0);
    code = errno;
  }
  free(p);
  if (fd >= 0 && connect(fd, (struct sockaddr*)&at, sizeof(at)) < 0) {
    code = errno;
    close(fd);
    fd = -1;
  }
  if (fd >= 0 && fcntl(fd, F_SETFL, fcntl(fd, F_GETFL) | O_NONBLOCK) < 0) {
    code = errno;
    close(fd);
    fd = -1;
  }
  if (fd >= 0) {
    dup = fcntl(fd, F_DUPFD, 3);
    if (dup < 0) {
      code = errno;
      close(fd);
      fd = -1;
    }
  }
  if (fd < 0) {
    return io_fail(e, (u32)code, NULL);
  }
  return io_done(e, io_tup(e, io_hand(fd), io_hand(dup)));
}

static void __attribute__((constructor)) unix_connect_use(void) {
  io_eff(CID(Unix.connect), unix_connect_run);
}
