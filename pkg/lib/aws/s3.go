/*
Copyright 2019 Cortex Labs, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package aws

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/endpoints"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"

	"github.com/cortexlabs/cortex/pkg/lib/errors"
	"github.com/cortexlabs/cortex/pkg/lib/files"
	"github.com/cortexlabs/cortex/pkg/lib/json"
	"github.com/cortexlabs/cortex/pkg/lib/msgpack"
	"github.com/cortexlabs/cortex/pkg/lib/parallel"
	"github.com/cortexlabs/cortex/pkg/lib/sets/strset"
	s "github.com/cortexlabs/cortex/pkg/lib/strings"
)

const DefaultS3Region string = endpoints.UsWest2RegionID

var S3Regions strset.Set

func init() {
	resolver := endpoints.DefaultResolver()
	partitions := resolver.(endpoints.EnumPartitions).Partitions()

	S3Regions = strset.New()

	for _, p := range partitions {
		if p.ID() == endpoints.AwsPartitionID || p.ID() == endpoints.AwsCnPartitionID {
			for id := range p.Regions() {
				S3Regions.Add(id)
			}
		}
	}
}

func (c *Client) S3Path(key string) string {
	return "s3://" + filepath.Join(c.Bucket, key)
}

func S3PathJoin(paths ...string) string {
	if len(paths) == 0 {
		return ""
	}
	paths[0] = paths[0][5:]
	return "s3://" + filepath.Join(paths...)
}

func (c *Client) IsS3File(keys ...string) (bool, error) {
	for _, key := range keys {
		_, err := c.S3.HeadObject(&s3.HeadObjectInput{
			Bucket: aws.String(c.Bucket),
			Key:    aws.String(key),
		})

		if IsNotFoundErr(err) {
			return false, nil
		}
		if err != nil {
			return false, errors.Wrap(err, key)
		}
	}

	return true, nil
}

func (c *Client) IsS3Prefix(prefixes ...string) (bool, error) {
	for _, prefix := range prefixes {
		out, err := c.S3.ListObjectsV2(&s3.ListObjectsV2Input{
			Bucket: aws.String(c.Bucket),
			Prefix: aws.String(prefix),
		})

		if err != nil {
			return false, errors.Wrap(err, prefix)
		}

		if *out.KeyCount == 0 {
			return false, nil
		}
	}

	return true, nil
}

func (c *Client) IsS3Dir(dirPaths ...string) (bool, error) {
	fullDirPaths := make([]string, len(dirPaths))
	for i, dirPath := range dirPaths {
		fullDirPaths[i] = s.EnsureSuffix(dirPath, "/")
	}
	return c.IsS3Prefix(fullDirPaths...)
}

func (c *Client) IsS3PathFile(s3Paths ...string) (bool, error) {
	keys, err := c.ExractS3PathPrefixes(s3Paths...)
	if err != nil {
		return false, err
	}
	return c.IsS3File(keys...)
}

func (c *Client) IsS3PathPrefix(s3Paths ...string) (bool, error) {
	prefixes, err := c.ExractS3PathPrefixes(s3Paths...)
	if err != nil {
		return false, err
	}
	return c.IsS3Prefix(prefixes...)
}

func (c *Client) IsS3PathDir(s3Paths ...string) (bool, error) {
	dirPaths, err := c.ExractS3PathPrefixes(s3Paths...)
	if err != nil {
		return false, err
	}
	return c.IsS3Prefix(dirPaths...)
}

func (c *Client) UploadBytesToS3(data []byte, key string) error {
	_, err := c.S3.PutObject(&s3.PutObjectInput{
		Body:                 bytes.NewReader(data),
		Key:                  aws.String(key),
		Bucket:               aws.String(c.Bucket),
		ACL:                  aws.String("private"),
		ContentDisposition:   aws.String("attachment"),
		ServerSideEncryption: aws.String("AES256"),
	})
	return errors.Wrap(err, key)
}

func (c *Client) UploadBytesesToS3(data []byte, keys ...string) error {
	fns := make([]func() error, len(keys))
	for i, key := range keys {
		key := key
		fns[i] = func() error {
			return c.UploadBytesToS3(data, key)
		}
	}
	return parallel.RunFirstErr(fns...)
}

func (c *Client) UploadFileToS3(filePath string, key string) error {
	data, err := files.ReadFileBytes(filePath)
	if err != nil {
		return err
	}
	return c.UploadBytesToS3(data, key)
}

func (c *Client) UploadBufferToS3(buffer *bytes.Buffer, key string) error {
	return c.UploadBytesToS3(buffer.Bytes(), key)
}

func (c *Client) UploadStringToS3(str string, key string) error {
	str = strings.TrimSpace(str)
	return c.UploadBytesToS3([]byte(str), key)
}

func (c *Client) UploadJSONToS3(obj interface{}, key string) error {
	jsonBytes, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	return c.UploadBytesToS3(jsonBytes, key)
}

func (c *Client) ReadJSONFromS3(objPtr interface{}, key string) error {
	jsonBytes, err := c.ReadBytesFromS3(key)
	if err != nil {
		return err
	}
	return errors.Wrap(json.Unmarshal(jsonBytes, objPtr), key)
}

func (c *Client) UploadMsgpackToS3(obj interface{}, key string) error {
	msgpackBytes, err := msgpack.Marshal(obj)
	if err != nil {
		return err
	}
	return c.UploadBytesToS3(msgpackBytes, key)
}

func (c *Client) ReadMsgpackFromS3(objPtr interface{}, key string) error {
	msgpackBytes, err := c.ReadBytesFromS3(key)
	if err != nil {
		return err
	}
	return errors.Wrap(msgpack.Unmarshal(msgpackBytes, objPtr), key)
}

func (c *Client) ReadStringFromS3(key string) (string, error) {
	response, err := c.S3.GetObject(&s3.GetObjectInput{
		Key:    aws.String(key),
		Bucket: aws.String(c.Bucket),
	})

	if err != nil {
		return "", errors.Wrap(err, key)
	}

	buf := new(bytes.Buffer)
	buf.ReadFrom(response.Body)
	return buf.String(), nil
}

func (c *Client) ReadBytesFromS3(key string) ([]byte, error) {
	response, err := c.S3.GetObject(&s3.GetObjectInput{
		Key:    aws.String(key),
		Bucket: aws.String(c.Bucket),
	})

	if err != nil {
		return nil, errors.Wrap(err, key)
	}

	buf := new(bytes.Buffer)
	buf.ReadFrom(response.Body)
	return buf.Bytes(), nil
}

func (c *Client) ListPrefix(prefix string, maxResults int64) ([]*s3.Object, error) {
	listObjectsInput := &s3.ListObjectsV2Input{
		Bucket:  aws.String(c.Bucket),
		Prefix:  aws.String(prefix),
		MaxKeys: aws.Int64(maxResults),
	}

	output, err := c.S3.ListObjectsV2(listObjectsInput)
	if err != nil {
		return nil, errors.Wrap(err, prefix)
	}

	return output.Contents, nil
}

func (c *Client) DeleteFromS3ByPrefix(prefix string, continueIfFailure bool) error {
	listObjectsInput := &s3.ListObjectsV2Input{
		Bucket:  aws.String(c.Bucket),
		Prefix:  aws.String(prefix),
		MaxKeys: aws.Int64(1000),
	}

	var subErr error

	err := c.S3.ListObjectsV2Pages(listObjectsInput,
		func(listObjectsOutput *s3.ListObjectsV2Output, lastPage bool) bool {
			deleteObjects := make([]*s3.ObjectIdentifier, len(listObjectsOutput.Contents))
			for i, object := range listObjectsOutput.Contents {
				deleteObjects[i] = &s3.ObjectIdentifier{Key: object.Key}
			}
			deleteObjectsInput := &s3.DeleteObjectsInput{
				Bucket: aws.String(c.Bucket),
				Delete: &s3.Delete{
					Objects: deleteObjects,
					Quiet:   aws.Bool(true),
				},
			}
			_, newSubErr := c.S3.DeleteObjects(deleteObjectsInput)
			if newSubErr != nil {
				subErr = newSubErr
				if !continueIfFailure {
					return false
				}
			}
			return true
		})

	if subErr != nil {
		return errors.Wrap(subErr, prefix)
	}
	return errors.Wrap(err, prefix)
}

func IsValidS3Path(s3Path string) bool {
	if !strings.HasPrefix(s3Path, "s3://") {
		return false
	}
	parts := strings.Split(s3Path[5:], "/")
	if len(parts) < 2 {
		return false
	}
	if parts[0] == "" || parts[1] == "" {
		return false
	}
	return true
}

func IsValidS3aPath(s3aPath string) bool {
	if !strings.HasPrefix(s3aPath, "s3a://") {
		return false
	}
	parts := strings.Split(s3aPath[6:], "/")
	if len(parts) < 2 {
		return false
	}
	if parts[0] == "" || parts[1] == "" {
		return false
	}
	return true
}

func SplitS3aPath(s3aPath string) (string, string, error) {
	if !IsValidS3aPath(s3aPath) {
		return "", "", ErrorInvalidS3aPath(s3aPath)
	}
	fullPath := s3aPath[len("s3a://"):]
	slashIndex := strings.Index(fullPath, "/")
	bucket := fullPath[0:slashIndex]
	key := fullPath[slashIndex+1:]

	return bucket, key, nil
}

func SplitS3Path(s3Path string) (string, string, error) {
	if !IsValidS3Path(s3Path) {
		return "", "", ErrorInvalidS3aPath(s3Path)
	}
	fullPath := s3Path[len("s3://"):]
	slashIndex := strings.Index(fullPath, "/")
	bucket := fullPath[0:slashIndex]
	key := fullPath[slashIndex+1:]

	return bucket, key, nil
}

func (c *Client) ExractS3PathPrefixes(s3Paths ...string) ([]string, error) {
	prefixes := make([]string, len(s3Paths))
	for i, s3Path := range s3Paths {
		bucket, prefix, err := SplitS3Path(s3Path)
		if err != nil {
			return nil, err
		}
		if bucket != c.Bucket {
			return nil, errors.New(fmt.Sprintf("bucket of S3 path %s does not match client bucket (%s)", s3Path, c.Bucket)) // unexpected
		}
		prefixes[i] = prefix
	}
	return prefixes, nil
}

func GetBucketRegion(bucket string) (string, error) {
	sess := session.Must(session.NewSession())
	region, err := s3manager.GetBucketRegion(aws.BackgroundContext(), sess, bucket, endpoints.UsWest2RegionID)
	if err != nil {
		return "", ErrorBucketInaccessible(bucket)
	}
	return region, nil
}
