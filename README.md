# Go HTTP/RPC Server Lab

A learning and testing project that exposes the same user service through four API styles: the Go standard library, Huma, schema-first OpenAPI, and ConnectRPC. All variants use `net/http.Server` and `http.ServeMux`. A fifth server executable demonstrates TLS 1.3 and mTLS. A client CLI exercises REST, strict mTLS, Connect, and gRPC.

Use the servers as standalone processes, static binaries in test pods, or containers. They are examples with deliberate limitations, rather than a production application template.

## Scope and limitations

- Users live in memory and disappear when the process restarts. The user API does not implement authentication or authorization.
- OpenTelemetry creates request spans and trace IDs, accepts incoming `traceparent` headers, and correlates access logs. Export is disabled by default; select the console or OTLP/HTTP exporter as described below.
- Automatically generated certificates are for local development. The mTLS example verifies client certificates against its CA and displays their URI SAN; it does not enforce an access policy based on a SPIFFE ID.
- Validation differs between variants. The domain service checks name length and whether an email contains `@`. Huma adds length constraints, email-format validation, and a role enum (`platform-engineer`, `sre`, `developer`). OpenAPI validates required fields, minimum name length, email format, and unknown fields against its schema. Connect uses domain validation and the library's standard Protobuf/ProtoJSON codecs; unknown ProtoJSON fields are accepted for schema compatibility.

## Project layout

```text
go-server/
├── api/
│   ├── openapi.yaml                 # OpenAPI 3.0 contract
│   ├── oapi-codegen.yaml            # OpenAPI generator configuration
│   └── proto/user/v1/user.proto     # Protobuf service contract
├── cmd/
│   ├── stdlib/                     # Standard net/http REST API
│   ├── stdlib-tls/                 # HTTPS and mTLS example
│   ├── huma/                       # Code-first OpenAPI 3.1 and Problem Details
│   ├── openapi/                    # Generated strict OpenAPI server
│   ├── connect/                    # Connect, gRPC, and HTTP/JSON
│   └── client/                     # REST/mTLS and generated RPC client CLI
├── gen/user/v1/                     # Generated Protobuf and Connect code
├── internal/
│   ├── domain/user.go              # Shared user service and domain errors
│   └── platform/                   # Server lifecycle, middleware, telemetry, TLS
├── scripts/
│   ├── generate.sh                 # Pinned generators and drift checks
│   └── package.sh                  # Static binary archives and checksums
├── examples/
│   ├── kubernetes/                 # Test Deployment and Service
│   └── tracing/                    # Jaeger and an OTLP-enabled server
├── .github/workflows/build.yml     # Tests and downloadable build artifacts
├── Dockerfile                      # Five servers and the client in one minimal image
└── Makefile
```

## Run locally

Building from source requires a Go toolchain compatible with `go.mod` (Go 1.27.3). Generated code is included, so ordinary builds do not require `protoc`.

From the project directory, run one server in each terminal as needed:

```bash
go run ./cmd/stdlib
go run ./cmd/stdlib-tls
go run ./cmd/huma
go run ./cmd/openapi
go run ./cmd/connect
```

| Executable | Default address | API |
| --- | --- | --- |
| `stdlib` | `:8081` | REST |
| `stdlib-tls` | `:8443` | REST over HTTPS and `/mtls/whoami` |
| `huma` | `:8082` | REST, `/openapi.json`, `/docs` |
| `openapi` | `:8083` | REST validated against OpenAPI |
| `connect` | `:8084` | Connect/gRPC RPC procedures |

Override the listen address with `ADDR`:

```bash
ADDR=127.0.0.1:9084 go run ./cmd/connect
```

The Makefile also provides `run-stdlib`, `run-stdlib-tls`, `run-huma`, `run-openapi`, and `run-connect`.

## Run or install directly from GitHub

Once the source is published at `github.com/abevz/go-server` and is accessible to the Go toolchain, these commands work without cloning the repository:

```bash
go run github.com/abevz/go-server/cmd/stdlib@latest
go run github.com/abevz/go-server/cmd/stdlib-tls@latest
go run github.com/abevz/go-server/cmd/huma@latest
go run github.com/abevz/go-server/cmd/openapi@latest
go run github.com/abevz/go-server/cmd/connect@latest
```

To keep an executable instead of rebuilding it through `go run`:

