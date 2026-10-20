package pi_permission_system

import (
	_ "embed"
)

// Port of buildPermissionsJsonSchema in src/config/config-schema.ts: the published JSON Schema (Draft 2020-12) of the config file.
// The original derives it from its zod source and commits the result as schemas/permissions.schema.json, with a parity test that
// fails when the two drift; the port embeds that committed file, and TestGeneratedSchemaMatchesTheOriginal checks it against the
// schema the original's own generator produces (recorded in testdata/config-oracle.json).

// permissionsSchemaURL is PERMISSIONS_SCHEMA_URL, the canonical hosted location of the generated JSON Schema.
const permissionsSchemaURL = "https://raw.githubusercontent.com/gotgenes/pi-packages/main/packages/pi-permission-system/schemas/permissions.schema.json"

//go:embed schemas/permissions.schema.json
var permissionsSchemaJSON []byte

// buildPermissionsJSONSchema returns the JSON Schema as an ordered object.
func buildPermissionsJSONSchema() *jsObject {
	v, err := parseJSON(permissionsSchemaJSON)
	if err != nil {
		panic("embedded permissions schema: " + err.Error())
	}
	return v.(*jsObject)
}
