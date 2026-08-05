package library

import (
	"context"
	"os"
	"strings"
)

// Object storage provider selection. Configured via environment variable:
//
//	OBJECT_STORAGE_PROVIDER  (optional) "s3" routes to S3-compatible storage;
//	                         any other value (including empty) routes to GCS.

const objectStorageProviderS3 = "s3"

// objectStorageProvider reports the configured provider, normalised for comparison.
func objectStorageProvider() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv("OBJECT_STORAGE_PROVIDER")))
}

// Upload stores data at remotePath using the provider named by
// OBJECT_STORAGE_PROVIDER and returns its public URL. It routes to UploadToS3
// when that variable is "s3", and to UploadToGCPWithContext otherwise, so
// callers need no provider-specific branching of their own.
func Upload(ctx context.Context, data []byte, remotePath string) (string, error) {
	if objectStorageProvider() == objectStorageProviderS3 {
		return UploadToS3(ctx, data, remotePath)
	}

	return UploadToGCPWithContext(ctx, data, remotePath)
}

// Delete removes the object at remotePath from the provider named by
// OBJECT_STORAGE_PROVIDER, mirroring the routing Upload performs.
func Delete(ctx context.Context, remotePath string) error {
	if objectStorageProvider() == objectStorageProviderS3 {
		return DeleteFromS3(ctx, remotePath)
	}

	return DeleteFromGCP(ctx, remotePath)
}