```bash
go install github.com/abevz/go-server/cmd/stdlib@latest
# Run the installed binary; add Go's bin directory to PATH if needed.
"$(go env GOPATH)/bin/stdlib"
```

`go install` uses `GOBIN` when set; otherwise its default destination is the Go workspace's `bin` directory. For reproducible test environments, pin a published version instead of `latest`, for example `go run github.com/abevz/go-server/cmd/stdlib@v0.1.0` after that version has been published. See the [Go module publishing guide](https://go.dev/doc/modules/publishing) and [versioned installation rules](https://go.dev/ref/mod#go-install).

## Build standalone binaries

Build all five servers and the client for the current machine:

```bash
make build
ADDR=:8080 ./dist/bin/stdlib
```

The binaries are built with `CGO_ENABLED=0`; they do not require a Go installation or a C runtime on the target machine. A Linux binary still requires the matching CPU architecture.

Cross-compile for Linux test nodes:

```bash
GOOS=linux GOARCH=amd64 make build BINDIR=dist/linux-amd64
GOOS=linux GOARCH=arm64 make build BINDIR=dist/linux-arm64
```

Create an archive containing all five servers and the client, a [binary usage guide](docs/binary-usage.md) installed as `README.md`, and both API contracts:

```bash
VERSION=v0.1.0 GOOS=linux GOARCH=amd64 make dist
VERSION=v0.1.0 GOOS=linux GOARCH=arm64 make dist

cd dist
sha256sum -c go-server_v0.1.0_linux_amd64.tar.gz.sha256
tar -xzf go-server_v0.1.0_linux_amd64.tar.gz
ADDR=:8080 ./bin/stdlib
```

`VERSION` labels the archive; it does not create a Git tag or publish a release. The default label is `dev`. `DIST_DIR` changes the archive destination.

The GitHub Actions workflow runs tests and vet, then builds Linux amd64/arm64 binary archives and Docker image archives as workflow artifacts. Download and extract the outer artifact ZIP before using the `.tar.gz` files. The workflow does not create releases or push images to a registry.

## Build and run the container

The multi-stage Dockerfile cross-compiles the five servers and the client and copies them into a `scratch` image. The image has no shell or Go toolchain, runs as UID/GID `65532`, and includes a writable `/certs` directory for the TLS example. The build context excludes local certificates, credentials, and binary archives.

```bash
make image
# Equivalent: docker build -t go-server:dev .
docker run --rm -p 8080:8080 go-server:dev
```

The image sets `ADDR=:8080` and runs `stdlib` by default. Select another executable with `--entrypoint`:

```bash
docker run --rm -p 8080:8080 \
  --entrypoint /usr/local/bin/huma go-server:dev

docker run --rm -p 8080:8080 \
  --entrypoint /usr/local/bin/connect go-server:dev
```

All variants use port 8080 inside this image because of `ADDR`. Override it with `-e ADDR=:9090` if needed. The RPC routes are listed below; Connect does not expose REST routes such as `/users/u-1`.

For HTTPS, retrieve the generated development certificates before making client requests:

```bash
docker run -d --name go-server-tls -p 8443:8080 \
  --entrypoint /usr/local/bin/stdlib-tls go-server:dev
# Wait for the "starting TLS server" message.
docker logs go-server-tls
docker cp go-server-tls:/certs ./container-certs
curl --cacert container-certs/ca.crt https://localhost:8443/users/u-1
docker rm -f go-server-tls
```

Build an image for another node architecture with BuildKit:

```bash
docker build --platform linux/arm64 -t go-server:arm64 .
```

To load a Docker image archive downloaded from the build workflow:

```bash
sha256sum -c go-server-image_linux_amd64.tar.gz.sha256
gzip -dc go-server-image_linux_amd64.tar.gz | docker load
# The workflow image tag is go-server:ci-amd64.
docker run --rm -p 8080:8080 go-server:ci-amd64
```

### Copy a binary into an existing Dockerfile

Build `go-server:dev` first, then use it as a source stage in your own Dockerfile. Replace `your-existing-image:tag` with your image's actual base:

```dockerfile
FROM go-server:dev AS test-servers
FROM your-existing-image:tag
COPY --from=test-servers /usr/local/bin/stdlib /usr/local/bin/stdlib
ENV ADDR=:8080
# Keep your application's entrypoint, or run this server directly:
CMD ["/usr/local/bin/stdlib"]
```

