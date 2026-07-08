// Package decision owns Hideout's local operator decision center records.
//
// It deliberately contains no provider execution: Manager Core validates claims
// and dispatches approved decisions to Go-owned providers such as HostFS write
// or export/share. Informational notices live beside actionable decisions but
// never expose claim or approve/deny semantics.
package decision
