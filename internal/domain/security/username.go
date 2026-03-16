package security

import "regexp"

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$`)

func IsValidUsernameOrPath(username string) bool {

	if !usernameRe.MatchString(username) {
		return false
	}

	if len(username) < 3 || len(username) > 36 {
		return false
	}

	return true
}
