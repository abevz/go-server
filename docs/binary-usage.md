# Go HTTP/RPC Server Lab: binary bundle

This bundle contains five static server executables and a client CLI for the OS and CPU architecture in the archive name. No Go installation or C runtime is required. Use an executable that matches your machine or container architecture.

```text
bin/stdlib
bin/stdlib-tls
bin/huma
bin/openapi
bin/connect
bin/client
api/openapi.yaml
api/proto/user/v1/user.proto
```

## Verify and extract

Place the `.tar.gz` archive and its `.sha256` file in the same directory. For example:

```bash
sha256sum -c go-server_v0.1.0_linux_amd64.tar.gz.sha256
tar -xzf go-server_v0.1.0_linux_amd64.tar.gz
```

## Run a server

Run one executable per terminal:

| Command | Default port | Example endpoint |
| --- | --- | --- |
| `./bin/stdlib` | 8081 | `GET /users/u-1` |
| `./bin/stdlib-tls` | 8443 | HTTPS `GET /users/u-1` |
| `./bin/huma` | 8082 | `GET /users/u-1`, `/docs` |
| `./bin/openapi` | 8083 | `GET /users/u-1` |
| `./bin/connect` | 8084 | `POST /user.v1.UserService/GetUser` |

For example, override the listen address and start the REST server:

```bash
ADDR=127.0.0.1:8080 ./bin/stdlib
```

In another terminal:

```bash
curl -i http://localhost:8080/users/u-1
```

For the RPC executable:

```bash
# Server terminal:
ADDR=127.0.0.1:8080 ./bin/connect

# Request terminal:
curl -i http://localhost:8080/user.v1.UserService/GetUser \
  -H 'Content-Type: application/json' -d '{"id":"u-1"}'
```

## TLS and mTLS

The TLS executable generates development certificates in `./certs/`. Set `CERT_DIR` to another writable directory if needed. Then make requests using the generated CA:

```bash
# Server terminal:
./bin/stdlib-tls

# Request terminal:
curl --cacert certs/ca.crt https://localhost:8443/users/u-1
curl --cacert certs/ca.crt --cert certs/client.crt --key certs/client.key \
  https://localhost:8443/mtls/whoami
```

Set `MTLS_STRICT=1` before starting the TLS executable to require a client certificate for every connection.

## Use the included client

For a running strict mTLS server (`MTLS_STRICT=1 ./bin/stdlib-tls`), use its generated CA and client certificate:

```bash
./bin/client -url https://localhost:8443 \
  -ca certs/ca.crt -cert certs/client.crt -key certs/client.key \
  -op get -id u-1

./bin/client -url https://localhost:8443 \
  -ca certs/ca.crt -cert certs/client.crt -key certs/client.key \
  -op whoami
```

For a running `./bin/connect` server:

```bash
./bin/client -protocol grpc -op get -id u-1
./bin/client -protocol grpc -op create -name Bob -email bob@example.com -role sre
./bin/client -protocol grpc -op list
./bin/client -protocol connect -op list       # Binary Protobuf over Connect.
./bin/client -protocol connect-json -op list  # ProtoJSON over Connect.
```

The RPC modes default to `http://localhost:8084`. REST defaults to `http://localhost:8081`; `-url` overrides either address. Use `-protocol rest` (the default) for the standard REST variants. Responses are JSON on stdout; errors go to stderr with a nonzero exit status. `-timeout` defaults to `10s`, and `-help` lists all options. TLS verifies the server's certificate and hostname, and requires both `-cert` and `-key` when a client certificate is configured.

## Export request spans

Console output requires no collector:

```bash
OTEL_TRACES_EXPORTER=console OTEL_BSP_SCHEDULE_DELAY=1000 \
OTEL_SERVICE_NAME=go-server-stdlib ./bin/stdlib
```

For an existing OTLP/HTTP collector:

```bash
OTEL_TRACES_EXPORTER=otlp \
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 \
OTEL_SERVICE_NAME=go-server-stdlib ./bin/stdlib
```

The base endpoint gets `/v1/traces` appended. Use `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` for a complete URL. Export defaults to `none`. Stop the server with SIGINT/SIGTERM to drain requests and flush pending spans.

## Add a binary to a test image

Place the extracted bundle in your Docker build context. Replace the base image placeholder with your own image:

```dockerfile
FROM your-existing-image:tag
COPY bin/stdlib /usr/local/bin/stdlib
ENV ADDR=:8080
CMD ["/usr/local/bin/stdlib"]
```

Match the executable's architecture to the destination image. For the TLS variant, provide a writable `CERT_DIR` and arrange client trust for the generated development CA.

These are in-memory test servers: user data is lost on restart, and the user API has no authentication or authorization. Connect JSON uses `createdAt`; the REST variants use `created_at`.

The bundle contains runnable binaries and API contracts. Source builds, code generation, Kubernetes manifests, and the Jaeger Compose example are part of the source distribution. Once published, find them in the [project repository](https://github.com/abevz/go-server).
