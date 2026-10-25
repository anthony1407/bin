import { useEffect, useState, useMemo } from "react";
import { useSearchParams, Link } from "react-router";
import { Check, Copy, Terminal, KeyRound } from "lucide-react";
import Editor from "../components/CodeEditor";
import { api, type AccessKey, type Bucket, type Info } from "../api";
import { PageHeader, Card, Button, Input, Spinner, useToast } from "../components/ui";
import { Select } from "../components/Select";

type SDKLang = "javascript" | "python" | "go" | "java" | "dotnet" | "php" | "ruby" | "curl" | "cli";

// Tabs in display order, with the editor's highlighting for each.
const LANG_TABS: { id: SDKLang; label: string; editor: string; terminal?: boolean }[] = [
  { id: "javascript", label: "TypeScript / Node.js", editor: "typescript" },
  { id: "python", label: "Python (boto3)", editor: "python" },
  { id: "go", label: "Go (aws-sdk-go-v2)", editor: "go" },
  { id: "java", label: "Java (SDK v2)", editor: "java" },
  { id: "dotnet", label: "C# / .NET", editor: "csharp" },
  { id: "php", label: "PHP", editor: "php" },
  { id: "ruby", label: "Ruby", editor: "ruby" },
  { id: "curl", label: "cURL", editor: "shell", terminal: true },
  { id: "cli", label: "AWS CLI & rclone", editor: "shell", terminal: true },
];
type SDKOp = "all" | "init" | "upload" | "download" | "list";

