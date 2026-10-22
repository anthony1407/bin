package s3

import (
	"context"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

var listKeys = []string{
	"2024/a.jpg", "2024/b.jpg", "2024/sub/c.jpg", "2025/d.jpg",
	"folder with spaces/ñandú +1.txt", "readme.txt", "tmp/x", "zzz",
}

func seedList(t *testing.T) *awss3.Client {
	t.Helper()
	c := newE2E(t)
	mustBucket(t, c, "list")
	for _, k := range listKeys {
		if _, err := c.PutObject(context.Background(), &awss3.PutObjectInput{
			Bucket: aws.String("list"), Key: aws.String(k), Body: strings.NewReader("data"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func TestE2EListV2Paginated(t *testing.T) {
	c := seedList(t)
	for _, tc := range []struct {
		prefix, delimiter string
		want              []string
	}{
		{"", "", listKeys},
		{"", "/", []string{"2024/", "2025/", "folder with spaces/", "readme.txt", "tmp/", "zzz"}},
		{"2024/", "/", []string{"2024/a.jpg", "2024/b.jpg", "2024/sub/"}},
	} {
		var got []string
		pages := 0
		p := awss3.NewListObjectsV2Paginator(c, &awss3.ListObjectsV2Input{
			Bucket: aws.String("list"), Prefix: aws.String(tc.prefix), Delimiter: aws.String(tc.delimiter), MaxKeys: aws.Int32(2),
		})
		for p.HasMorePages() {
			out, err := p.NextPage(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			pages++
			if int(aws.ToInt32(out.KeyCount)) != len(out.Contents)+len(out.CommonPrefixes) || aws.ToInt32(out.MaxKeys) != 2 {
				t.Errorf("KeyCount %d for %d+%d entries", aws.ToInt32(out.KeyCount), len(out.Contents), len(out.CommonPrefixes))
			}
			for _, o := range out.Contents {
				got = append(got, aws.ToString(o.Key))
				if aws.ToInt64(o.Size) != 4 || aws.ToString(o.ETag) != `"8d777f385d3dfec8815d20f7496026dc"` || o.StorageClass != types.ObjectStorageClassStandard {
					t.Errorf("object fields: %+v", o)
				}
			}
			for _, cp := range out.CommonPrefixes {
				got = append(got, aws.ToString(cp.Prefix))
			}
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("prefix=%q delimiter=%q: got %q, want %q", tc.prefix, tc.delimiter, got, tc.want)
		}
		if want := (len(tc.want) + 1) / 2; pages != want {
			t.Errorf("prefix=%q delimiter=%q: %d pages, want %d", tc.prefix, tc.delimiter, pages, want)
		}
	}
}

func TestE2EListV2Options(t *testing.T) {
	ctx := context.Background()
	c := seedList(t)

	out, err := c.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
		Bucket: aws.String("list"), StartAfter: aws.String("2025/d.jpg"), FetchOwner: aws.Bool(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Contents) != 4 || aws.ToString(out.Contents[0].Key) != "folder with spaces/ñandú +1.txt" ||
		out.Contents[0].Owner == nil || aws.ToString(out.Contents[0].Owner.DisplayName) != "bin" {
		t.Errorf("start-after + fetch-owner: %+v", out.Contents)
	}

	// encoding-type=url: values arrive encoded and decode back to the key.
	out, err = c.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
		Bucket: aws.String("list"), Prefix: aws.String("folder with"), EncodingType: types.EncodingTypeUrl,
	})
	if err != nil || len(out.Contents) != 1 {
		t.Fatalf("encoded listing: %+v, %v", out, err)
	}
	raw := aws.ToString(out.Contents[0].Key)
	decoded, _ := url.PathUnescape(raw)
	if raw != "folder%20with%20spaces/%C3%B1and%C3%BA%20%2B1.txt" || decoded != "folder with spaces/ñandú +1.txt" {
		t.Errorf("encoded key %q decodes to %q", raw, decoded)
	}

	_, err = c.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{Bucket: aws.String("list"), ContinuationToken: aws.String("!!not base64!!")})
	if code := apiCode(err); code != "InvalidArgument" {
		t.Errorf("bad continuation token: %v", err)
	}
	_, err = c.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{Bucket: aws.String("missing")})
	if code := apiCode(err); code != "NoSuchBucket" {
		t.Errorf("missing bucket: %v", err)
	}
	out, err = c.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{Bucket: aws.String("list"), MaxKeys: aws.Int32(0)})
	if err != nil || len(out.Contents) != 0 || aws.ToBool(out.IsTruncated) {
		t.Errorf("max-keys=0: %+v, %v", out, err)
	}
}

func TestE2EListV1Markers(t *testing.T) {
	ctx := context.Background()
	c := seedList(t)
	var got []string
	marker := ""
	for i := 0; i < 20; i++ {
		out, err := c.ListObjects(ctx, &awss3.ListObjectsInput{
			Bucket: aws.String("list"), Delimiter: aws.String("/"), MaxKeys: aws.Int32(2), Marker: aws.String(marker),
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range out.Contents {
			got = append(got, aws.ToString(o.Key))
			if o.Owner == nil || aws.ToString(o.Owner.ID) != "owner-id" {
				t.Errorf("V1 contents need an Owner: %+v", o)
			}
		}
		for _, cp := range out.CommonPrefixes {
			got = append(got, aws.ToString(cp.Prefix))
		}
		if !aws.ToBool(out.IsTruncated) {
			break
		}
		marker = aws.ToString(out.NextMarker)
	}
	sort.Strings(got)
	want := []string{"2024/", "2025/", "folder with spaces/", "readme.txt", "tmp/", "zzz"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("V1 with markers: got %q, want %q", got, want)
	}
}
