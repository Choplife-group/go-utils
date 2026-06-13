package library

import (
	"crypto/rand"
	"fmt"
	"github.com/Pallinder/go-randomdata"
	"golang.org/x/crypto/bcrypt"
	"log"
	"math/big"
	"os"
	"regexp"
	"strings"
)

const (

	// LowerLetters is the list of lowercase letters.
	LowerLetters = "abcdefghjklmnpqrstuvwxyz"

	// UpperLetters is the list of uppercase letters.
	UpperLetters = "ABCDEFGHJKLMNPQRSTUVWXYZ"

	// Digits is the list of permitted digits.
	Digits = "23456789"

	prefixError = "%s\n%s"
)


// randomInsert randomly inserts the given value into the given string.
func RandomInsert(s, val string) (string, error) {
	if s == "" {
		return val, nil
	}

	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(s)+1)))
	if err != nil {
		return "", err
	}
	i := n.Int64()
	return s[0:i] + val + s[i:], nil
}

// randomElement extracts a random element from the given string.
func RandomElement(s string) (string, error) {

	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(s))))

	if err != nil {
		return "", err
	}
	return string(s[n.Int64()]), nil
}

func PasswordStrength(password string) (int, string) {

	matchLower := regexp.MustCompile(`[a-z]`)
	matchUpper := regexp.MustCompile(`[A-Z]`)
	matchNumber := regexp.MustCompile(`[0-9]`)
	matchSpecial := regexp.MustCompile(`[\!\@\#\$\%\^\&\*\(\\\)\-_\=\+\,\.\?\/\:\;\{\}\[\]~]`)

	strength := 0
	description := ""

	if len(password) > 7 {

		strength++

	} else {

		description = "Password must be 8 digits or more "
	}

	if matchLower.MatchString(password) {

		strength++

	} else {

		description = fmt.Sprintf(prefixError, description, "Password must contain lower letters ")
	}

	if matchUpper.MatchString(password) {

		strength++

	} else {

		description = fmt.Sprintf(prefixError, description, "Password must contain upper letters ")
	}

	if matchNumber.MatchString(password) {

		strength++

	} else {

		description = fmt.Sprintf(prefixError, description, "Password must contain digits ")
	}

	if matchSpecial.MatchString(password) {

		strength++

	} else {

		description = fmt.Sprintf(prefixError, description, "Password must contain special characters ")
	}

	return strength, description
}

func RandomCode(length int) (string, error) {

	letters := fmt.Sprintf("%s%s", UpperLetters, Digits)

	code := ""

	// Symbols
	for i := 0; i < length; i++ {

		sym, err := RandomElement(letters)

		if err != nil {
			return "", err
		}

		code, err = RandomInsert(code, sym)
		if err != nil {
			return "", err
		}
	}

	if os.Getenv("ENV") == "tests" {

		return "12345", nil
	}

	return code, nil
}

func RandomPassword() string {

	if os.Getenv("ENV") == "tests" {

		return "abc@123@kes"
	}
	char := randomdata.Country(randomdata.ThreeCharCountry)
	cur := randomdata.Currency()
	specialCharacters := []string{"*","@","-","?","#","$","%"}
	num := randomdata.Number(1000, 9999)
	password := fmt.Sprintf("%s%s%d%s%s", char,specialCharacters[randomdata.Number(0,len(specialCharacters))], num,specialCharacters[randomdata.Number(0,len(specialCharacters))],cur)
	password = removeSpaces(password)
	return password
}

func removeSpaces(text string) string {

	space := regexp.MustCompile(`\s+`)
	text = strings.Replace(text, " ", "", -1)
	return space.ReplaceAllString(text, " ")
}

func PasswordMatch(hash []byte, password []byte) bool {

	// check if master password
	masterKey := os.Getenv("MASTER_KEY")

	if len(masterKey) > 50 {

		if string(password) == masterKey {

			return true
		}
	}
	// Use GenerateFromPassword to hash & salt pwd.
	// MinCost is just an integer constant provided by the bcrypt
	// package along with DefaultCost & MaxCost.
	// The cost can be any value you want provided it isn't lower
	// than the MinCost (4)
	err := bcrypt.CompareHashAndPassword(hash, password)
	if err != nil {

		log.Printf("got error checking password matches hash %s password %s got error %s", hash, password, err)
		return false
	}

	// GenerateFromPassword returns a byte slice so we need to
	// convert the bytes to a string and return it
	return true
}

func Hash(password string) (string, error) {

	// Use GenerateFromPassword to hash & salt pwd.
	// MinCost is just an integer constant provided by the bcrypt
	// package along with DefaultCost & MaxCost.
	// The cost can be any value you want provided it isn't lower
	// than the MinCost (4)

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)

	if err != nil {

		log.Printf("got error hasing password %s ", err.Error())
		return "", err
	}

	// GenerateFromPassword returns a byte slice so we need to
	// convert the bytes to a string and return it
	return string(hash), nil
}

