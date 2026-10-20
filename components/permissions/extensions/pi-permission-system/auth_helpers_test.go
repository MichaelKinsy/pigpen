package pi_permission_system

// nopQuery and nopAuthLog are the inert PermissionQuery and AuthorizerLog a chain link is handed in twins that only check identity.
type nopQuery struct{}

func (nopQuery) CheckPermission(string, *string, *string) PermissionCheckResult {
	return PermissionCheckResult{}
}
func (nopQuery) GetToolPermission(string, *string) string { return "ask" }

type nopAuthLog struct{}

func (nopAuthLog) Review(string, *jsObject) {}
func (nopAuthLog) Debug(string, *jsObject)  {}
