package logic

import "regexp"

// identifierPattern is the shared storage-safe identifier grammar for schema
// fields and stable pipeline keys.
var identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

func IsValidIdentifier(value string) bool {
	return identifierPattern.MatchString(value)
}
