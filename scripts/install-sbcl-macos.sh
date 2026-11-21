#!/bin/sh
# Installs SBCL 2.6.9 with core compression on an Apple-silicon macOS runner, the way release.yml and ci.yml both need it for the
# Lisp Piglet pig-autolith (PiG builds the Lisp image into the Binary and does not bundle SBCL).
#
# SBCL publishes no macOS arm64 binary of 2.6.9, so it is built from the official source tarball with the official arm64-darwin 2.4.0
# binary as the cross-compiling host. Both downloads are checked against the SHA-256 pinned here. The source's pin was also compared with
# the SHA-256 SBCL publishes for the uncompressed tarball in the signed sbcl-2.6.9-crhodes.asc (14ea1b03...78ab, checked on the .tar
# that `bunzip2` yields). Nothing floating: no Homebrew formula, no `latest`.
# Needs the system zstd library that core compression links (preinstalled on GitHub's macOS runners; the script stops if it is missing).
#
#   sh scripts/install-sbcl-macos.sh <install-root>      prints PIG_SBCL and SBCL_HOME lines to append to $GITHUB_ENV
set -eu
root=${1:?usage: install-sbcl-macos.sh <install-root>}
version=2.6.9
src_sha=c6fd1d735570eb4ff34caf9609988ca77ed0bd12b55d09a4fed075be890da513
src_tar_sha=14ea1b033b4da5220639404c32cb163de813659e955cf08580cc766114ea78ab
boot_sha=1d01fac2d9748f769c9246a0a11a2c011d7843337f8f06ca144f5a500e10c117
base=https://downloads.sourceforge.net/project/sbcl/sbcl
[ "$(uname -s)-$(uname -m)" = Darwin-arm64 ] || { echo "install-sbcl-macos.sh: needs macOS on arm64" >&2; exit 1; }
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fetch() { curl -fsSL --retry 3 -o "$work/$1" "$2"; echo "$3  $work/$1" | shasum -a 256 -c - >&2; }
fetch source.tar.bz2 "$base/$version/sbcl-$version-source.tar.bz2" "$src_sha"
fetch boot.tar.bz2 "$base/2.4.0/sbcl-2.4.0-arm64-darwin-binary.tar.bz2" "$boot_sha"
[ "$(bunzip2 -c "$work/source.tar.bz2" | shasum -a 256 | cut -d' ' -f1)" = "$src_tar_sha" ] || { echo "install-sbcl-macos.sh: source tar differs from the SHA-256 SBCL signs" >&2; exit 1; }
tar -C "$work" -xjf "$work/source.tar.bz2"
tar -C "$work" -xjf "$work/boot.tar.bz2"
cd "$work/sbcl-$version"
SBCL_MACOSX_VERSION_MIN=${SBCL_MACOSX_VERSION_MIN:-$(sw_vers -productVersion | cut -d. -f1).0} \
  sh make.sh --prefix="$root" --xc-host="$work/sbcl-2.4.0-arm64-darwin/run-sbcl.sh" --with-sb-core-compression --with-sb-thread >&2
INSTALL_ROOT="$root" sh install.sh >&2
"$root/bin/sbcl" --version >&2
echo "PIG_SBCL=$root/bin/sbcl"
echo "SBCL_HOME=$root/lib/sbcl"
