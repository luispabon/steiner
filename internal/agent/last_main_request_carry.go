package agent

// CarryLastMainRequest returns the last main request a finished run leaves
// carried. A failed run that returned no request keeps carried, because a
// failed main call never sets one; every other outcome adopts returned, zero
// included, so a success can reset the identity. Hosts that report a run error
// as nil, such as the interactive run adapter, apply this rule themselves before
// the driver adopts the output.
func CarryLastMainRequest(carried, returned LastMainRequest, runErr error) LastMainRequest {
	if runErr != nil && returned == (LastMainRequest{}) {
		return carried
	}
	return returned
}
