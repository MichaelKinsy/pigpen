# Sourced by oracle.sh, run.sh and ansi.sh.
#
# oh-my-pig follows omp at one version. omp updates itself, so `omp` on PATH is soon a later one, and a
# comparison with it reports what omp changed since as differences of the port. The harnesses refuse it.

OMP_REFERENCE_VERSION="19.1.1"

# require_omp_reference <omp binary>: exits 2 unless the binary is omp at the reference version.
require_omp_reference() {
  local got
  got="$("$1" --version 2>/dev/null | head -n 1)"
  if [ "$got" != "omp/$OMP_REFERENCE_VERSION" ]; then
    echo "error: $1 is ${got:-not an omp binary}; the comparison needs omp/$OMP_REFERENCE_VERSION (set OMP_BIN to that binary)" >&2
    exit 2
  fi
}
