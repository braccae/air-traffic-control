package nodeapi

import (
	"air-traffic-control/backend/pkg/secrets"
)

// validateVaultSecretRef rejects references to vault variables that do not
// exist. Manual secret naming means a typo is otherwise invisible until the
// first connection attempt; failing at save time keeps the inventory honest.
func validateVaultSecretRef(name string) error {
	return secrets.ValidateRef(name)
}
