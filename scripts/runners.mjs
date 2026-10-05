// The native GitHub runner for each target a Piglet manifest may list in build.targets. PiG's native builder builds only
// the machine it runs on, so every target needs a runner of its own OS and architecture. Used by build-matrix and checked
// against the CI platform matrix (scripts/check-workflows.mjs, part of npm run quality). macos-13 (darwin/amd64) is retired; macos-15-intel replaces it.
export const runners = {
  'linux/amd64': 'ubuntu-24.04',
  'linux/arm64': 'ubuntu-24.04-arm',
  'darwin/arm64': 'macos-15',
  'darwin/amd64': 'macos-15-intel',
  'windows/amd64': 'windows-2025',
};
