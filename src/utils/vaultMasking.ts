/**
 * Client-safe helpers for the Ansible Vault editor.
 *
 * Secret Protection Invariant (AGENTS.md §5): a vault variable's value is never
 * rendered, logged, or returned by the API. The only representation that ever
 * leaves the vault store is this mask, which carries the variable NAME only.
 */

export const VAULT_VALUE_MASK = '••••••••';

/** Masked display form for a vault variable, e.g. `•••••••• (vault: api_key)`. */
export function maskVaultValue(variableName: string): string {
  return `${VAULT_VALUE_MASK} (vault: ${variableName})`;
}

/** Variable names must be valid Ansible identifiers (lowercase is conventional). */
const VAULT_VARIABLE_NAME_PATTERN = /^[A-Za-z_][A-Za-z0-9_]*$/;

export function isValidVaultVariableName(name: string): boolean {
  return typeof name === 'string' && VAULT_VARIABLE_NAME_PATTERN.test(name);
}
