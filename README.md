# repohost

Publish Debian (apt) and RPM (yum/dnf) package repositories to cloud object
storage — S3, GCS, or anything S3-compatible.

If you ship Linux packages for your software, you normally need somewhere to
host them. repohost turns a plain object-storage bucket into that host: it
writes the index metadata apt and dnf expect (`dists/`, `repodata/`), signs it
with your PGP key, and prunes old versions. Clients point `apt-get` or `dnf`
straight at the bucket's URL, so there is no server to run and no repository
SaaS to pay for.

It is available both as a Go library and as a `repohost` CLI.

**Design trade-offs.** repohost is built for the common case of publishing your
own software's packages, not for mirroring a distro:

- It keeps a bounded number of versions per package (`KeepVersions`), so
  repository state stays small enough to rewrite on every publish.
- It assumes a **single writer** per repository — run publishes serially (e.g.
  from one CI job), not concurrently.

Both apt and yum repositories can live in the same bucket: their key layouts
(`pool/` + `dists/` versus `Packages/` + `repodata/`) do not collide.

## Install

### CLI

```sh
# From source (requires Go 1.26+)
go install github.com/pete-woods/repohost/cmd/repohost@latest

# Or grab a prebuilt archive from the releases page
# https://github.com/pete-woods/repohost/releases

# Or, on macOS, via the Homebrew tap
brew install --cask pete-woods/tap/repohost
```

### Library

```sh
go get github.com/pete-woods/repohost
```

## Usage

### CLI

```sh
# Publish .deb packages to an apt repository in an S3 bucket
repohost push deb --bucket my-packages --distribution stable \
  --keep-versions 3 --sign-key ./signing-key.asc \
  dist/mytool_1.2.3_amd64.deb

# Publish .rpm packages to a yum/dnf repository in the same bucket
repohost push rpm --bucket my-packages \
  --keep-versions 3 --sign-key ./signing-key.asc \
  dist/mytool-1.2.3.x86_64.rpm

# Remove a specific version (metadata is regenerated and re-signed)
repohost rm deb --bucket my-packages --distribution stable mytool 1.2.3
repohost rm rpm --bucket my-packages mytool 1.2.3
```

The signing key's passphrase is read from `REPOHOST_SIGN_PASSPHRASE`, so it never
lands in your shell history. Omit `--sign-key` to publish an unsigned
repository. `--dry-run` logs what would happen without writing anything.

Use `--backend gcs` for Google Cloud Storage, or `--endpoint` plus
`--s3-path-style` for an S3-compatible service such as MinIO. Credentials come
from the usual AWS / Google environment (`AWS_*`, `GOOGLE_APPLICATION_CREDENTIALS`,
instance metadata, …). Run `repohost push deb --help` for the full flag list.

### Library

```go
package main

import (
	"context"
	"log"
	"os"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/pete-woods/repohost"
	"github.com/pete-woods/repohost/pgp"
)

func main() {
	ctx := context.Background()

	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatal(err)
	}
	backend := repohost.S3(s3.NewFromConfig(cfg), "my-packages")

	key, err := os.ReadFile("signing-key.asc")
	if err != nil {
		log.Fatal(err)
	}
	signer, err := pgp.NewSigner(key, os.Getenv("REPOHOST_SIGN_PASSPHRASE"))
	if err != nil {
		log.Fatal(err)
	}

	apt := repohost.NewAPT(backend, repohost.APTConfig{
		Distribution: "stable",
		Origin:       "mytool",
		KeepVersions: 3,
		Signer:       signer,
	})

	deb, err := os.Open("dist/mytool_1.2.3_amd64.deb")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = deb.Close() }()

	// "" means the default "main" component.
	if err := apt.Add(ctx, "", deb); err != nil {
		log.Fatal(err)
	}
}
```

`repohost.NewYUM` is the same shape with `YUMConfig` and `Add(ctx, rpm)`. Both
types also expose `Remove(...)`, which returns how many packages it deleted.

`repohost.GCS(client, bucket)` swaps in Google Cloud Storage; nothing else
changes. `Signer` is an interface — `pgp` is a convenience implementation, and
the core library takes no crypto dependency, so you can back signing with a KMS
or an HSM instead.

### Client setup

Make the bucket readable over HTTP (a public bucket policy, or a CDN in front of
it) and distribute your PGP public key to clients. Then, with `BASE_URL` set to
the bucket's public URL:

```sh
# Debian / Ubuntu
cp repohost-public-key.asc /etc/apt/keyrings/mytool.asc
echo "deb [signed-by=/etc/apt/keyrings/mytool.asc] $BASE_URL stable main" \
  > /etc/apt/sources.list.d/mytool.list
apt-get update && apt-get install mytool
```

```sh
# Fedora / RHEL
cp repohost-public-key.asc /etc/pki/rpm-gpg/mytool.asc
cat > /etc/yum.repos.d/mytool.repo <<EOF
[mytool]
name=mytool
baseurl=$BASE_URL/
enabled=1
gpgcheck=0
repo_gpgcheck=1
gpgkey=file:///etc/pki/rpm-gpg/mytool.asc
EOF
dnf install mytool
```

(`repo_gpgcheck=1` verifies the repository metadata signature; `gpgcheck`
controls per-package RPM signatures, which repohost does not manage.)

## Development

### Local

This repository makes use [Task](https://taskfile.dev/#/) which can be installed (on MacOS) with:

```
$ brew install go-task/tap/go-task
```

Most other tools referenced in the `Taskfile.yml` are managed by the go.mod tool section.

See the full list of available tasks by running `task -l`, or, see the [Taskfile.yml](./Taskfile.yml) script.

```sh
# Run all static checks (formatting, lint, license headers, go.mod, release config)
task check
# Auto-fix what can be fixed automatically
task fix
```

The tests include integration and acceptance tests, which need the Docker
Compose dependencies (SeaweedFS for S3, fake-gcs-server for GCS) and Docker
itself for the real `apt-get`/`dnf` install tests:

```sh
# Start the test dependencies and wait until they are healthy
task compose-up

# Run all the tests
task test
# Run the tests for one package
task test -- ./internal/apt/...

# Tear the dependencies down again
task compose-down
```
