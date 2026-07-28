package library

import (
	"context"
	"fmt"
	"os"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

// Google Cloud Storage helpers. Configured via environment variables:
//
//	GCLOUD_BUCKET                    (required) bucket name
//	GCLOUD_STORAGE_CREDENTIALS_PATH  (required) path to the service-account JSON

// newGCSClient builds a Google Cloud Storage client using the credentials file
// referenced by GCLOUD_STORAGE_CREDENTIALS_PATH.
func newGCSClient(ctx context.Context) (*storage.Client, error) {
	credentialsPath := os.Getenv("GCLOUD_STORAGE_CREDENTIALS_PATH")

	return storage.NewClient(ctx, option.WithCredentialsFile(credentialsPath))
}

// UploadToGCPWithContext uploads data to the GCLOUD_BUCKET bucket at remotePath,
// makes it publicly readable, and returns its public URL.
func UploadToGCPWithContext(ctx context.Context, data []byte, remotePath string) (string, error) {
	remoteBucket := os.Getenv("GCLOUD_BUCKET")

	client, err := newGCSClient(ctx)
	if err != nil {
		return "", err
	}
	defer client.Close()

	// Write straight to the object; a missing/unreachable bucket surfaces on
	// Write/Close, so no separate bucket-metadata preflight (which would require
	// storage.buckets.get on an otherwise object-create-only principal).
	obj := client.Bucket(remoteBucket).Object(remotePath)

	w := obj.NewWriter(ctx)
	if _, err = w.Write(data); err != nil {
		return "", err
	}

	if err = w.Close(); err != nil {
		return "", err
	}

	// The object is now committed. If making it public fails (e.g. the bucket
	// uses uniform bucket-level access where object ACLs are unsupported), delete
	// the just-written object so we don't leave a private orphan behind.
	if err := obj.ACL().Set(ctx, storage.AllUsers, storage.RoleReader); err != nil {
		_ = obj.Delete(ctx)
		return "", err
	}

	return fmt.Sprintf("https://storage.googleapis.com/%s/%s", remoteBucket, escapeObjectPath(remotePath)), nil
}

// DeleteFromGCP removes the object at remotePath from the GCLOUD_BUCKET bucket.
func DeleteFromGCP(ctx context.Context, remotePath string) error {
	remoteBucket := os.Getenv("GCLOUD_BUCKET")

	client, err := newGCSClient(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	return client.Bucket(remoteBucket).Object(remotePath).Delete(ctx)
}
