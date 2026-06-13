package library

import (
	"regexp"
)

func IsValidEmail(email string) bool {

	var emailRegex = regexp.MustCompile("^[a-zA-Z0-9.!#$%&'*+\\/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$")

	if len(email) < 3 || len(email) > 254 {

		return false
	}

	return emailRegex.MatchString(email)
}