export function ConnectSDKPage() {
  const [searchParams] = useSearchParams();
  const initialBucket = searchParams.get("bucket") || "";
  const initialKey = searchParams.get("key") || "sample-file.txt";

  const [info, setInfo] = useState<Info | null>(null);
  const [buckets, setBuckets] = useState<Bucket[]>([]);
  const [keys, setKeys] = useState<AccessKey[]>([]);
  const [selectedBucket, setSelectedBucket] = useState(initialBucket);
  const [selectedKey, setSelectedKey] = useState(initialKey);
  const [selectedAccessKey, setSelectedAccessKey] = useState("");
  const [secretKey, setSecretKey] = useState("YOUR_SECRET_KEY");
  const [lang, setLang] = useState<SDKLang>("javascript");
  const [operation, setOperation] = useState<SDKOp>("all");
  const [copied, setCopied] = useState(false);
  const [loading, setLoading] = useState(true);
  const toast = useToast();

  useEffect(() => {
    Promise.all([
      api.info().catch(() => null),
      api.buckets().catch(() => []),
      api.accessKeys().catch(() => []),
    ]).then(([infoRes, bucketsRes, keysRes]) => {
      if (infoRes) setInfo(infoRes);
      if (bucketsRes) {
        setBuckets(bucketsRes);
        if (!initialBucket && bucketsRes.length > 0) {
          setSelectedBucket(bucketsRes[0].name);
        }
      }
      if (keysRes && keysRes.length > 0) {
        setKeys(keysRes);
        setSelectedAccessKey(keysRes[0].accessKey);
        if (keysRes[0].secretKey) setSecretKey(keysRes[0].secretKey);
      }
      setLoading(false);
    });
  }, [initialBucket]);

  const endpoint = useMemo(() => {
    if (info?.publicUrl) return info.publicUrl;
    if (info?.s3Endpoint) return info.s3Endpoint;
    return `${window.location.protocol}//${window.location.hostname}:9000`;
  }, [info]);

  const region = info?.region || "us-east-1";
  const bucketName = selectedBucket || "my-bucket";
  const objectKey = selectedKey || "sample-file.txt";
  const accessKeyId = selectedAccessKey || "YOUR_ACCESS_KEY";

  const codeSnippet = useMemo(() => {
    if (lang === "javascript") {
      if (operation === "init") {
        return `// Install: npm install @aws-sdk/client-s3
import { S3Client } from "@aws-sdk/client-s3";

export const s3 = new S3Client({
  endpoint: "${endpoint}",
  region: "${region}",
  credentials: {
    accessKeyId: "${accessKeyId}",
    secretAccessKey: "${secretKey}",
  },
  forcePathStyle: true, // Required for custom S3 endpoints
});`;
      }
      if (operation === "upload") {
        return `import { S3Client, PutObjectCommand } from "@aws-sdk/client-s3";
import { readFileSync } from "fs";

const s3 = new S3Client({
  endpoint: "${endpoint}",
  region: "${region}",
  credentials: {
    accessKeyId: "${accessKeyId}",
    secretAccessKey: "${secretKey}",
  },
  forcePathStyle: true,
});

async function uploadFile() {
  const fileContent = readFileSync("./${objectKey}");
  
  await s3.send(
    new PutObjectCommand({
      Bucket: "${bucketName}",
      Key: "${objectKey}",
      Body: fileContent,
      ContentType: "text/plain",
    })
  );
  console.log("Uploaded ${objectKey} to ${bucketName} successfully!");
}

uploadFile().catch(console.error);`;
      }
      if (operation === "download") {
        return `import { S3Client, GetObjectCommand } from "@aws-sdk/client-s3";
import { writeFileSync } from "fs";

const s3 = new S3Client({
  endpoint: "${endpoint}",
  region: "${region}",
  credentials: {
    accessKeyId: "${accessKeyId}",
    secretAccessKey: "${secretKey}",
  },
  forcePathStyle: true,
});

async function downloadFile() {
  const response = await s3.send(
    new GetObjectCommand({
      Bucket: "${bucketName}",
      Key: "${objectKey}",
    })
  );
  
  const bytes = await response.Body?.transformToByteArray();
  if (bytes) {
    writeFileSync("./downloaded-${objectKey}", bytes);
    console.log("Downloaded ${objectKey} successfully!");
  }
}

downloadFile().catch(console.error);`;
      }
      if (operation === "list") {
        return `import { S3Client, ListObjectsV2Command } from "@aws-sdk/client-s3";

const s3 = new S3Client({
  endpoint: "${endpoint}",
  region: "${region}",
  credentials: {
    accessKeyId: "${accessKeyId}",
    secretAccessKey: "${secretKey}",
  },
  forcePathStyle: true,
});

async function listFiles() {
  const result = await s3.send(
    new ListObjectsV2Command({
      Bucket: "${bucketName}",
      MaxKeys: 100,
    })
  );
  
  console.log("Objects in ${bucketName}:");
  result.Contents?.forEach((item) => {
    console.log(\`- \${item.Key} (\${item.Size} bytes)\`);
  });
}

listFiles().catch(console.error);`;
      }

      // Default: Complete Starter
      return `// ==========================================
// bin S3 Client - JavaScript / TypeScript
// Install: npm install @aws-sdk/client-s3
// ==========================================

import {
  S3Client,
  PutObjectCommand,
  GetObjectCommand,
  ListObjectsV2Command,
} from "@aws-sdk/client-s3";

const s3 = new S3Client({
  endpoint: "${endpoint}",
  region: "${region}",
  credentials: {
    accessKeyId: "${accessKeyId}",
    secretAccessKey: "${secretKey}",
  },
  forcePathStyle: true, // Always required for bin / path-style
});

async function main() {
  // 1. Upload Object
  console.log("Uploading file to ${bucketName}...");
  await s3.send(
    new PutObjectCommand({
      Bucket: "${bucketName}",
      Key: "${objectKey}",
      Body: "Hello from bin storage!",
      ContentType: "text/plain",
    })
  );

  // 2. List Objects
  console.log("\\nListing bucket objects:");
  const list = await s3.send(new ListObjectsV2Command({ Bucket: "${bucketName}" }));
  list.Contents?.forEach((o) => console.log(\`  - \${o.Key} (\${o.Size} bytes)\`));

  // 3. Download Object
  const data = await s3.send(
    new GetObjectCommand({
      Bucket: "${bucketName}",
      Key: "${objectKey}",
    })
  );
  const text = await data.Body?.transformToString();
  console.log("\\nDownloaded content:", text);
}

main().catch(console.error);`;
    }

    if (lang === "python") {
      if (operation === "upload") {
        return `import boto3

s3 = boto3.client(
    "s3",
    endpoint_url="${endpoint}",
    aws_access_key_id="${accessKeyId}",
    aws_secret_access_key="${secretKey}",
    region_name="${region}",
)

s3.upload_file("./${objectKey}", "${bucketName}", "${objectKey}")
print("Uploaded ${objectKey} to ${bucketName}")`;
      }

      if (operation === "download") {
        return `import boto3

s3 = boto3.client(
    "s3",
    endpoint_url="${endpoint}",
    aws_access_key_id="${accessKeyId}",
    aws_secret_access_key="${secretKey}",
    region_name="${region}",
)

s3.download_file("${bucketName}", "${objectKey}", "./downloaded-${objectKey}")
print("Downloaded ${objectKey} successfully")`;
      }

      return `# ==========================================
# bin S3 Client - Python (boto3)
# Install: pip install boto3
# ==========================================

import boto3
from botocore.client import Config

s3 = boto3.client(
    "s3",
    endpoint_url="${endpoint}",
    aws_access_key_id="${accessKeyId}",
    aws_secret_access_key="${secretKey}",
    region_name="${region}",
    config=Config(s3={"addressing_style": "path"}),
)

# 1. Upload Object
s3.put_object(
    Bucket="${bucketName}",
    Key="${objectKey}",
    Body=b"Hello from bin Python SDK!",
    ContentType="text/plain",
)
print("Uploaded ${objectKey} to ${bucketName}")

# 2. List Objects
response = s3.list_objects_v2(Bucket="${bucketName}")
print("\\nBucket objects:")
for item in response.get("Contents", []):
    print(f"  - {item['Key']} ({item['Size']} bytes)")

# 3. Download Object
obj = s3.get_object(Bucket="${bucketName}", Key="${objectKey}")
content = obj["Body"].read().decode("utf-8")
print("\\nObject content:", content)

# 4. Generate Presigned URL (1 hour)
url = s3.generate_presigned_url(
    "get_object",
    Params={"Bucket": "${bucketName}", "Key": "${objectKey}"},
    ExpiresIn=3600,
)
print("\\nPresigned URL:", url)`;
    }

    if (lang === "go") {
      return `// ==========================================
// bin S3 Client - Go (AWS SDK for Go v2)
// Install: go get github.com/aws/aws-sdk-go-v2/config \\
//            github.com/aws/aws-sdk-go-v2/credentials \\
//            github.com/aws/aws-sdk-go-v2/service/s3
// ==========================================

package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func main() {
	ctx := context.Background()

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("${region}"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("${accessKeyId}", "${secretKey}", "")),
	)
	if err != nil {
		log.Fatalln(err)
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String("${endpoint}")
		o.UsePathStyle = true // Always required for bin / path-style
	})

	// 1. Put Object
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String("${bucketName}"),
		Key:         aws.String("${objectKey}"),
		Body:        strings.NewReader("Hello from bin Go Client!"),
		ContentType: aws.String("text/plain"),
	})
	if err != nil {
		log.Fatalln("Upload failed:", err)
	}
	fmt.Println("Uploaded ${objectKey} to ${bucketName}")

	// 2. List Objects
	fmt.Println("\\nObjects in ${bucketName}:")
	pages := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: aws.String("${bucketName}"),
	})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			log.Fatalln("List failed:", err)
		}
		for _, object := range page.Contents {
			fmt.Printf("  - %s (%d bytes)\\n", aws.ToString(object.Key), aws.ToInt64(object.Size))
		}
	}
}`;
    }

    if (lang === "curl") {
      // curl sends the path as written: encode each segment of the key.
      const keyPath = objectKey.split("/").map(encodeURIComponent).join("/");
      return `# ==========================================
# bin S3 API with cURL (SigV4 signing built in)
# Needs curl 7.75+; curl 8 also sends the payload hash S3 requires.
# ==========================================

AUTH=(--aws-sigv4 "aws:amz:${region}:s3" --user "${accessKeyId}:${secretKey}")
S3="${endpoint}"

# 1. List buckets
curl "\${AUTH[@]}" "$S3/"

# 2. List objects in ${bucketName} (add &prefix=folder/ to narrow it)
curl "\${AUTH[@]}" "$S3/${bucketName}/?list-type=2"

# 3. Upload a file
curl "\${AUTH[@]}" -T ./local-file.txt "$S3/${bucketName}/${keyPath}"

# 4. Download it
curl "\${AUTH[@]}" -o ./downloaded-file "$S3/${bucketName}/${keyPath}"

# 5. Object metadata only (size, type, ETag)
curl "\${AUTH[@]}" -I "$S3/${bucketName}/${keyPath}"

# 6. Delete it
curl "\${AUTH[@]}" -X DELETE "$S3/${bucketName}/${keyPath}"

# 7. Create a bucket
curl "\${AUTH[@]}" -X PUT "$S3/new-bucket-name"

# Keys with spaces or special characters must be URL-encoded,
# e.g. "my report.pdf" -> my%20report.pdf
# Windows PowerShell: use curl.exe and --user "${accessKeyId}:${secretKey}" inline.`;
    }

    if (lang === "java") {
      return `// ==========================================
// bin S3 Client - Java (AWS SDK for Java v2)
// Maven: software.amazon.awssdk:s3
// ==========================================

import java.net.URI;
import java.nio.file.Paths;
import software.amazon.awssdk.auth.credentials.AwsBasicCredentials;
import software.amazon.awssdk.auth.credentials.StaticCredentialsProvider;
import software.amazon.awssdk.core.sync.RequestBody;
import software.amazon.awssdk.core.sync.ResponseTransformer;
import software.amazon.awssdk.regions.Region;
import software.amazon.awssdk.services.s3.S3Client;
import software.amazon.awssdk.services.s3.model.*;

public class BinExample {
    public static void main(String[] args) {
        S3Client s3 = S3Client.builder()
            .endpointOverride(URI.create("${endpoint}"))
            .region(Region.of("${region}"))
            .credentialsProvider(StaticCredentialsProvider.create(
                AwsBasicCredentials.create("${accessKeyId}", "${secretKey}")))
            .forcePathStyle(true) // Always required for bin / path-style
            .build();

        // 1. Upload
        s3.putObject(PutObjectRequest.builder()
                .bucket("${bucketName}").key("${objectKey}").contentType("text/plain").build(),
            RequestBody.fromString("Hello from bin Java Client!"));

        // 2. Download
        s3.getObject(GetObjectRequest.builder().bucket("${bucketName}").key("${objectKey}").build(),
            ResponseTransformer.toFile(Paths.get("downloaded-file")));

        // 3. List
        s3.listObjectsV2Paginator(ListObjectsV2Request.builder().bucket("${bucketName}").build())
            .contents()
            .forEach(o -> System.out.printf("  - %s (%d bytes)%n", o.key(), o.size()));
    }
}`;
    }

    if (lang === "dotnet") {
      return `// ==========================================
// bin S3 Client - C# / .NET (AWSSDK.S3)
// Install: dotnet add package AWSSDK.S3
// ==========================================

using Amazon.Runtime;
using Amazon.S3;
using Amazon.S3.Model;

var s3 = new AmazonS3Client(
    new BasicAWSCredentials("${accessKeyId}", "${secretKey}"),
    new AmazonS3Config
    {
        ServiceURL = "${endpoint}",
        AuthenticationRegion = "${region}",
        ForcePathStyle = true, // Always required for bin / path-style
    });

// 1. Upload
await s3.PutObjectAsync(new PutObjectRequest
{
    BucketName = "${bucketName}",
    Key = "${objectKey}",
    ContentBody = "Hello from bin .NET Client!",
    ContentType = "text/plain",
});

// 2. Download
using (var obj = await s3.GetObjectAsync("${bucketName}", "${objectKey}"))
{
    await obj.WriteResponseStreamToFileAsync("downloaded-file", false, default);
}

// 3. List
var page = await s3.ListObjectsV2Async(new ListObjectsV2Request { BucketName = "${bucketName}" });
foreach (var o in page.S3Objects)
    Console.WriteLine($"  - {o.Key} ({o.Size} bytes)");`;
    }

    if (lang === "php") {
      return `<?php
// ==========================================
// bin S3 Client - PHP (AWS SDK for PHP v3)
// Install: composer require aws/aws-sdk-php
// ==========================================

require 'vendor/autoload.php';

use Aws\\S3\\S3Client;

$s3 = new S3Client([
    'version'                 => 'latest',
    'region'                  => '${region}',
    'endpoint'                => '${endpoint}',
    'use_path_style_endpoint' => true, // Always required for bin / path-style
    'credentials'             => ['key' => '${accessKeyId}', 'secret' => '${secretKey}'],
]);

// 1. Upload
$s3->putObject([
    'Bucket'      => '${bucketName}',
    'Key'         => '${objectKey}',
    'Body'        => 'Hello from bin PHP Client!',
    'ContentType' => 'text/plain',
]);

// 2. Download
$s3->getObject(['Bucket' => '${bucketName}', 'Key' => '${objectKey}', 'SaveAs' => 'downloaded-file']);

// 3. List
foreach ($s3->getPaginator('ListObjectsV2', ['Bucket' => '${bucketName}']) as $page) {
    foreach ($page['Contents'] ?? [] as $o) {
        echo "  - {$o['Key']} ({$o['Size']} bytes)\\n";
    }
}

// 4. A link that works for an hour without credentials
$url = (string) $s3->createPresignedRequest(
    $s3->getCommand('GetObject', ['Bucket' => '${bucketName}', 'Key' => '${objectKey}']),
    '+1 hour'
)->getUri();
echo $url, "\\n";`;
    }

    if (lang === "ruby") {
      return `# ==========================================
# bin S3 Client - Ruby (aws-sdk-s3)
# Install: gem install aws-sdk-s3
# ==========================================

require "aws-sdk-s3"

s3 = Aws::S3::Client.new(
  endpoint: "${endpoint}",
  region: "${region}",
  access_key_id: "${accessKeyId}",
  secret_access_key: "${secretKey}",
  force_path_style: true # Always required for bin / path-style
)

# 1. Upload
s3.put_object(bucket: "${bucketName}", key: "${objectKey}",
              body: "Hello from bin Ruby Client!", content_type: "text/plain")

# 2. Download
s3.get_object(bucket: "${bucketName}", key: "${objectKey}", response_target: "downloaded-file")

# 3. List
s3.list_objects_v2(bucket: "${bucketName}").each do |page|
  page.contents.each { |o| puts "  - #{o.key} (#{o.size} bytes)" }
end

# 4. A link that works for an hour without credentials
puts Aws::S3::Presigner.new(client: s3)
  .presigned_url(:get_object, bucket: "${bucketName}", key: "${objectKey}", expires_in: 3600)`;
    }

    // AWS CLI / rclone
    return `# ==========================================
# bin S3 Client - AWS CLI & rclone
# ==========================================

# 1. AWS CLI Configuration
aws configure set aws_access_key_id "${accessKeyId}"
aws configure set aws_secret_access_key "${secretKey}"
aws configure set default.region "${region}"

# 2. List Objects in Bucket
aws --endpoint-url ${endpoint} s3 ls s3://${bucketName}/

# 3. Upload File
aws --endpoint-url ${endpoint} s3 cp ./local-file.txt s3://${bucketName}/${objectKey}

# 4. Download File
aws --endpoint-url ${endpoint} s3 cp s3://${bucketName}/${objectKey} ./downloaded-${objectKey}

# 5. Sync Directory
aws --endpoint-url ${endpoint} s3 sync ./my-folder s3://${bucketName}/backup/

# ------------------------------------------
# rclone Configuration (~/.config/rclone/rclone.conf)
# ------------------------------------------
# [bin]
# type = s3
# provider = Other
# env_auth = false
# access_key_id = ${accessKeyId}
# secret_access_key = ${secretKey}
# endpoint = ${endpoint}
# acl = private
#
# Usage:
# rclone ls bin:${bucketName}
# rclone copy ./files bin:${bucketName}/sync/`;
  }, [lang, operation, endpoint, region, bucketName, objectKey, accessKeyId, secretKey]);

  function copyCode() {
    navigator.clipboard.writeText(codeSnippet).then(() => {
      setCopied(true);
      toast("ok", "Code snippet copied to clipboard!");
      setTimeout(() => setCopied(false), 2000);
    });
  }

  if (loading) {
    return (
      <div className="flex justify-center py-20">
        <Spinner />
      </div>
    );
  }

  return (
    <div className="space-y-6 pb-16">
      <PageHeader
        title="Connect SDK & Client Code Generator"
        subtitle="Ready-to-use snippets with active credentials and your S3 endpoint for any language."
      />

      {/* Top Configuration Bar */}
      {/* A form only so the browser accepts the password field; it never submits. */}
      <form onSubmit={(e) => e.preventDefault()}>
      <Card className="p-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-4 bg-sunken/40">
        <div>
          <label htmlFor="connectsdk-target-bucket" className="label-strip mb-1.5 block">Target Bucket</label>
          <Select
            id="connectsdk-target-bucket"
            aria-label="Target Bucket"
            value={selectedBucket}
            onChange={setSelectedBucket}
            options={buckets.map((b) => ({ value: b.name, label: b.name }))}
            emptyText="No buckets available"
          />
        </div>

        <div>
          <label htmlFor="connectsdk-object-key" className="label-strip mb-1.5 block">Object Key (Sample)</label>
          <Input
            id="connectsdk-object-key"
            value={selectedKey}
            onChange={(e) => setSelectedKey(e.target.value)}
            placeholder="folder/file.ext"
            className="font-mono text-xs"
            autoComplete="username"
          />
        </div>

        <div>
          <label htmlFor="connectsdk-access-key" className="label-strip mb-1.5 block">Access Key</label>
          {keys.length > 0 ? (
            <Select
              id="connectsdk-access-key"
              aria-label="Access Key"
              value={selectedAccessKey}
              onChange={(val) => {
                setSelectedAccessKey(val);
                const match = keys.find((k) => k.accessKey === val);
                if (match?.secretKey) setSecretKey(match.secretKey);
              }}
              options={keys.map((k) => ({ value: k.accessKey, label: `${k.name || k.accessKey} (${k.accessKey.slice(0, 8)}…)` }))}
            />
          ) : (
            <Input
              id="connectsdk-access-key"
              autoComplete="username"
              value={selectedAccessKey}
              onChange={(e) => setSelectedAccessKey(e.target.value)}
              placeholder="BIN_ROOT_ACCESS_KEY"
              className="font-mono text-xs"
            />
          )}
        </div>

        <div>
          <label htmlFor="connectsdk-secret-key" className="label-strip mb-1.5 block">Secret Key</label>
          <div className="relative">
            <Input
              id="connectsdk-secret-key"
              type="password"
              autoComplete="current-password"
              value={secretKey}
              onChange={(e) => setSecretKey(e.target.value)}
              className="font-mono text-xs pr-10"
            />
            <span className="absolute right-2.5 top-1/2 -translate-y-1/2 text-faint text-[10px]">
              SigV4
            </span>
          </div>
        </div>
      </Card>
      </form>

      {/* Main Code View */}
      <div className="rounded-xl border border-line bg-card overflow-hidden shadow-sm">
        {/* Navigation & Tabs */}
        <div className="flex flex-col sm:flex-row sm:items-center justify-between border-b border-line bg-sunken/60 px-4 py-2.5 gap-3">
          <div className="flex items-center gap-1 overflow-x-auto pb-1 sm:pb-0" role="tablist" aria-label="Language">
            {LANG_TABS.map((t) => (
              <button
                key={t.id}
                role="tab"
                aria-selected={lang === t.id}
                onClick={() => setLang(t.id)}
                className={`flex shrink-0 items-center gap-1.5 whitespace-nowrap px-3 py-1.5 text-xs font-medium rounded-lg transition-colors ${
                  lang === t.id ? "bg-accent text-white shadow-sm font-semibold" : "text-muted hover:text-ink hover:bg-sunken"
                }`}
              >
                {t.terminal && <Terminal className="size-3.5" />}
                <span>{t.label}</span>
              </button>
            ))}
          </div>

          <div className="flex items-center gap-2">
            {lang === "javascript" && (
              <div className="flex rounded-md border border-line bg-card p-0.5 text-[11px]">
                <button
                  onClick={() => setOperation("all")}
                  className={`px-2 py-0.5 rounded ${operation === "all" ? "bg-accent/20 text-accent font-semibold" : "text-muted"}`}
                >
                  Full Demo
                </button>
                <button
                  onClick={() => setOperation("init")}
                  className={`px-2 py-0.5 rounded ${operation === "init" ? "bg-accent/20 text-accent font-semibold" : "text-muted"}`}
                >
                  Client Init
                </button>
                <button
                  onClick={() => setOperation("upload")}
                  className={`px-2 py-0.5 rounded ${operation === "upload" ? "bg-accent/20 text-accent font-semibold" : "text-muted"}`}
                >
                  Upload
                </button>
                <button
                  onClick={() => setOperation("download")}
                  className={`px-2 py-0.5 rounded ${operation === "download" ? "bg-accent/20 text-accent font-semibold" : "text-muted"}`}
                >
                  Download
                </button>
                <button
                  onClick={() => setOperation("list")}
                  className={`px-2 py-0.5 rounded ${operation === "list" ? "bg-accent/20 text-accent font-semibold" : "text-muted"}`}
                >
                  List
                </button>
              </div>
            )}

            <Button
              variant="primary"
              icon={copied ? <Check className="size-3.5 text-ok" /> : <Copy className="size-3.5" />}
              onClick={copyCode}
            >
              {copied ? "Copied!" : "Copy Code"}
            </Button>
          </div>
        </div>

        {/* Code Editor Preview */}
        <div className="h-[460px] w-full bg-[#1e1e1e]">
          <Editor
            height="100%"
            language={LANG_TABS.find((t) => t.id === lang)?.editor ?? "shell"}
            value={codeSnippet}
            theme="vs-dark"
            options={{
              readOnly: true,
              fontSize: 13,
              fontFamily: "'IBM Plex Mono', monospace",
              minimap: { enabled: false },
              lineNumbers: "on",
              wordWrap: "on",
              padding: { top: 12, bottom: 12 },
              scrollBeyondLastLine: false,
            }}
          />
        </div>

        {/* Quick Tips Footer */}
        <div className="border-t border-line bg-sunken/40 px-4 py-3 flex flex-wrap items-center justify-between text-xs text-muted gap-2">
          <div className="flex items-center gap-4">
            <span className="flex items-center gap-1.5">
              <span className="size-2 rounded-full bg-ok inline-block" />
              Endpoint: <code className="font-mono text-ink text-[11px] bg-sunken px-1.5 py-0.5 rounded">{endpoint}</code>
            </span>
            <span>
              Region: <code className="font-mono text-ink text-[11px] bg-sunken px-1.5 py-0.5 rounded">{region}</code>
            </span>
          </div>

          <div className="flex items-center gap-3">
            <Link
              to="/access-keys"
              className="flex items-center gap-1 text-accent hover:underline font-medium text-[11px]"
            >
              <KeyRound className="size-3" /> Manage Access Keys
            </Link>
          </div>
        </div>
      </div>
    </div>
  );
}
