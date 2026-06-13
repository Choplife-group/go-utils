package library

import (
	"strconv"
	"strings"
)


func ReferenceNumber(int642 int64) string {

	int642 = int642 + 1000*1000 // make 3 digits
	hex := strings.ToUpper(strconv.FormatInt(int642, 36))
	return strings.ToUpper(hex)

}
