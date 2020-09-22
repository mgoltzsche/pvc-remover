# Build the manager binary
FROM golang:1.13-alpine3.12 AS builder

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY main.go main.go
COPY controllers/*_controller.go controllers/

# Build
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on
RUN go build -a -o manager -ldflags "-s -w -extldflags '-static'" main.go


# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot AS manager
WORKDIR /
COPY --from=builder /workspace/manager .
USER nonroot:nonroot
ENTRYPOINT ["/manager"]


# Download kubebuilder and run tests
FROM alpine:3.12 AS kubebuilder
ENV KUBEBUILDER_VERSION=2.3.1
RUN wget -O - https://go.kubebuilder.io/dl/$KUBEBUILDER_VERSION/linux/amd64 | tar -xz -C /tmp/ \
	&& mv /tmp/kubebuilder_${KUBEBUILDER_VERSION}_linux_amd64 /usr/local/kubebuilder
FROM builder
COPY --from=kubebuilder /usr/local/kubebuilder /usr/local/kubebuilder
COPY controllers/*_test.go controllers/
RUN go test ./... -coverprofile cover.out


FROM manager
