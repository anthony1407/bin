// Command crashtest checks that acknowledged writes survive a crash.
//
//	crashtest write  -kind buckets|objects -endpoint URL -out acked.txt -for 10s
//	crashtest verify -kind buckets|objects -endpoint URL -out acked.txt
//
// Run "write", SIGKILL the server mid-way, restart it, then run "verify".
// For objects, every acknowledged object is downloaded and its SHA-256
// compared with what was uploaded.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const objectBucket = "crash-objects"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: crashtest write|verify [flags]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	kind := fs.String("kind", "buckets", "what to write: buckets or objects")
	endpoint := fs.String("endpoint", "http://localhost:19000", "S3 endpoint")
	out := fs.String("out", "acked.txt", "file listing acknowledged writes")
	dur := fs.Duration("for", 10*time.Second, "how long to keep writing")
	ak := fs.String("ak", "testaccesskey", "access key")
	sk := fs.String("sk", "testsecretkey12345", "secret key")
	fs.Parse(os.Args[2:])

	client := s3.New(s3.Options{
		BaseEndpoint:     aws.String(*endpoint),
		Region:           "us-east-1",
		UsePathStyle:     true,
		Credentials:      credentials.NewStaticCredentialsProvider(*ak, *sk, ""),
		RetryMaxAttempts: 1,
	})
	switch os.Args[1] + "/" + *kind {
	case "write/buckets":
		write(*out, *dur, func(ctx context.Context, i int64) (string, error) {
			name := fmt.Sprintf("crash-%06d", i)
			_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(name)})
			return name, err
		})
	case "write/objects":
		client.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String(objectBucket)})
		write(*out, *dur, func(ctx context.Context, i int64) (string, error) {
			data := make([]byte, 1024+mrand.IntN(1<<20))
			rand.Read(data)
			key := fmt.Sprintf("obj-%06d", i)
			_, err := client.PutObject(ctx, &s3.PutObjectInput{
				Bucket: aws.String(objectBucket), Key: aws.String(key), Body: bytes.NewReader(data),
			})
			sum := sha256.Sum256(data)
			return key + " " + hex.EncodeToString(sum[:]), err
		})
	case "verify/buckets":
		verifyBuckets(client, *out)
	case "verify/objects":
		verifyObjects(client, *out)
	default:
		fmt.Fprintln(os.Stderr, "unknown command or kind")
		os.Exit(2)
	}
}

// write runs 8 workers calling op until dur elapses, recording every
// acknowledged write.
func write(out string, dur time.Duration, op func(context.Context, int64) (string, error)) {
	f, err := os.Create(out)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	var mu sync.Mutex
	var next, acked, failed atomic.Int64
	ctx, cancel := context.WithTimeout(context.Background(), dur)
	defer cancel()

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				rctx, rcancel := context.WithTimeout(ctx, 5*time.Second)
				line, err := op(rctx, next.Add(1))
				rcancel()
				if err != nil {
					failed.Add(1)
					time.Sleep(50 * time.Millisecond)
					continue
				}
				acked.Add(1)
				mu.Lock()
				fmt.Fprintln(f, line)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	fmt.Printf("acknowledged=%d failed=%d\n", acked.Load(), failed.Load())
}

func verifyBuckets(c *s3.Client, out string) {
	res, err := c.ListBuckets(context.Background(), &s3.ListBucketsInput{})
	if err != nil {
		panic(err)
	}
	present := map[string]bool{}
	for _, b := range res.Buckets {
		present[aws.ToString(b.Name)] = true
	}
	acked, missing := 0, 0
	eachLine(out, func(line string) {
		acked++
		if !present[line] {
			missing++
			fmt.Println("MISSING", line)
		}
	})
	fmt.Printf("acknowledged=%d present_on_server=%d missing=%d\n", acked, len(res.Buckets), missing)
	if missing > 0 {
		os.Exit(1)
	}
}

func verifyObjects(c *s3.Client, out string) {
	acked, missing, corrupt := 0, 0, 0
	eachLine(out, func(line string) {
		acked++
		key, want, _ := strings.Cut(line, " ")
		res, err := c.GetObject(context.Background(), &s3.GetObjectInput{Bucket: aws.String(objectBucket), Key: aws.String(key)})
		if err != nil {
			missing++
			fmt.Println("MISSING", key, err)
			return
		}
		h := sha256.New()
		_, err = io.Copy(h, res.Body)
		res.Body.Close()
		if err != nil || hex.EncodeToString(h.Sum(nil)) != want {
			corrupt++
			fmt.Println("CORRUPT", key, err)
		}
	})
	fmt.Printf("acknowledged=%d missing=%d corrupt=%d\n", acked, missing, corrupt)
	if missing+corrupt > 0 {
		os.Exit(1)
	}
}

func eachLine(path string, fn func(string)) {
	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fn(sc.Text())
	}
}
