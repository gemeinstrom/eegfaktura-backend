FROM golang:1.26.10 AS builder

RUN apt update && apt install -y protobuf-compiler
WORKDIR /usr/src/app

# pre-copy/cache go.mod for pre-downloading dependencies and only redownloading them in subsequent builds if they change
COPY go.mod go.sum ./
RUN go mod download && go mod verify
# the code generators at the versions in go.mod, as the CI builds with (never @latest)
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go google.golang.org/grpc/cmd/protoc-gen-go-grpc     github.com/atombender/go-jsonschema

COPY . .

ENV PATH="/go/bin:${PATH}"

# Codegen muss VOR `go mod tidy` / `go install ./...` / `go build` laufen (generierter Code ist
# nicht eingecheckt): `make generate` erzeugt das protobuf-Package und model/registration_model.go,
# wie in der CI. Ohne sie fehlt das Package proto (tidy scheitert) bzw. `undefined: protobuf.*`.
RUN make generate

RUN go mod tidy
RUN go install ./...
RUN go build -o /usr/local/bin/vfeeg-backend -ldflags="-s -w" server.go

# Laufzeit: schlankes Debian derselben Release wie das Build-Image (golang:1.26.10 = trixie), ohne
# Go-Toolchain, git, ssh, curl. Das Binary ist dynamisch gegen glibc gelinkt (cgo an), daher kein
# scratch/static. Gebraucht werden nur CA-Zertifikate (ausgehendes HTTPS, SMTP mit TLS) und Zonendaten
# (TZ, time.Local). Datierter Tag = unveraenderlich; veroeffentlicht 2026-10-28.
FROM debian:trixie-20261027-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/*

ENV TZ="Europe/Berlin"

# service.version in every log line; the release workflow passes the commit (the version for a tag build)
ARG SERVICE_VERSION=dev
ENV SERVICE_VERSION=${SERVICE_VERSION}

COPY --from=builder /usr/local/bin/vfeeg-backend /usr/local/bin/vfeeg-backend
COPY config.yaml /etc/backend/

# Copy the entrypoint
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
# Make it executable
RUN chmod +x /usr/local/bin/entrypoint.sh

WORKDIR /usr/src/app
#RUN rm -r ./*

VOLUME /opt/storage

EXPOSE 8080

USER 1000

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["vfeeg-backend", "-configPath", "/etc/backend/"]