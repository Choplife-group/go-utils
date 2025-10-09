package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/go-cmd/cmd"
	"google.golang.org/api/option"
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

// UploadToGCPWithContext uploads a file to a Google Cloud Storage bucket
// We need to set the following environment variables:
// GCLOUD_BUCKET is the name of the bucket to upload to
// GCLOUD_STORAGE_CREDENTIALS_PATH is the path to the credentials file
func UploadToGCPWithContext(ctx context.Context, data []byte, remotePath string) (string, error) {
	remoteBucket := os.Getenv("GCLOUD_BUCKET")

	if remoteBucket == "" {
		return "", errors.New("GCLOUD_BUCKET is not set")
	}

	credentialsPath := os.Getenv("GCLOUD_STORAGE_CREDENTIALS_PATH")

	if credentialsPath == "" {
		return "", errors.New("GCLOUD_STORAGE_CREDENTIALS_PATH is not set")
	}

	client, err := storage.NewClient(ctx, option.WithCredentialsFile(credentialsPath))
	if err != nil {
		return "", err
	}

	defer client.Close()

	bh := client.Bucket(remoteBucket)
	// Next check if the bucket exists
	if _, err = bh.Attrs(ctx); err != nil {
		return "", err
	}

	obj := bh.Object(remotePath)

	w := obj.NewWriter(ctx)
	_, err = w.Write(data)
	if err != nil {
		w.Close()

		return "", err
	}

	err = w.Close()
	if err != nil {
		return "", err
	}

	if err := obj.ACL().Set(ctx, storage.AllUsers, storage.RoleReader); err != nil {
		return "", err
	}

	pathURL := fmt.Sprintf("https://storage.googleapis.com/%s/%s", remoteBucket, remotePath)

	return pathURL, nil
}