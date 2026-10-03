//go:build !windows

package bypass

func lookupOwner(flow) (owner, bool) { return owner{}, false }
