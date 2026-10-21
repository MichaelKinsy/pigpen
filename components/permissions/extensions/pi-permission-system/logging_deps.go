package pi_permission_system

// Lane-local stand-ins for what other lanes of the port own (the lead dedups at integration; the names mirror the TypeScript):
//   - config: PermissionSystemExtensionConfig (here the three fields the writer reads);

// LogConfig is what the log writer reads of the extension config on every write (the original's getConfig thunk result).
type LogConfig struct {
	DebugLog            bool
	PermissionReviewLog bool
	// ReviewLogFieldMaxWidth is nil when the config names none.
	ReviewLogFieldMaxWidth *float64
}
