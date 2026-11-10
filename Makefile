BINARY_NAME=vfeeg-backend

# tools/ is a directory: without .PHONY `make tools` would be "up to date" and install nothing
.PHONY: all tools generate build test clean run protoc atlas-hash atlas-diff

# The generated code (proto/*.pb.go, model/registration_model.go) is not tracked: run `make tools generate`
# after cloning and after changing a .proto or a JSON schema. Needs protoc on the PATH (CI pins 29.3);
# `tools` installs the generators at the versions in go.mod.
all: generate test build

tools:
	go install google.golang.org/protobuf/cmd/protoc-gen-go google.golang.org/grpc/cmd/protoc-gen-go-grpc github.com/atombender/go-jsonschema

generate: protoc
	cd model && go generate ./...

build:
	go build -o $(BINARY_NAME) -ldflags="-s -w" server.go
test:
	go test ./...
clean:
	go clean
	rm -f $(BINARY_NAME)
run: build
	./$(BINARY_NAME) -configPath .

protoc:
	protoc --experimental_allow_proto3_optional=true --proto_path=. --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative ./proto/*.proto

atlas-hash:
	atlas migrate hash --env local

atlas-diff:
	atlas migrate diff --env local
