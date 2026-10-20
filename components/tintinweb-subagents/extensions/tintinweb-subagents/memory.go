package tintinweb_subagents

// Persistent agent memory. upstream: src/memory.ts.

func isUnsafeName(name string) bool                            { return false }
func isSymlink(path string) bool                               { return false }
func safeReadFile(path string) (string, bool)                  { return "", false }
func resolveMemoryDir(name, scope, cwd string) (string, error) { return "", nil }
func ensureMemoryDir(dir string) error                         { return nil }
func readMemoryIndex(dir string) (string, bool)                { return "", false }
func buildMemoryBlock(name, scope, cwd string) (string, error) { return "", nil }
func buildReadOnlyMemoryBlock(name, scope, cwd string) (string, error) {
	return "", nil
}
