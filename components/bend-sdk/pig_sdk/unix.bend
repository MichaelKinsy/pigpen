import Base

# Connects to the Unix socket at `path`. Answers two Socket handles for the one
# connection (the second is a dup of the first), so one computation can read
# while another writes. Both are non-blocking: Base's TCP.send_bytes and
# TCP.recv_bytes park the computation until the descriptor is ready. Base has
# no Unix-domain socket effect, and it takes no user handle type, so this
# reuses Base's Socket handle. The JS and C files are this SDK's own.
def Unix.connect(path: String) -> IO(Result<&1, &1, U32 & String, Socket & Socket>):
  import "./unix_connect.c"
  import "./unix_connect.js"