For a binary built locally with `GOOS=linux GOARCH=amd64 make build BINDIR=dist/linux-amd64`, the corresponding instruction in your own build context is:

```dockerfile
COPY dist/linux-amd64/stdlib /usr/local/bin/stdlib
```

The executable must match the destination image's architecture. Copying a binary does not copy the source image's environment variables or directory permissions. For `stdlib-tls`, also provide a writable `CERT_DIR` and arrange trust for its development CA.

## Run in a test Kubernetes cluster

[examples/kubernetes/deployment.yaml](examples/kubernetes/deployment.yaml) contains a Deployment and Service. It uses the local image `go-server:dev` with `imagePullPolicy: Never`, so load that image into every node that may run the pod before applying the manifest. For an existing kind cluster:

```bash
make image
kind load docker-image go-server:dev
kubectl apply -f examples/kubernetes/deployment.yaml
kubectl rollout status deployment/go-server
kubectl port-forward service/go-server 8080:8080
```

In another terminal:

```bash
curl http://localhost:8080/users/u-1
```

For other clusters, use their image-import mechanism or push the image to a registry accessible by the nodes, then update both `image` and `imagePullPolicy`. The manifest uses a non-root process, a read-only root filesystem, and an `emptyDir` mounted at `/certs`. It can also serve as a template for a test sidecar.

Change the container `command` to `/usr/local/bin/huma`, `/usr/local/bin/openapi`, `/usr/local/bin/connect`, or `/usr/local/bin/stdlib-tls` to select a variant. TCP probes work with each protocol; the Service still exposes port 8080. The TLS variant speaks HTTPS on that port. Probes check that the process accepts connections, rather than validating API behavior.

## Export spans

Every variant has HTTP server instrumentation. Set `OTEL_TRACES_EXPORTER` to choose one exporter:

| Value | Behavior |
| --- | --- |
| `none` or unset | Trace IDs and propagation remain available; completed spans are not exported. |
| `console` | Export completed spans as JSON to standard output. |
| `otlp` | Export completed spans using OTLP/HTTP with Protobuf encoding. |

Unknown values fail server startup. The exporter selector accepts one value, not a comma-separated list. This example's OTLP transport is HTTP/Protobuf; it does not switch to gRPC through `OTEL_EXPORTER_OTLP_PROTOCOL`.

### Console: no collector required

```bash
OTEL_TRACES_EXPORTER=console \
OTEL_SERVICE_NAME=go-server-stdlib \
OTEL_BSP_SCHEDULE_DELAY=1000 \
go run ./cmd/stdlib
```

Make a request from another terminal:

```bash
curl -i http://localhost:8081/users/u-1
```

The completed server span appears on standard output after the batch delay. Access logs continue on standard error and contain the same trace ID as `X-Trace-Id`. On SIGINT/SIGTERM, the server drains requests and attempts to flush pending spans with a five-second timeout.

### OTLP/HTTP and Jaeger

Start the included test stack:

```bash
docker compose -f examples/tracing/compose.yaml up --build
```

Then send a request and open the Jaeger UI at `http://localhost:16686`. Select service `go-server-stdlib`, or search for the response's trace ID:

```bash
curl -i http://localhost:8080/users/u-1
```

