// The platform this process runs on, in Go's and PiG's vocabulary: "linux/amd64", "windows/amd64", ...
// PiG's native Piglet builder builds only the machine it runs on, so a local build and every CI job name the host target.
export function targetOf(platform = process.platform, arch = process.arch) {
  const goos = { linux: 'linux', darwin: 'darwin', win32: 'windows' }[platform];
  const goarch = { x64: 'amd64', arm64: 'arm64' }[arch];
  if (!goos || !goarch) return undefined;
  return { goos, goarch, target: `${goos}/${goarch}`, exe: goos === 'windows' ? '.exe' : '' };
}

/** The host target, or throws when the platform is one PiG does not build for. */
export function hostTarget() {
  const t = targetOf();
  if (!t) throw new Error(`unsupported platform ${process.platform}/${process.arch}`);
  return t;
}
