// The suggestion panel follows the claimed ticket: it is visible only while
// the current staff member owns a HUMAN conversation. Service responses decide
// ownership (can_reply); this helper keeps the UI rule in one testable place.
export function canSuggestFor(owned: boolean, state: string | undefined): boolean {
  return owned && state === 'HUMAN'
}
