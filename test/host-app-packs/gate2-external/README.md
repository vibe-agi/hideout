# Gate 2 External Host-App Pack

This fixture is intentionally outside the Hideout binary and built-in recipe
catalog. It binds `hcode` to the same Core-verified VS Code application and
Core-owned safety profile used by the built-in `code` recipe.

The fixture adds no executable host effect, signing claim, safety rule, host
path, or fallback. A real Gate 2 run must install it through the public app-pack
lifecycle and prove that disabling or revoking it removes `hcode` without
affecting the built-in `code` binding.
