# Owned extensions

The `seed-check/` empty Go factory exists only to exercise build/sign/verify
(PiG refuses Binary builds with zero extensions). It adds no capability and must
be replaced before publication. Place reviewed sources here and select exact
members in `../piglet.yaml`. Never auto-discover or copy a running session's
third-party extensions into a release.