The Compose example runs Jaeger 2.22.0 with transient in-memory trace storage and exports spans to `http://jaeger:4318/v1/traces`. This is a local tracing demo; its ports are bound to loopback. See the [Jaeger getting-started guide](https://www.jaegertracing.io/docs/2.22/getting-started/).

For a server running directly on the host, point it at an existing collector or the Jaeger instance's published port:

```bash
OTEL_TRACES_EXPORTER=otlp \
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 \
OTEL_SERVICE_NAME=go-server-stdlib \
go run ./cmd/stdlib
```

`OTEL_EXPORTER_OTLP_ENDPOINT` is a base URL; the HTTP exporter appends `/v1/traces`. To provide a complete trace URL instead, use `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`. The SDK also reads resource attributes, sampling, and batch processor settings, for example:

```bash
OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=lab \
OTEL_TRACES_SAMPLER=always_on \
OTEL_TRACES_EXPORTER=console \
./dist/bin/stdlib
```

The default service name is the executable's variant (`stdlib`, `huma`, and so on); `OTEL_SERVICE_NAME` overrides it. The same environment variables work in the container or in a pod. Use the collector's reachable service address inside a cluster rather than a host loopback address. See [OpenTelemetry Go exporters](https://opentelemetry.io/docs/languages/go/exporters/) for transport configuration.

## Client CLI: REST, strict mTLS, gRPC, and Protobuf

The `client` executable supports `get`, `list`, and `create`. Its `rest` mode works with all four REST variants, including HTTPS. It also supports `whoami` for the TLS server's `/mtls/whoami` route.

| `-protocol` | Wire format | Default URL |
| --- | --- | --- |
| `rest` (default) | HTTP/JSON, or HTTPS when the URL starts with `https://` | `http://localhost:8081` |
| `connect` | Connect with binary Protobuf | `http://localhost:8084` |
| `connect-json` | Connect with ProtoJSON | `http://localhost:8084` |
| `grpc` | gRPC with binary Protobuf over HTTP/2 | `http://localhost:8084` |

Responses are printed as JSON even when the wire format is binary Protobuf. REST output follows the REST response shape; RPC output contains the `user` or `users` envelope. Diagnostics go to standard error. Exit codes are `0` for success/help, `1` for request or runtime failures, and `2` for invalid arguments. Requests default to a ten-second timeout; override it with `-timeout 3s`.

### Strict mTLS client

Start the server in one terminal:

```bash
MTLS_STRICT=1 go run ./cmd/stdlib-tls
```

In another terminal, present the generated client certificate and trust the server's CA:

```bash
go run ./cmd/client -protocol rest -url https://localhost:8443 \
  -ca certs/ca.crt -cert certs/client.crt -key certs/client.key \
  -op get -id u-1

# Inspect the identity verified by the TLS server.
go run ./cmd/client -url https://localhost:8443 \
  -ca certs/ca.crt -cert certs/client.crt -key certs/client.key \
  -op whoami
```

Both the CA and hostname are verified. `-cert` and `-key` must be supplied together; there is no insecure verification bypass. The same TLS options can be combined with any protocol when its endpoint uses HTTPS. A gRPC endpoint must also support HTTP/2. The lab's `connect` server uses cleartext HTTP/2 by default; `stdlib-tls` exposes REST over TLS.

### gRPC and Connect clients

Start `go run ./cmd/connect` in one terminal, then use the generated RPC client through the CLI:

```bash
go run ./cmd/client -protocol grpc -op get -id u-1
go run ./cmd/client -protocol grpc -op create \
  -name Bob -email bob@example.com -role sre
go run ./cmd/client -protocol grpc -op list

# Same generated service, binary Protobuf over Connect.
go run ./cmd/client -protocol connect -op list

# Same service using the Connect JSON codec.
go run ./cmd/client -protocol connect-json -op get -id u-1
```

Use `-url` for a different address. For example, `go run ./cmd/client -url http://localhost:8082 -op list` targets Huma. All RPC modes use the generated messages and client in `gen/user/v1`; no manual Protobuf codec is involved. See `cmd/client/main.go` for the TLS transport configuration and typed calls.

The client is included in binary archives and the container image. After publishing the source revision, it can also run without cloning:

```bash
go run github.com/abevz/go-server/cmd/client@latest -protocol grpc -op get
# Or use the locally built executable:
./dist/bin/client -protocol connect -op list
```

## Generate contracts

The source contracts are `api/openapi.yaml` and `api/proto/user/v1/user.proto`. Do not edit generated files by hand.

Generation requires Go 1.27.3 and a `protoc` executable reporting `libprotoc 36.1` from `protoc --version`. Its version is pinned because the compiler version appears in the generated `.pb.go` header. Go plugins are installed into a temporary directory rather than replacing global binaries:

| Tool | Version |
| --- | --- |
| `protoc-gen-go` | `v1.36.11` |
| `protoc-gen-connect-go` | `v1.21.1` |
| `oapi-codegen` | `v2.8.1` |

```bash
make generate         # Regenerate types and adapters.
make check-generated  # Compare fresh output without modifying project files.
```

`check-generated` fails if any generated file differs. The first run needs access to the Go module proxy to download generators.

The OpenAPI server uses a generated `StrictServerInterface` and request validation against the embedded schema. Generating Go types alone does not enforce every schema constraint; email-format validation is enabled explicitly. RPC uses generated Protobuf messages, a Connect client, and a Connect handler with codecs supplied by the Connect library. See [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen) and [protoc-gen-connect-go v1.21.1](https://pkg.go.dev/connectrpc.com/connect/cmd/protoc-gen-connect-go@v1.21.1).

## Exercise each API

Every process starts with user `u-1` (`Aleksey`). The server middleware returns `X-Trace-Id`, accepts W3C `traceparent`, and writes structured access logs.

### Standard library REST API: port 8081

Start `go run ./cmd/stdlib`:

```bash
# Fetch the initial user.
curl -i http://localhost:8081/users/u-1

# Propagate a trace ID into the response header and access log.
curl -i -H 'traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01' \
  http://localhost:8081/users/u-1

# Reject an unknown JSON field with 400.
curl -i -X POST http://localhost:8081/users \
  -H 'Content-Type: application/json' \
  -d '{"name":"Bob","email":"bob@example.com","unknown_field":123}'

# Create a user with 201; repeat the request to get 409.
curl -i -X POST http://localhost:8081/users \
  -H 'Content-Type: application/json' \
  -d '{"name":"Bob","email":"bob@example.com","role":"sre"}'
```

### TLS 1.3, HTTP/2, and mTLS: port 8443

Start `go run ./cmd/stdlib-tls`. On first startup, it generates an ECDSA P-256 development CA, a localhost server certificate, and a client certificate in `./certs/`. The client certificate has `CN=payments-svc` and URI SAN `spiffe://cluster.local/ns/prod/sa/payments`. Use `CERT_DIR` to change the directory.

```bash
# Ordinary HTTPS; curl -v shows the negotiated TLS version and ALPN.
curl -i --cacert certs/ca.crt https://localhost:8443/users/u-1

# The mTLS endpoint returns 401 without a client certificate.
curl -i --cacert certs/ca.crt https://localhost:8443/mtls/whoami

# Present the client certificate; expect the verified identity and HTTP/2.0.
curl -s --cacert certs/ca.crt \
  --cert certs/client.crt --key certs/client.key \
  https://localhost:8443/mtls/whoami | jq .

# Require a client certificate during the TLS handshake for every connection.
MTLS_STRICT=1 go run ./cmd/stdlib-tls
```

Stop the ordinary TLS process before starting strict mode on the same port.

### Huma code-first OpenAPI: port 8082

Start `go run ./cmd/huma`. The generated OpenAPI 3.1 document is at `/openapi.json`; interactive documentation is at `http://localhost:8082/docs`.

```bash
curl -s http://localhost:8082/openapi.json | jq .

# Invalid schema values produce 422 with RFC 9457 Problem Details.
curl -i -X POST http://localhost:8082/users \
  -H 'Content-Type: application/json' \
  -d '{"name":"A","email":"not-an-email","role":"invalid-role"}'
```

### Schema-first OpenAPI: port 8083

Start `go run ./cmd/openapi`:

```bash
curl -i http://localhost:8083/users/u-1

# Schema validation rejects this request before calling the service.
curl -i -X POST http://localhost:8083/users \
  -H 'Content-Type: application/json' \
  -d '{"name":"A","email":"invalid"}'
```

Unknown fields, malformed JSON, trailing JSON values, and invalid schema values return 400. A request body over 1 MiB returns 413.

### ConnectRPC and gRPC: port 8084

Start `go run ./cmd/connect`. The same port serves Connect JSON, binary Protobuf, and gRPC clients. JSON responses use standard ProtoJSON: the `.proto` field `created_at` is serialized as `createdAt`. The REST variants retain `created_at`.

```bash
curl -i -X POST http://localhost:8084/user.v1.UserService/GetUser \
  -H 'Content-Type: application/json' -d '{"id":"u-1"}'

# Domain validation and interceptor error mapping produce invalid_argument.
curl -i -X POST http://localhost:8084/user.v1.UserService/GetUser \
  -H 'Content-Type: application/json' -d '{"id":"   "}'

curl -i -X POST http://localhost:8084/user.v1.UserService/CreateUser \
  -H 'Content-Type: application/json' \
  -d '{"name":"Alice","email":"alice@example.com","role":"platform"}'
```

## Inspect protocols and packets

The shared HTTP server enables native cleartext HTTP/2 (`h2c`) alongside HTTP/1.1 through `http.Protocols`. The TLS server negotiates HTTP/2 through ALPN instead.

### gRPC with grpcurl

The generated Connect handler uses standard ProtoJSON and binary Protobuf codecs. Supply the `.proto` file to `grpcurl`; server reflection is not registered:

```bash
grpcurl -plaintext -import-path api/proto -proto user/v1/user.proto list
grpcurl -plaintext -import-path api/proto -proto user/v1/user.proto describe user.v1.UserService

grpcurl -plaintext -import-path api/proto -proto user/v1/user.proto \
  -d '{"id":"u-1"}' localhost:8084 user.v1.UserService/GetUser

# Expect the gRPC NotFound status.
grpcurl -plaintext -import-path api/proto -proto user/v1/user.proto \
  -d '{"id":"u-999"}' localhost:8084 user.v1.UserService/GetUser
```

### Compare HTTP/JSON and binary gRPC with tcpdump

Capture local test traffic in another terminal:

```bash
sudo tcpdump -i lo -nn -A 'tcp port 8084'
# Hexadecimal and ASCII output:
sudo tcpdump -i lo -nn -X 'tcp port 8084'
```

Compare the JSON `curl` request above with the binary `grpcurl` call. HTTP/1.1 headers and JSON are directly readable. HTTP/2 has binary frames and HPACK-compressed headers, so a protocol decoder is more useful than an ASCII dump. The Protobuf encoding of `GetUserRequest{id: "u-1"}` is `0a 03 75 2d 31`, preceded by gRPC's compression flag and four-byte message length.

Decode HTTP/2 and gRPC frames with tshark:

```bash
sudo tshark -i lo -f 'tcp port 8084' \
  -d tcp.port==8084,http2 -Y 'http2 or grpc' -V
```

### Inspect TLS and decrypt local test traffic

Check the certificate chain and ALPN negotiation:

```bash
openssl s_client -connect localhost:8443 -CAfile certs/ca.crt \
  -alpn h2 -tls1_3 </dev/null

openssl s_client -connect localhost:8443 -CAfile certs/ca.crt \
  -cert certs/client.crt -key certs/client.key \
  -alpn h2 -tls1_3 </dev/null
```

TLS 1.3 with ephemeral key exchange cannot be decrypted using the server's private key alone. For local debugging, use a curl TLS backend that supports `SSLKEYLOGFILE`. Start the capture before making the request so it includes the handshake:

```bash
# Capture terminal:
sudo tshark -i lo -f 'tcp port 8443' \
  -o 'tls.keylog_file:/tmp/tls-keys.log' -Y http2 -V

# Request terminal:
SSLKEYLOGFILE=/tmp/tls-keys.log curl -s --cacert certs/ca.crt \
  --cert certs/client.crt --key certs/client.key \
  https://localhost:8443/mtls/whoami
```

### Exercise cross-origin protection

The shared middleware uses `http.CrossOriginProtection`. A normal curl request without browser cross-origin headers is permitted; a simulated cross-site browser POST is rejected:

```bash
curl -i -X POST http://localhost:8081/users \
  -H 'Sec-Fetch-Site: cross-site' -H 'Content-Type: application/json' \
  -d '{"name":"Bob","email":"bob@example.com"}'
# 403 Forbidden
```

## Tests and code debugging

```bash
go test -v -race ./...
go vet ./...
make check-generated
```

Tests start temporary loopback servers on ports assigned by the OS, so fixed ports 8081–8084 and 8443 are not required. They cover basic HTTP behavior, TLS 1.3/mTLS, and Get/Create/List through generated clients using Connect Protobuf, Connect JSON, and gRPC over h2c. The gRPC test uses the shared server's protocol configuration and verifies HTTP/2. Telemetry tests receive actual OTLP/HTTP exports, check incoming trace propagation and child-span hierarchy, and verify console export and shutdown flushing. Regression tests reject malformed Protobuf and invalid OpenAPI requests without changing service state.

Use Delve to debug a server or test:

```bash
dlv debug ./cmd/stdlib --listen=:2345 --headless=true --api-version=2
dlv test ./cmd/huma -- -test.run TestHumaServer
```

Useful interactive commands include `b main.(*stdlibHandler).createUser`, `c`, `locals`, and `p req`.

## Publication

The source must be published at the module path declared in `go.mod` before the versioned remote commands can download it. Keep generated files in the published source and use immutable semantic version tags such as `v0.1.0` for repeatable installations.

Before publishing a version, run the checks above, build the required architectures, and review the English documentation. Binary archives and their `.sha256` files can be attached to a GitHub release. A registry image can be published separately after the Docker build and runtime behavior have been checked. None of the local Make targets or the build workflow publishes a release or registry image.
