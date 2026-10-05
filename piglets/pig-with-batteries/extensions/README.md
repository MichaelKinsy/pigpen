# Owned extensions

The `seed-check/` empty Go factory was added to exercise build/sign/verify
(PiG refuses Binary builds with zero extensions). The herdr Go extension now
satisfies that, so it is redundant and can be dropped on the owner's word. The herdr reporter is not
here: it is the shared Package `components/herdr`, selected in `../piglet.yaml`. It adds no capability and must
be replaced before publication. Place reviewed sources here and select exact
members in `../piglet.yaml`. Never auto-discover or copy a running session's
third-party extensions into a release.
