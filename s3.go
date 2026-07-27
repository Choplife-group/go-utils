package library

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3-compatible object storage (e.g. Hetzner Object Storage, Cloudflare R2, AWS S3).
// Configured entirely via environment variables:
//
//	S3_BUCKET_ACCESS_KEY  (required) access key ID
//	S3_BUCKET_SECRET_KEY  (required) secret access key
//	S3_BUCKET             (required) bucket name
//	S3_REGION             (optional) defaults to "fsn1"
//	S3_ENDPOINT           (optional) defaults to "https://<region>.your-objectstorage.com"
//	S3_PUBLIC_URL         (optional) base URL for returned links; defaults to the endpoint
//	S3_ACL                (optional) canned ACL to set on upload, e.g. "public-read".
//	                      Leave empty for providers that disable object ACLs
//	                      (e.g. Cloudflare R2) and rely on bucket-level public access.

const defaultS3Region = "fsn1"

// s3Config holds the resolved storage settings read from the environment.
type s3Config struct {
	accessKey string
	secretKey string
	bucket    string
	region    string
	endpoint  string
	publicURL string
	acl       string
}

// loadS3Config reads and validates the S3 settings from the environment.
func loadS3Config() (s3Config, error) {
	cfg := s3Config{
		accessKey: os.Getenv("S3_BUCKET_ACCESS_KEY"),
		secretKey: os.Getenv("S3_BUCKET_SECRET_KEY"),
		bucket:    os.Getenv("S3_BUCKET"),
		region:    os.Getenv("S3_REGION"),
		endpoint:  os.Getenv("S3_ENDPOINT"),
		publicURL: os.Getenv("S3_PUBLIC_URL"),
		acl:       os.Getenv("S3_ACL"),
	}

	if cfg.accessKey == "" || cfg.secretKey == "" || cfg.bucket == "" {
		return s3Config{}, fmt.Errorf("s3 storage not configured: set S3_BUCKET_ACCESS_KEY, S3_BUCKET_SECRET_KEY and S3_BUCKET")
	}

	if cfg.region == "" {
		cfg.region = defaultS3Region
	}

	if cfg.endpoint == "" {
		cfg.endpoint = fmt.Sprintf("https://%s.your-objectstorage.com", cfg.region)
	}

	if cfg.publicURL == "" {
		cfg.publicURL = cfg.endpoint
	}

	return cfg, nil
}

// newS3Client builds an S3 client for the given configuration.
func newS3Client(ctx context.Context, cfg s3Config) (*s3.Client, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.accessKey, cfg.secretKey, ""),
		),
		awsconfig.WithEndpointResolverWithOptions(
			aws.EndpointResolverWithOptionsFunc(
				func(service, region string, options ...interface{}) (aws.Endpoint, error) {
					return aws.Endpoint{URL: cfg.endpoint, HostnameImmutable: true}, nil
				},
			),
		),
	)
	if err != nil {
		return nil, err
	}

	return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = true
	}), nil
}

// UploadToS3 uploads data to S3-compatible object storage at remotePath, makes it
// publicly readable, and returns its public URL.
func UploadToS3(ctx context.Context, data []byte, remotePath string) (string, error) {
	cfg, err := loadS3Config()
	if err != nil {
		return "", err
	}

	client, err := newS3Client(ctx, cfg)
	if err != nil {
		return "", err
	}

	input := &s3.PutObjectInput{
		Bucket: aws.String(cfg.bucket),
		Key:    aws.String(remotePath),
		Body:   bytes.NewReader(data),
	}

	// Only set an object ACL when explicitly configured. Providers such as
	// Cloudflare R2 and ACL-disabled S3 buckets reject a canned ACL; those rely
	// on bucket-level public access instead.
	if cfg.acl != "" {
		input.ACL = types.ObjectCannedACL(cfg.acl)
	}

	if _, err = client.PutObject(ctx, input); err != nil {
		return "", err
	}

	// The object Key is stored raw, but the returned URL must be escaped so keys
	// containing characters like '#' or '?' resolve to the same object.
	return fmt.Sprintf("%s/%s/%s", strings.TrimRight(cfg.publicURL, "/"), cfg.bucket, escapeObjectPath(remotePath)), nil
}

// escapeObjectPath URL-escapes each segment of an object key while preserving
// the '/' separators, so the returned link points at the uploaded key.
func escapeObjectPath(key string) string {
	segments := strings.Split(key, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}

	return strings.Join(segments, "/")
}

// DeleteFromS3 removes the object at remotePath from S3-compatible object storage.
func DeleteFromS3(ctx context.Context, remotePath string) error {
	cfg, err := loadS3Config()
	if err != nil {
		return err
	}

	client, err := newS3Client(ctx, cfg)
	if err != nil {
		return err
	}

	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(cfg.bucket),
		Key:    aws.String(remotePath),
	})

	return err
}
