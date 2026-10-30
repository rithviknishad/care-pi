// Package s3admin creates the Care buckets in versitygw. Care never creates buckets.
package s3admin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Spec describes the buckets to ensure.
type Spec struct {
	// Endpoint is host:port of the S3 API, for example 127.0.0.1:17070.
	Endpoint       string
	AccessKey      string
	SecretKey      string
	Region         string
	PatientBucket  string
	FacilityBucket string
}

// Admin ensures the buckets.
type Admin interface {
	EnsureBuckets(ctx context.Context, s Spec) error
}

// FacilityPolicy returns the bucket policy that allows anonymous GET on the facility bucket.
// Upstream Care reads facility cover images by unsigned URL (SP5).
func FacilityPolicy(bucket string) string {
	p := map[string]any{
		"Version": "2012-10-17",
		"Statement": []any{map[string]any{
			"Effect":    "Allow",
			"Principal": "*",
			"Action":    []string{"s3:GetObject"},
			"Resource":  []string{"arn:aws:s3:::" + bucket + "/*"},
		}},
	}
	b, _ := json.Marshal(p)
	return string(b)
}

// MinIO talks to versitygw with minio-go.
type MinIO struct {
	// Wait is how long EnsureBuckets retries while the S3 API does not answer.
	Wait time.Duration
}

// EnsureBuckets implements Admin. It is idempotent.
func (m MinIO) EnsureBuckets(ctx context.Context, s Spec) error {
	c, err := minio.New(s.Endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(s.AccessKey, s.SecretKey, ""),
		Secure:       false,
		Region:       s.Region,
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		return fmt.Errorf("cannot create the S3 client: %w", err)
	}
	wait := m.Wait
	if wait == 0 {
		wait = 2 * time.Minute
	}
	deadline := time.Now().Add(wait)
	for _, b := range []string{s.PatientBucket, s.FacilityBucket} {
		for {
			err = ensureBucket(ctx, c, b, s.Region)
			if err == nil || time.Now().After(deadline) {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
			}
		}
		if err != nil {
			return fmt.Errorf("cannot create bucket %s in care-s3: %w", b, err)
		}
	}
	if err := c.SetBucketPolicy(ctx, s.FacilityBucket, FacilityPolicy(s.FacilityBucket)); err != nil {
		return fmt.Errorf("cannot set the read policy of bucket %s: %w", s.FacilityBucket, err)
	}
	return nil
}

func ensureBucket(ctx context.Context, c *minio.Client, bucket, region string) error {
	ok, err := c.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	// minio-go sends a LocationConstraint for each region except us-east-1.
	return c.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: region})
}

// Fake records the specs it receives.
type Fake struct {
	Calls []Spec
	Err   error
}

// EnsureBuckets implements Admin.
func (f *Fake) EnsureBuckets(_ context.Context, s Spec) error {
	f.Calls = append(f.Calls, s)
	return f.Err
}
