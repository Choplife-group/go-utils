package library

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/go-cmd/cmd"
)

func NumberOfLines(file string) int {

	envCmd := cmd.NewCmd("wc", "-l", file)

	// Run and wait for Cmd to return Status
	status := <-envCmd.Start()

	var response1 string

	// Print each line of STDOUT from Cmd
	for _, line := range status.Stdout {
		response1 = line
	}

	words := strings.Fields(response1)

	fmt.Println(words[0])

	// Create Cmd, buffered output
	//envCmd := cmd.NewCmd("awk","'END{print NR}' ",file)
	envCmd = cmd.NewCmd("wc", "-l", file)

	// Run and wait for Cmd to return Status
	status = <-envCmd.Start()

	var response string

	// gets each line of STDOUT from Cmd
	for _, line := range status.Stdout {
		response = line
	}

	words = strings.Fields(response)

	count, _ := strconv.Atoi(words[0])
	return count
}

func GetFileExtension(fn string) string {

	return strings.TrimSpace(strings.ReplaceAll(path.Ext(fn),".",""))

}

func RandomFileName(length int) (string, error) {

	letters := fmt.Sprintf("%s%s%s", LowerLetters, UpperLetters, Digits)

	code := ""

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

	return code, nil
}