package library

import (
	"crypto/md5"
	"fmt"
)

func MD5S(s string) string {
	h := md5.New()
	h.Write([]byte(s))
	return fmt.Sprintf("%x", h.Sum(nil))
}
