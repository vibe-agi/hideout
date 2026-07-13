package hostcap

import _ "embed"

// builtinHostAppPackJSON is package-owned recipe data. It is decoded by the
// Manager/hostapppack integration, never by the authority provider itself.
//
//go:embed recipes/builtin-vscode.json
var builtinHostAppPackJSON []byte

// BuiltinHostAppPackJSON returns the embedded pack through an app-agnostic
// lifecycle API. Application names and behavior remain data in the pack.
func BuiltinHostAppPackJSON() []byte {
	return append([]byte(nil), builtinHostAppPackJSON...)
}
